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

// Row is one selectable line in the content pane.
//
//   - Value (plus ValueColor) is the right-aligned detail shown on the same
//     line, e.g. a traffic figure or a state.
//   - Sub opens another pane of rows, which is how a user's detail page is
//     reached from the user list.
//   - Run executes an action; Confirm asks first when the change is
//     destructive or hard to undo.
//   - Options turns the row into a choice dialog (used by autostart).
type Row struct {
	Label      string
	Value      string
	ValueColor string
	Confirm    string
	Options    []string
	Run        func() (string, error)
	Sub        func() []Row
	// Icon is the emoji shown between the number and the label (main menu
	// rows only); Rule renders a dim separator line instead of a numbered
	// entry.
	Icon string
	Rule bool
}

// category is one entry of the main menu. The icon is the emoji shown left of
// the number (like the reference shell menu); summary renders the small
// right-aligned figure that gives the section at a glance ("2 users"), with an
// optional colour for state-like values.
type category struct {
	title   string
	icon    string
	rows    func() []Row
	summary func() (string, string)
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

// serviceRows mirrors the classic shell menu's service section: the panel
// unit is controlled through the init system, exactly like `3m-ui start`.
func (c *Console) serviceRows() []Row {
	s := c.strings
	boot := s.Disable
	if c.enabled {
		boot = s.Enable
	}
	return []Row{
		{
			Label:   s.ActStart,
			Confirm: s.ConfirmStart,
			Run: func() (string, error) {
				return s.ActStart, c.runService("start")
			},
		},
		{
			Label:   s.ActStop,
			Confirm: s.ConfirmStop,
			Run: func() (string, error) {
				return s.ActStop, c.runService("stop")
			},
		},
		{
			Label:   s.ActRestart,
			Confirm: s.ConfirmRestart,
			Run: func() (string, error) {
				return s.ActRestart, c.runService("restart")
			},
		},
		{
			Label:   s.ActBoot,
			Options: []string{s.Enable, s.Disable},
			Value:   boot,
		},
		{
			Label: s.ActLogs,
			Run: func() (string, error) {
				return "", c.showServiceLogs()
			},
		},
	}
}
