package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kazeyukiro/3m-ui/backend/internal/buildinfo"
	"github.com/kazeyukiro/3m-ui/backend/internal/cluster"
	panelconfig "github.com/kazeyukiro/3m-ui/backend/internal/config"
	"github.com/kazeyukiro/3m-ui/backend/internal/database/models"
	"github.com/kazeyukiro/3m-ui/backend/internal/listener"
	mhconfig "github.com/kazeyukiro/3m-ui/backend/internal/mihomo/config"
	"github.com/kazeyukiro/3m-ui/backend/internal/protocol"
	"github.com/kazeyukiro/3m-ui/backend/internal/telegram"
	"github.com/kazeyukiro/3m-ui/backend/internal/user"
	"gorm.io/gorm"
)

// --- formatting helpers -----------------------------------------------------

// humanBytes renders a byte count the way the panel's own UI does: binary
// units with one decimal, and never more precision than operators need when
// eyeballing a quota.
func humanBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	const unit = 1024
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	v := float64(n)
	i := 0
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	if i == 0 {
		return strconv.FormatInt(n, 10) + " B"
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

// humanRate renders bytes/second.
func humanRate(bps int64) string {
	if bps <= 0 {
		return "0 B/s"
	}
	return humanBytes(bps) + "/s"
}

func humanTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02 15:04")
}

func (c *Console) onOff(v bool) string {
	if v {
		return c.strings.Enable
	}
	return c.strings.Disable
}

// dbErrorRow is what a pane shows when the panel database cannot be read.
func (c *Console) dbErrorRow() []Row {
	msg := c.strings.DBUnavail
	if c.dbErr != nil {
		msg += ": " + c.dbErr.Error()
	}
	return []Row{{Label: msg}}
}

// reloadConfig asks the running panel to regenerate and apply the core config
// after a direct database write. It is best effort: when the panel is down the
// change simply waits for the next apply, which is what the footer says.
func (c *Console) reloadConfig() error {
	if len(c.listeners) == 0 {
		return nil
	}
	return c.apiPost("/api/v1/listeners/" + strconv.FormatUint(uint64(c.listeners[0].ID), 10) + "/reload")
}

// --- dashboard (web UI: /) -------------------------------------------------

func (c *Console) dashboardRows() []Row {
	s := c.strings
	listen := c.cfg.Server.Listen
	if listen == "" {
		listen = s.ListenAll
	}
	pub := c.cfg.Server.PublicURL
	if pub == "" {
		pub = s.None
	}

	rows := []Row{
		// versionTag(), not "v"+Version: release builds already carry the
		// prefix, so the header and this row must agree.
		{Label: s.Version, Value: versionTag()},
		{Label: s.State, Value: c.stateLabel(), ValueColor: c.stateColor()},
	}
	if c.active {
		rows = append(rows, Row{Label: s.PanelAPI, Value: c.apiLabel(), ValueColor: c.apiColor()})
	}
	rows = append(rows,
		Row{Label: s.CoreState, Value: c.coreLabel(), ValueColor: c.coreColor()},
		Row{Label: s.Port, Value: strconv.Itoa(c.cfg.Server.Port)},
		Row{Label: s.Listen, Value: listen},
		Row{Label: s.PublicURL, Value: pub},
	)
	if c.dbErr == nil {
		active := 0
		for _, u := range c.users {
			if user.IsCredentialActive(u) {
				active++
			}
		}
		enabled := 0
		for _, l := range c.listeners {
			if l.Enabled {
				enabled++
			}
		}
		var up, down, used int64
		for _, u := range c.users {
			up += u.UploadBytes
			down += u.DownloadBytes
			used += u.TrafficUsed
		}
		rows = append(rows,
			Row{Label: s.UsersTotal, Value: strconv.Itoa(len(c.users))},
			Row{Label: s.UsersActive, Value: strconv.Itoa(active), ValueColor: green},
			Row{Label: s.ListenersAll, Value: strconv.Itoa(len(c.listeners))},
			Row{Label: s.ListenersOn, Value: strconv.Itoa(enabled), ValueColor: green},
			Row{Label: s.TrafficAll, Value: humanBytes(used)},
			Row{Label: s.Upload, Value: humanBytes(up)},
			Row{Label: s.Download, Value: humanBytes(down)},
			Row{Label: s.CertCount, Value: strconv.Itoa(len(c.certs))},
		)
		if c.trafficFetched && c.trafficOK {
			rows = append(rows,
				Row{Label: s.LiveRate, Value: "↑ " + humanRate(c.trafficSt.UploadRate) + "  ↓ " + humanRate(c.trafficSt.DownloadRate)},
				Row{Label: s.Connections, Value: strconv.Itoa(c.trafficSt.Connections)},
			)
		}
		if c.coreFetched && c.coreOK && c.coreSt.Running {
			rows = append(rows,
				Row{Label: s.CoreVersion, Value: c.coreSt.Version},
				Row{Label: s.CorePID, Value: strconv.Itoa(c.coreSt.PID)},
				Row{Label: s.Uptime, Value: c.coreSt.Uptime},
			)
		}
	} else {
		rows = append(rows, Row{Label: s.DBUnavail, Value: c.dbErr.Error(), ValueColor: red})
	}
	if !c.active {
		rows = append(rows, Row{Label: s.OfflineTip, ValueColor: yellow})
	}
	return rows
}

// --- users (web UI: /users) ------------------------------------------------

func (c *Console) usersRows() []Row {
	if c.dbErr != nil {
		return c.dbErrorRow()
	}
	if len(c.users) == 0 {
		return []Row{{Label: c.strings.NoUsers}}
	}
	rows := make([]Row, 0, len(c.users))
	for _, u := range c.users {
		u := u
		_, color := c.userState(u)
		rows = append(rows, Row{
			Label:      u.Username,
			Value:      humanBytes(u.TrafficUsed) + " / " + c.limitLabel(u.TrafficLimit),
			ValueColor: color,
			Sub:        func() []Row { return c.userDetailByID(u.ID) },
		})
	}
	return rows
}

func (c *Console) userState(u models.ProxyUser) (string, string) {
	s := c.strings
	if !u.Enabled {
		return s.Disable, red
	}
	if !u.ExpireTime.IsZero() && time.Now().After(u.ExpireTime) {
		return s.Expired, red
	}
	if u.TrafficLimit > 0 && u.TrafficUsed >= u.TrafficLimit {
		return s.Failed, red
	}
	if user.IsCredentialActive(u) {
		return s.Enable, green
	}
	return s.Unknown, yellow
}

// ipLimitLabel renders the concurrent-IP cap, where zero means unlimited.
func (c *Console) ipLimitLabel(limit int) string {
	if limit <= 0 {
		return c.strings.Unlimited
	}
	return strconv.Itoa(limit)
}

func (c *Console) limitLabel(limit int64) string {
	if limit <= 0 {
		return c.strings.Unlimited
	}
	return humanBytes(limit)
}

// userDetailByID rebuilds a user's page from the current record. Looking the
// user up by ID (instead of closing over a copy) matters after an action: the
// pane must show the new state, exactly like the web UI re-fetches.
func (c *Console) userDetailByID(id uint) []Row {
	for _, u := range c.users {
		if u.ID == id {
			return c.userDetail(u)
		}
	}
	return []Row{{Label: c.strings.NoUsers}}
}

func (c *Console) userDetail(u models.ProxyUser) []Row {
	s := c.strings
	state, color := c.userState(u)
	expire := s.Never
	if !u.ExpireTime.IsZero() {
		expire = humanTime(u.ExpireTime)
	}
	last := s.None
	if u.LastSeen != nil {
		last = humanTime(*u.LastSeen)
	}
	token := u.SubToken
	if token == "" {
		token = s.NoSubToken
	}
	return []Row{
		{Label: s.Username, Value: u.Username},
		{Label: s.State, Value: state, ValueColor: color},
		{Label: s.TrafficUsed, Value: humanBytes(u.TrafficUsed)},
		{Label: s.TrafficLimit, Value: c.limitLabel(u.TrafficLimit)},
		{Label: s.Upload, Value: humanBytes(u.UploadBytes)},
		{Label: s.Download, Value: humanBytes(u.DownloadBytes)},
		{Label: s.ExpireAt, Value: expire},
		{Label: s.Online, Value: c.onOff(u.Online)},
		{Label: s.LastSeen, Value: last},
		{Label: s.IPLimit, Value: c.ipLimitLabel(u.IPLimit)},
		{Label: s.SubToken, Value: token},
		{Label: s.Remark, Value: u.Remark},
		{Label: "—", Value: ""},
		{
			Label:   c.userToggleLabel(u),
			Confirm: c.userToggleConfirm(u),
			Run: func() (string, error) {
				return c.userToggle(u)
			},
		},
		{
			Label:   s.ActResetTraffic,
			Confirm: s.ConfirmResetTraffic,
			Run: func() (string, error) {
				return s.ActResetTraffic, c.userResetTraffic(u.ID)
			},
		},
		{
			Label:   s.ActRotateToken,
			Confirm: s.ConfirmRotateToken,
			Run: func() (string, error) {
				return s.ActRotateToken, c.userRotateToken(u.ID)
			},
		},
		{
			Label:   s.ActDeleteUser,
			Confirm: s.ConfirmDeleteUser,
			Run: func() (string, error) {
				return s.ActDeleteUser, c.userDelete(u.ID)
			},
		},
	}
}

// --- listeners (web UI: /listeners) ----------------------------------------

func (c *Console) listenersRows() []Row {
	if c.dbErr != nil {
		return c.dbErrorRow()
	}
	if len(c.listeners) == 0 {
		return []Row{{Label: c.strings.NoListeners}}
	}
	rows := make([]Row, 0, len(c.listeners))
	for _, l := range c.listeners {
		l := l
		_, color := c.listenerState(l)
		rows = append(rows, Row{
			Label:      l.Name,
			Value:      l.Protocol + ":" + l.Port,
			ValueColor: color,
			Sub:        func() []Row { return c.listenerDetailByID(l.ID) },
		})
	}
	return rows
}

func (c *Console) listenerState(l models.Listener) (string, string) {
	s := c.strings
	if !l.Enabled {
		return s.Disable, yellow
	}
	if rt, ok := c.runtime[l.ID]; ok && c.runtimeOK {
		switch rt.State {
		case "listening":
			return s.Running, green
		case "not_listening":
			return s.Stopped, red
		}
		return s.Unknown, yellow
	}
	return s.Enable, green
}

func (c *Console) listenerDetailByID(id uint) []Row {
	for _, l := range c.listeners {
		if l.ID == id {
			return c.listenerDetail(l)
		}
	}
	return []Row{{Label: c.strings.NoListeners}}
}

func (c *Console) listenerDetail(l models.Listener) []Row {
	s := c.strings
	state, color := c.listenerState(l)
	bind := l.BindAddress
	if bind == "" {
		bind = s.ListenAll
	}
	host := l.PublicHost
	if host == "" {
		host = s.None
	}
	port := l.PublicPort
	if port == "" {
		port = s.None
	}
	return []Row{
		{Label: s.Username, Value: l.Name},
		{Label: s.Protocol, Value: l.Protocol},
		{Label: s.Port, Value: l.Port},
		{Label: s.BindAddress, Value: bind},
		{Label: s.Runtime, Value: state, ValueColor: color},
		{Label: s.TLS, Value: c.onOff(l.TLS)},
		{Label: s.UDP, Value: c.onOff(l.UDP)},
		{Label: s.Multiplier, Value: fmt.Sprintf("%.2f", l.TrafficMultiplier)},
		{Label: s.PublicHost, Value: host},
		{Label: s.PublicPort, Value: port},
		{Label: s.SNI, Value: l.AccessSNI},
		{Label: "—", Value: ""},
		{
			Label:   c.listenerToggleLabel(l),
			Confirm: c.listenerToggleConfirm(l),
			Run: func() (string, error) {
				return c.listenerToggle(l)
			},
		},
	}
}

// --- share (web UI: /share) ------------------------------------------------

func (c *Console) shareRows() []Row {
	if c.dbErr != nil {
		return c.dbErrorRow()
	}
	if len(c.users) == 0 {
		return []Row{{Label: c.strings.NoUsers}}
	}
	rows := make([]Row, 0, len(c.users))
	for _, u := range c.users {
		u := u
		rows = append(rows, Row{
			Label: u.Username,
			Value: c.strings.ShareHint,
			Sub:   func() []Row { return c.shareDetailByID(u.ID) },
		})
	}
	return rows
}

func (c *Console) shareDetailByID(id uint) []Row {
	for _, u := range c.users {
		if u.ID == id {
			return c.shareDetail(u)
		}
	}
	return []Row{{Label: c.strings.NoUsers}}
}

func (c *Console) shareDetail(u models.ProxyUser) []Row {
	s := c.strings
	if u.SubToken == "" {
		return []Row{{Label: s.NoSubToken, ValueColor: yellow}}
	}
	return []Row{
		{Label: s.ShareTip, ValueColor: dim},
		{Label: s.SubToken, Value: u.SubToken},
		{Label: "—", Value: ""},
		{Label: s.FmtMihomo, Value: c.subURL(u, "mihomo")},
		{Label: s.FmtV2Ray, Value: c.subURL(u, "v2ray")},
		{Label: s.FmtSingbox, Value: c.subURL(u, "singbox")},
		{Label: s.FmtURI, Value: c.subURL(u, "uri")},
		{Label: s.FmtHTML, Value: c.subURL(u, "html")},
		{Label: "—", Value: ""},
		{
			Label:   s.ActRotateToken,
			Confirm: s.ConfirmRotateToken,
			Run: func() (string, error) {
				return s.ActRotateToken, c.userRotateToken(u.ID)
			},
		},
	}
}

// subURL builds a subscription link the same way the panel API does.
func (c *Console) subURL(u models.ProxyUser, target string) string {
	return panelconfig.GetSubscriptionURL(c.cfg, nil, u.SubToken, target)
}

// --- traffic (web UI: /traffic) -------------------------------------------

func (c *Console) trafficRows() []Row {
	s := c.strings
	if c.dbErr != nil {
		return c.dbErrorRow()
	}
	rows := []Row{}
	if c.trafficFetched {
		if c.trafficOK {
			rows = append(rows,
				Row{Label: s.LiveRate, Value: "↑ " + humanRate(c.trafficSt.UploadRate) + "  ↓ " + humanRate(c.trafficSt.DownloadRate)},
				Row{Label: s.Connections, Value: strconv.Itoa(c.trafficSt.Connections)},
				Row{Label: s.Upload, Value: humanBytes(c.trafficSt.UploadBytes)},
				Row{Label: s.Download, Value: humanBytes(c.trafficSt.DownloadBytes)},
			)
		} else {
			rows = append(rows, Row{Label: s.APIDown, ValueColor: yellow})
		}
	}
	if len(c.users) == 0 {
		return append(rows, Row{Label: s.NoTraffic})
	}
	sorted := make([]models.ProxyUser, len(c.users))
	copy(sorted, c.users)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].TrafficUsed > sorted[j].TrafficUsed })
	rows = append(rows, Row{Label: "— " + s.Ranking + " —", ValueColor: dim})
	for _, u := range sorted {
		u := u
		rows = append(rows, Row{
			Label: u.Username,
			Value: humanBytes(u.TrafficUsed) + " / " + c.limitLabel(u.TrafficLimit),
			Sub: func() []Row {
				return []Row{
					{Label: s.Username, Value: u.Username},
					{Label: s.Upload, Value: humanBytes(u.UploadBytes)},
					{Label: s.Download, Value: humanBytes(u.DownloadBytes)},
					{Label: s.Total, Value: humanBytes(u.TrafficUsed)},
					{Label: s.TrafficLimit, Value: c.limitLabel(u.TrafficLimit)},
				}
			},
		})
	}
	return rows
}

// --- cluster (web UI: /cluster) -------------------------------------------

func (c *Console) clusterRows() []Row {
	if c.dbErr != nil {
		return c.dbErrorRow()
	}
	if len(c.servers) == 0 {
		return []Row{{Label: c.strings.NoServers}}
	}
	rows := make([]Row, 0, len(c.servers))
	for _, srv := range c.servers {
		srv := srv
		status, color := c.serverState(srv)
		rows = append(rows, Row{
			Label:      srv.Name,
			Value:      status,
			ValueColor: color,
			Sub:        func() []Row { return c.serverDetail(srv) },
		})
	}
	return rows
}

func (c *Console) serverState(s models.RemoteServer) (string, string) {
	switch s.LastStatus {
	case "up":
		return c.strings.Running, green
	case "down", "error":
		return c.strings.Stopped, red
	}
	return c.strings.Unknown, yellow
}

func (c *Console) serverDetail(s models.RemoteServer) []Row {
	st := c.strings
	checked := st.None
	if s.LastCheckAt != nil {
		checked = humanTime(*s.LastCheckAt)
	}
	status, color := c.serverState(s)
	rows := []Row{
		{Label: st.RemotePanel, Value: s.Name},
		{Label: st.BaseURL, Value: s.BaseURL},
		{Label: st.LastStatus, Value: status, ValueColor: color},
		{Label: st.LastCheck, Value: checked},
		{Label: st.State, Value: c.onOff(s.Enabled)},
		{Label: st.Remark, Value: s.Remark},
		{Label: "— " + st.MirrorNodes + " —", ValueColor: dim},
	}
	mirrors := 0
	for _, m := range c.mirrors {
		if m.RemoteServerID != s.ID {
			continue
		}
		mirrors++
		rows = append(rows, Row{Label: m.Name, Value: m.Protocol + ":" + m.Port, ValueColor: green})
	}
	if mirrors == 0 {
		rows = append(rows, Row{Label: st.NoMirrors, ValueColor: dim})
	}
	return rows
}

// --- routing (web UI: /routing) -------------------------------------------

func (c *Console) routingRows() []Row {
	if c.dbErr != nil {
		return c.dbErrorRow()
	}
	s := c.strings
	rows := []Row{
		{Label: "— " + s.ClientRules + " —", ValueColor: dim},
		{Label: s.DNSMode + " / mode", Value: c.visual.Mode + " / " + c.dnsMode()},
		{Label: s.Rules, Value: strconv.Itoa(len(c.visual.Rules))},
		{Label: s.Groups, Value: strconv.Itoa(len(c.visual.Groups))},
		{Label: s.Protocol + " proxies", Value: strconv.Itoa(len(c.visual.Proxies))},
		{Label: "— " + s.ServerRules + " —", ValueColor: dim},
		{Label: s.Rules, Value: strconv.Itoa(len(c.serverRt.Rules))},
		{Label: s.Groups, Value: strconv.Itoa(len(c.serverRt.Groups))},
		{Label: s.RuleProvider, Value: strconv.Itoa(len(c.serverRt.RuleProviders))},
		{Label: s.WarpGlobal, Value: c.onOff(c.serverRt.WarpGlobal)},
		{Label: s.WarpDomains, Value: strconv.Itoa(len(c.serverRt.WarpDomains))},
	}
	if len(c.serverRt.Rules) > 0 {
		rows = append(rows, Row{
			Label: "查看服务端规则 / view server rules",
			Sub:   c.ruleRows(c.serverRt.Rules),
		})
	}
	if len(c.visual.Rules) > 0 {
		rows = append(rows, Row{
			Label: "查看客户端规则 / view client rules",
			Sub:   c.ruleRows(c.visual.Rules),
		})
	}
	return rows
}

func (c *Console) ruleRows(rules []string) func() []Row {
	return func() []Row {
		if len(rules) == 0 {
			return []Row{{Label: c.strings.NoRules}}
		}
		rows := make([]Row, 0, len(rules))
		for i, r := range rules {
			rows = append(rows, Row{Label: strconv.Itoa(i + 1), Value: r})
		}
		return rows
	}
}

func (c *Console) dnsMode() string {
	if !c.visual.DNS.Enable {
		return c.strings.Disable
	}
	if c.visual.DNS.EnhancedMode != "" {
		return c.visual.DNS.EnhancedMode
	}
	return c.strings.Enable
}

// --- core (web UI: /core) -------------------------------------------------

func (c *Console) coreRows() []Row {
	s := c.strings
	rows := []Row{
		{Label: s.CoreRunning, Value: c.coreLabel(), ValueColor: c.coreColor()},
	}
	if c.coreFetched && c.coreOK {
		rows = append(rows,
			Row{Label: s.CoreVersion, Value: c.coreSt.Version},
			Row{Label: s.CorePID, Value: strconv.Itoa(c.coreSt.PID)},
			Row{Label: s.Uptime, Value: c.coreSt.Uptime},
		)
	} else if !c.active {
		rows = append(rows, Row{Label: s.Offline, Value: s.OfflineTip, ValueColor: yellow})
	} else if c.coreFetched {
		rows = append(rows, Row{Label: s.APIDown, ValueColor: yellow})
	}
	rows = append(rows, Row{Label: "—", Value: ""})
	if c.active {
		rows = append(rows,
			Row{Label: s.ActCoreStart, Confirm: s.ConfirmCoreStart, Run: func() (string, error) {
				return s.ActCoreStart, c.apiPost("/api/v1/mihomo/start")
			}},
			Row{Label: s.ActCoreStop, Confirm: s.ConfirmCoreStop, Run: func() (string, error) {
				return s.ActCoreStop, c.apiPost("/api/v1/mihomo/stop")
			}},
			Row{Label: s.ActCoreRestart, Confirm: s.ConfirmCoreRestart, Run: func() (string, error) {
				return s.ActCoreRestart, c.apiPost("/api/v1/mihomo/restart")
			}},
			Row{Label: s.CoreLogs, Run: func() (string, error) {
				return "", c.showCoreLogs()
			}},
		)
	} else {
		rows = append(rows, Row{Label: s.OfflineTip, ValueColor: yellow})
	}
	return rows
}

// --- logs (web UI: /logs) -------------------------------------------------

func (c *Console) logsRows() []Row {
	s := c.strings
	rows := []Row{
		{Label: s.SvcLogs, Run: func() (string, error) { return "", c.showServiceLogs() }},
	}
	if c.active {
		rows = append(rows,
			Row{Label: s.PanelLogs, Run: func() (string, error) { return "", c.showPanelLogs() }},
			Row{Label: s.CoreLogs, Run: func() (string, error) { return "", c.showCoreLogs() }},
		)
	} else {
		rows = append(rows, Row{Label: s.OfflineTip, ValueColor: yellow})
	}
	return rows
}

// --- config (web UI: /config) ---------------------------------------------

func (c *Console) configRows() []Row {
	s := c.strings
	listen := c.cfg.Server.Listen
	if listen == "" {
		listen = s.ListenAll
	}
	pub := c.cfg.Server.PublicURL
	if pub == "" {
		pub = s.None
	}
	subListen := c.cfg.Server.SubListen
	if subListen == "" {
		subListen = listen
	}
	subPort := s.None
	if c.cfg.Server.SubPort > 0 {
		subPort = strconv.Itoa(c.cfg.Server.SubPort)
	}
	subPath := c.cfg.Server.SubPath
	if subPath == "" {
		subPath = panelconfig.SubscriptionBasePath(c.cfg)
	}
	webPath := c.cfg.Server.WebPath
	if webPath == "" {
		webPath = "/"
	}
	return []Row{
		{Label: s.ConfigFile, Value: c.configPath},
		{Label: s.Port, Value: strconv.Itoa(c.cfg.Server.Port)},
		{Label: s.Listen, Value: listen},
		{Label: s.PublicURL, Value: pub},
		{Label: s.SubPort, Value: subPort},
		{Label: s.SubListen, Value: subListen},
		{Label: s.SubPath, Value: subPath},
		{Label: s.WebPath, Value: webPath},
		{Label: s.DBPath, Value: c.cfg.Database.Path},
		{Label: s.CoreBin, Value: c.cfg.Mihomo.Binary},
		{Label: "mihomo config", Value: c.cfg.Mihomo.Config},
	}
}

// --- settings (web UI: /settings) -----------------------------------------

func (c *Console) settingsRows() []Row {
	if c.dbErr != nil {
		return c.dbErrorRow()
	}
	s := c.strings
	bot := s.Disable
	if c.tg.Enabled && c.tg.BotToken != "" {
		bot = s.Enable
	}
	host := c.profile.PublicHost
	if host == "" {
		host = s.None
	}
	port := c.profile.PublicPort
	if port == "" {
		port = s.None
	}
	sni := c.profile.SNI
	if sni == "" {
		sni = s.None
	}
	events := strings.Join(c.tg.ChatIDs, ", ")
	if events == "" {
		events = s.None
	}
	botColor := yellow
	if c.tg.Enabled && c.tg.BotToken != "" {
		botColor = green
	}
	return []Row{
		{Label: "— " + s.Telegram + " —", ValueColor: dim},
		{Label: s.TgBot, Value: bot, ValueColor: botColor},
		{Label: s.TgChats, Value: events},
		{Label: s.TgEnabled, Value: c.onOff(c.tg.Enabled)},
		{Label: "— " + s.AccessProf + " —", ValueColor: dim},
		{Label: s.PublicHost, Value: host},
		{Label: s.PublicPort, Value: port},
		{Label: s.SNI, Value: sni},
		{Label: "— " + s.SubPage + " —", ValueColor: dim},
		{Label: s.ResetDay, Value: c.setting("traffic_reset_day")},
		{Label: s.SubTitle, Value: c.setting("sub_title")},
		{Label: s.SubEncrypt, Value: c.setting("sub_encrypt")},
	}
}

// setting reads a key straight from the panel_settings table. Only
// non-sensitive, informational keys are shown here.
func (c *Console) setting(key string) string {
	if c.db == nil {
		return c.strings.None
	}
	var row models.PanelSetting
	if err := c.db.Where("key = ?", key).First(&row).Error; err != nil || row.Value == "" {
		return c.strings.None
	}
	return row.Value
}

// --- certs / system panes --------------------------------------------------

func (c *Console) certRows() []Row {
	s := c.strings
	if len(c.certs) == 0 {
		return []Row{{Label: s.NoCerts, Value: c.certDir}}
	}
	rows := make([]Row, 0, len(c.certs))
	for _, cert := range c.certs {
		days := cert.daysLeft()
		value := cert.Expiry.Format("2006-01-02")
		color := green
		if days < 0 {
			color = red
			value = s.Expired
		} else if days <= 30 {
			color = yellow
		}
		rows = append(rows, Row{
			Label:      cert.Name,
			Value:      value,
			ValueColor: color,
			Sub: func() []Row {
				return []Row{
					{Label: s.Username, Value: cert.Name},
					{Label: s.Issuer, Value: cert.Issuer},
					{Label: s.Expires, Value: cert.Expiry.Format("2006-01-02 15:04")},
					{Label: s.DaysLeft, Value: strconv.Itoa(days)},
				}
			},
		})
	}
	return rows
}

func (c *Console) systemRows() []Row {
	return []Row{
		{Label: c.strings.Version, Value: buildinfo.Version},
		{Label: "git commit", Value: buildinfo.GitCommit},
		{Label: "build time", Value: buildinfo.BuildTime},
		{Label: c.strings.ConfigFile, Value: c.configPath},
		{Label: c.strings.DBPath, Value: c.cfg.Database.Path},
		{Label: c.strings.CoreBin, Value: c.cfg.Mihomo.Binary},
		{Label: "init", Value: c.initName()},
	}
}

// --- user actions ----------------------------------------------------------

func (c *Console) userToggleLabel(u models.ProxyUser) string {
	if u.Enabled {
		return c.strings.ActDisableUser
	}
	return c.strings.ActEnableUser
}

func (c *Console) userToggleConfirm(u models.ProxyUser) string {
	if u.Enabled {
		return c.strings.ConfirmDisableUser
	}
	return ""
}

func (c *Console) userToggle(u models.ProxyUser) (string, error) {
	action := user.BatchEnable
	label := c.strings.ActEnableUser
	if u.Enabled {
		action = user.BatchDisable
		label = c.strings.ActDisableUser
	}
	err := withWritableDB(c.cfg.Database.Path, func(db *gorm.DB) error {
		_, err := user.NewService(db).Batch(action, []uint{u.ID}, user.BatchOpts{})
		return err
	})
	if err != nil {
		return "", err
	}
	if rlErr := c.reloadConfig(); rlErr != nil {
		// The write landed; only the live apply failed. Say so instead of
		// reporting a failure for something that was actually saved.
		return label + " (" + c.strings.OfflineTip + ")", nil
	}
	return label, nil
}

func (c *Console) userResetTraffic(id uint) error {
	err := withWritableDB(c.cfg.Database.Path, func(db *gorm.DB) error {
		_, err := user.NewService(db).ResetTraffic(id)
		return err
	})
	if err != nil {
		return err
	}
	return c.reloadConfig()
}

func (c *Console) userRotateToken(id uint) error {
	err := withWritableDB(c.cfg.Database.Path, func(db *gorm.DB) error {
		_, err := user.NewService(db).RotateSubToken(id)
		return err
	})
	if err != nil {
		return err
	}
	return c.reloadConfig()
}

func (c *Console) userDelete(id uint) error {
	err := withWritableDB(c.cfg.Database.Path, func(db *gorm.DB) error {
		return user.NewService(db).Delete(id)
	})
	if err != nil {
		return err
	}
	return c.reloadConfig()
}

// --- listener actions ------------------------------------------------------

func (c *Console) listenerToggleLabel(l models.Listener) string {
	if l.Enabled {
		return c.strings.ActDisableListener
	}
	return c.strings.ActEnableListener
}

func (c *Console) listenerToggleConfirm(l models.Listener) string {
	if l.Enabled {
		return c.strings.ConfirmListenerOff
	}
	return c.strings.ConfirmListenerOn
}

func (c *Console) listenerToggle(l models.Listener) (string, error) {
	label := c.strings.ActEnableListener
	if l.Enabled {
		label = c.strings.ActDisableListener
	}
	err := withWritableDB(c.cfg.Database.Path, func(db *gorm.DB) error {
		// mihomoApply is nil on purpose: the console must never start or
		// restart the core by itself. Writing the YAML is the panel's job,
		// triggered right after through the reload endpoint.
		return listener.NewService(db, c.cfg.Mihomo.Config, nil).BatchSetEnabled([]uint{l.ID}, !l.Enabled)
	})
	if err != nil {
		return "", err
	}
	if rlErr := c.reloadConfig(); rlErr != nil {
		return label + " (" + c.strings.OfflineTip + ")", nil
	}
	return label, nil
}

// --- state labels ----------------------------------------------------------

func (c *Console) stateLabel() string {
	if c.active {
		return c.strings.Running
	}
	return c.strings.Stopped
}

func (c *Console) stateColor() string {
	if c.active {
		return green
	}
	return red
}

func (c *Console) apiLabel() string {
	if c.apiOK {
		return c.strings.APIReachable
	}
	return c.strings.APIDown
}

func (c *Console) apiColor() string {
	if c.apiOK {
		return green
	}
	return yellow
}

func (c *Console) coreLabel() string {
	if !c.active {
		return c.strings.Stopped
	}
	if !c.coreFetched || !c.coreOK {
		return c.strings.Unknown
	}
	if c.coreSt.Running {
		return c.strings.Running
	}
	return c.strings.Stopped
}

func (c *Console) coreColor() string {
	switch c.coreLabel() {
	case c.strings.Running:
		return green
	case c.strings.Stopped:
		return red
	}
	return yellow
}

func (c *Console) initName() string {
	switch c.init {
	case initSystemd:
		return "systemd"
	case initOpenRC:
		return "OpenRC"
	}
	return c.strings.None
}

// --- log views -------------------------------------------------------------

func (c *Console) showServiceLogs() error {
	out, err := c.serviceLogs(c.height - 10)
	if err != nil {
		return err
	}
	c.showOutput(c.strings.SvcLogs, splitLines(out))
	return nil
}

func (c *Console) showPanelLogs() error {
	lines, err := c.fetchPanelLogs()
	if err != nil {
		return err
	}
	if len(lines) == 0 {
		lines = []string{c.strings.NoLogs}
	}
	c.showOutput(c.strings.PanelLogs, lines)
	return nil
}

func (c *Console) showCoreLogs() error {
	lines, err := c.fetchCoreLogs()
	if err != nil {
		return err
	}
	if len(lines) == 0 {
		lines = []string{c.strings.NoLogs}
	}
	c.showOutput(c.strings.CoreLogs, lines)
	return nil
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// --- data loading ---------------------------------------------------------

// loadData refreshes everything the database-backed panes show. API-backed
// state is fetched separately, on demand, so a stopped panel never makes the
// console wait on timeouts.
func (c *Console) loadData() {
	c.users = nil
	c.listeners = nil
	c.servers = nil
	c.mirrors = nil

	if c.db == nil {
		return
	}
	if rows, err := user.NewService(c.db).GetAll(); err == nil {
		c.users = rows
	}
	if rows, err := listener.NewService(c.db, c.cfg.Mihomo.Config, nil).GetAll(); err == nil {
		c.listeners = rows
	}
	if rows, err := cluster.NewService(c.db).List(); err == nil {
		c.servers = rows
	}
	if rows, err := cluster.NewService(c.db).ListMirroredNodes(0); err == nil {
		c.mirrors = rows
	}
	if v, err := mhconfig.GetVisualConfig(c.db); err == nil {
		c.visual = v
	}
	if v, err := mhconfig.GetServerRouting(c.db); err == nil {
		c.serverRt = v
	}
	if v, err := telegram.LoadSettings(c.db); err == nil {
		c.tg = v
	}
	c.profile = protocol.LoadAccessProfile(c.db)
}
