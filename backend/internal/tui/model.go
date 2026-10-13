package tui

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kazeyukiro/3m-ui/backend/internal/buildinfo"
)

const serviceName = "3m-ui"

type initKind int

const (
	initSystemd initKind = iota
	initOpenRC
	initNone
)

func detectInit() initKind {
	if _, err := exec.LookPath("systemctl"); err == nil {
		return initSystemd
	}
	if _, err := exec.LookPath("rc-service"); err == nil {
		return initOpenRC
	}
	return initNone
}

// Row is one selectable line in the content pane. Action marks it as runnable;
// Confirm carries the question a modal must ask before running it.
type Row struct {
	Label      string
	Value      string
	ValueColor string
	Action     string
	Confirm    string
	Options    []string
}

type certInfo struct {
	Name   string
	Issuer string
	Expiry time.Time
}

func (c *certInfo) daysLeft() int {
	return int(time.Until(c.Expiry).Hours() / 24)
}

// loadCerts reads every PEM certificate in dir. Errors are skipped on purpose:
// a single unreadable file must not take the whole pane down.
func loadCerts(dir string) []certInfo {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []certInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".crt" && ext != ".pem" && ext != ".cer" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		block, _ := pem.Decode(data)
		if block == nil {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		out = append(out, certInfo{
			Name:   strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())),
			Issuer: cert.Issuer.CommonName,
			Expiry: cert.NotAfter,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// --- service control -------------------------------------------------------

func (c *Console) serviceActive() bool {
	switch c.init {
	case initSystemd:
		return exec.Command("systemctl", "is-active", "--quiet", serviceName).Run() == nil
	case initOpenRC:
		return exec.Command("rc-service", serviceName, "status").Run() == nil
	}
	return false
}

func (c *Console) serviceEnabled() bool {
	switch c.init {
	case initSystemd:
		return exec.Command("systemctl", "is-enabled", "--quiet", serviceName).Run() == nil
	case initOpenRC:
		// OpenRC uses runlevel symlinks; rc-update is the closest equivalent.
		return exec.Command("rc-update", "show").Run() == nil
	}
	return false
}

// runService executes start/stop/restart/enable/disable.
func (c *Console) runService(action string) error {
	switch c.init {
	case initSystemd:
		return exec.Command("systemctl", action, serviceName).Run()
	case initOpenRC:
		return exec.Command("rc-service", serviceName, action).Run()
	}
	return errors.New("no supported init system (need systemd or OpenRC)")
}

// serviceLogs returns the tail of the service log. journalctl when systemd,
// otherwise the last lines of the panel log file if we can find one.
func (c *Console) serviceLogs(maxLines int) (string, error) {
	if c.init == initSystemd {
		out, err := exec.Command("journalctl", "-u", serviceName, "-n", strconv.Itoa(maxLines), "--no-pager").Output()
		if err != nil {
			return "", err
		}
		return string(out), nil
	}
	for _, p := range []string{"/var/log/3m-ui.log", "/var/log/3m-ui/3m-ui.log"} {
		if b, err := os.ReadFile(p); err == nil {
			lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
			if len(lines) > maxLines {
				lines = lines[len(lines)-maxLines:]
			}
			return strings.Join(lines, "\n"), nil
		}
	}
	return "", errors.New("no journalctl and no panel log file")
}

// --- pane contents ---------------------------------------------------------

func (c *Console) reload() {
	c.active = c.serviceActive()
	c.enabled = c.serviceEnabled()
	c.certs = loadCerts(c.certDir)
}

func (c *Console) overviewRows() []Row {
	s := c.strings
	state := s.Running
	color := green
	if !c.active {
		state = s.Stopped
		color = red
	}
	listen := c.cfg.Server.Listen
	if listen == "" {
		listen = s.ListenAll
	}
	pub := c.cfg.Server.PublicURL
	if pub == "" {
		pub = s.None
	}
	boot := s.Disable
	if c.enabled {
		boot = s.Enable
	}
	return []Row{
		{Label: s.Version, Value: buildinfo.Version},
		{Label: "状态", Value: state, ValueColor: color},
		{Label: s.Port, Value: strconv.Itoa(c.cfg.Server.Port)},
		{Label: s.Listen, Value: listen},
		{Label: s.PublicURL, Value: pub},
		{Label: s.Autostart, Value: boot},
		{Label: s.CertCount, Value: strconv.Itoa(len(c.certs))},
	}
}

func (c *Console) serviceRows() []Row {
	return []Row{
		{Label: "启动", Action: "start", Confirm: c.strings.ConfirmStart},
		{Label: "停止", Action: "stop", Confirm: c.strings.ConfirmStop},
		{Label: "重启", Action: "restart", Confirm: c.strings.ConfirmRestart},
		{Label: c.strings.ActBoot, Action: "boot", Options: []string{c.strings.Enable, c.strings.Disable}},
		{Label: "查看日志", Action: "logs"},
	}
}

func (c *Console) certRows() []Row {
	s := c.strings
	if len(c.certs) == 0 {
		return []Row{{Label: s.NoCerts, Value: c.certDir}}
	}
	rows := make([]Row, 0, len(c.certs))
	for _, cert := range c.certs {
		days := cert.daysLeft()
		color := green
		if days < 0 {
			color = red
		} else if days <= 30 {
			color = yellow
		}
		value := cert.Expiry.Format("2006-01-02")
		if days < 0 {
			value = s.Expired
		}
		rows = append(rows, Row{Label: cert.Name, Value: value, ValueColor: color})
	}
	return rows
}

func (c *Console) systemRows() []Row {
	return []Row{
		{Label: "版本", Value: buildinfo.Version},
		{Label: "git commit", Value: buildinfo.GitCommit},
		{Label: "构建时间", Value: buildinfo.BuildTime},
		{Label: "配置文件", Value: c.configPath},
		{Label: "数据库", Value: c.cfg.Database.Path},
	}
}
