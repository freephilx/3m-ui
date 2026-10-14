package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kazeyukiro/3m-ui/backend/internal/auth"
	"github.com/kazeyukiro/3m-ui/backend/internal/config"
	"github.com/kazeyukiro/3m-ui/backend/internal/database/models"
	"github.com/kazeyukiro/3m-ui/backend/internal/mihomo"
	mhconfig "github.com/kazeyukiro/3m-ui/backend/internal/mihomo/config"
	"github.com/kazeyukiro/3m-ui/backend/internal/subpage"
	"github.com/kazeyukiro/3m-ui/backend/internal/telegram"
	"github.com/kazeyukiro/3m-ui/backend/internal/user"
	"gorm.io/gorm"
)

// --- panel HTTP API ---------------------------------------------------------
//
// The console reads the panel through the very same endpoints the web UI uses,
// so both interfaces always agree: one state calculation, one definition of
// "blocked", one traffic figure. Anything that only exists inside the panel
// process - core PID, live rates, log ring buffers, listener runtime - has to
// come this way anyway; rebuilding it in a second process is what reports a
// stopped core and empty logs for a perfectly healthy panel.
//
// Authentication reuses the panel's own JWT secret to mint a short-lived admin
// token, so there is no login prompt, no password and no rate limit involved.
// The database is only a fallback for when the panel is stopped, or installed
// somewhere this process cannot see (a container, a different mount namespace).

const apiTimeout = 4 * time.Second

func apiClient() *http.Client { return &http.Client{Timeout: apiTimeout} }

// apiBase builds the loopback root of the panel API, honouring web_path and a
// concrete bind address.
func apiBase(cfg *config.Config, useTLS bool) string {
	scheme := "http"
	if useTLS {
		scheme = "https"
	}
	host := "127.0.0.1"
	if listen := strings.TrimSpace(cfg.Server.Listen); listen != "" &&
		listen != "0.0.0.0" && listen != "::" && listen != "*" {
		// Bound to one address: loopback is not necessarily listening.
		if strings.Contains(listen, ":") && !strings.HasPrefix(listen, "[") {
			host = "[" + listen + "]"
		} else {
			host = listen
		}
	}
	prefix := strings.TrimSuffix(strings.TrimSpace(cfg.Server.WebPath), "/")
	return scheme + "://" + host + ":" + strconv.Itoa(cfg.Server.Port) + prefix
}

// panelToken mints an admin JWT straight from the panel's secret, skipping the
// login endpoint. It has to honour session_version because the auth middleware
// rejects tokens minted for an older session.
func panelToken(cfg *config.Config, db *gorm.DB) (string, error) {
	if db == nil {
		return "", fmt.Errorf("no database handle")
	}
	if strings.TrimSpace(cfg.JWT.Secret) == "" {
		return "", fmt.Errorf("JWT secret is not configured")
	}
	var u models.User
	if err := db.Where("role = ?", "admin").Order("id asc").First(&u).Error; err != nil {
		return "", err
	}
	version := u.SessionVersion
	if version == 0 {
		version = 1
	}
	tok, _, err := auth.GenerateToken(cfg.JWT.Secret, u.ID, u.Username, u.Role, version, 30*time.Minute)
	return tok, err
}

// ensureToken mints the admin token once per session.
//
// Reading the admin row needs the database, which is not always reachable from
// the console. Since session_version defaults to 1 and only moves when the
// admin password is rotated, we can still try to authenticate in that case
// instead of showing an empty console - and if the guess is wrong the panel
// simply answers 401, which the console reports rather than pretending the
// panel has no data.
func (c *Console) ensureToken() (string, error) {
	if c.token != "" {
		return c.token, nil
	}
	if tok, err := panelToken(c.cfg, c.db); err == nil {
		c.token = tok
		return tok, nil
	}
	if strings.TrimSpace(c.cfg.JWT.Secret) == "" {
		return "", fmt.Errorf("JWT secret is not configured")
	}
	tok, _, err := auth.GenerateToken(c.cfg.JWT.Secret, 1, "admin", "admin", 1, 30*time.Minute)
	if err != nil {
		return "", err
	}
	c.token = tok
	c.tokenGuessed = true
	return tok, nil
}

type apiResult struct {
	status int
	body   []byte
	err    error
}

func (c *Console) apiCall(method, path string, body any) apiResult {
	tok, err := c.ensureToken()
	if err != nil {
		return apiResult{err: err}
	}
	var reader io.Reader
	if body != nil {
		raw, mErr := json.Marshal(body)
		if mErr != nil {
			return apiResult{err: mErr}
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.apiRoot+path, reader)
	if err != nil {
		return apiResult{err: err}
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := apiClient().Do(req)
	if err != nil {
		return apiResult{err: err}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return apiResult{status: resp.StatusCode, err: fmt.Errorf("HTTP %d", resp.StatusCode)}
	}
	return apiResult{status: resp.StatusCode, body: raw}
}

func (c *Console) apiGet(path string, out any) error {
	res := c.apiCall(http.MethodGet, path, nil)
	if res.err != nil {
		return res.err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(res.body, out)
}

func (c *Console) apiPost(path string, body any) error {
	return c.apiCall(http.MethodPost, path, body).err
}

func (c *Console) apiDelete(path string) error {
	return c.apiCall(http.MethodDelete, path, nil).err
}

// --- typed fetchers ---------------------------------------------------------
//
// Each fetcher mirrors the payload the web UI parses, so the console cannot
// drift into its own idea of what a user or a listener looks like.

type coreStatus struct {
	Running bool   `json:"running"`
	Version string `json:"version"`
	PID     int    `json:"pid"`
	Uptime  string `json:"uptime"`
}

type trafficStatus struct {
	UploadBytes   int64 `json:"upload_bytes"`
	DownloadBytes int64 `json:"download_bytes"`
	UploadRate    int64 `json:"upload_rate"`
	DownloadRate  int64 `json:"download_rate"`
	Connections   int   `json:"connections"`
}

type panelServerInfo struct {
	Port       int    `json:"port"`
	Listen     string `json:"listen"`
	PublicURL  string `json:"public_url"`
	ConfigPath string `json:"config_path"`
}

type logEntry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Payload   string `json:"payload"`
}

// fetchUsers pulls the same user list the web UI renders. SafeUser carries the
// derived flags the raw table does not have (Blocked, for instance), which is
// exactly the sort of thing a console that reads the database by hand gets
// wrong or leaves out.
func (c *Console) fetchUsers() ([]user.SafeUser, error) {
	var rows []user.SafeUser
	err := c.apiGet("/api/v1/users", &rows)
	return rows, err
}

func (c *Console) fetchListeners() ([]models.Listener, error) {
	var rows []models.Listener
	err := c.apiGet("/api/v1/listeners", &rows)
	return rows, err
}

func (c *Console) fetchRuntime() (map[uint]mihomo.ListenerRuntime, error) {
	var rows []mihomo.ListenerRuntime
	if err := c.apiGet("/api/v1/listeners/runtime-status", &rows); err != nil {
		return nil, err
	}
	out := make(map[uint]mihomo.ListenerRuntime, len(rows))
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, nil
}

func (c *Console) fetchServers() ([]models.RemoteServer, error) {
	var rows []models.RemoteServer
	err := c.apiGet("/api/v1/cluster", &rows)
	return rows, err
}

func (c *Console) fetchMirrors() ([]models.RemoteNodeMirror, error) {
	var rows []models.RemoteNodeMirror
	err := c.apiGet("/api/v1/cluster/mirrored-nodes", &rows)
	return rows, err
}

func (c *Console) fetchCoreStatus() (coreStatus, error) {
	var st coreStatus
	err := c.apiGet("/api/v1/mihomo/status", &st)
	return st, err
}

func (c *Console) fetchTrafficStatus() (trafficStatus, error) {
	var st trafficStatus
	err := c.apiGet("/api/v1/traffic/status", &st)
	return st, err
}

func (c *Console) fetchServerRouting() (mhconfig.ServerRoutingConfig, error) {
	var cfg mhconfig.ServerRoutingConfig
	err := c.apiGet("/api/v1/config/server-routing", &cfg)
	return cfg, err
}

func (c *Console) fetchVisual() (mhconfig.VisualConfig, error) {
	var cfg mhconfig.VisualConfig
	err := c.apiGet("/api/v1/config/visual", &cfg)
	return cfg, err
}

func (c *Console) fetchPanelServer() (panelServerInfo, error) {
	var info panelServerInfo
	err := c.apiGet("/api/v1/system/panel-server", &info)
	return info, err
}

func (c *Console) fetchPanelSettings() (map[string]string, error) {
	settings := map[string]string{}
	err := c.apiGet("/api/v1/panel-settings", &settings)
	return settings, err
}

func (c *Console) fetchTelegram() (telegram.Settings, error) {
	var s telegram.Settings
	err := c.apiGet("/api/v1/telegram/settings", &s)
	return s, err
}

func (c *Console) fetchSubPage() (subpage.Settings, error) {
	var s subpage.Settings
	err := c.apiGet("/api/v1/system/subscription-page", &s)
	return s, err
}

func (c *Console) fetchPanelLogs() ([]string, error) {
	var entries []logEntry
	if err := c.apiGet("/api/v1/system/panel-logs", &entries); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, fmt.Sprintf("%s %s %s", e.Timestamp, e.Level, e.Payload))
	}
	return out, nil
}

func (c *Console) fetchCoreLogs() ([]string, error) {
	var entries []mihomo.LogResponse
	if err := c.apiGet("/api/v1/mihomo/logs", &entries); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, fmt.Sprintf("%s %s %s",
			e.Timestamp.Format("15:04:05"), e.Level, e.Payload))
	}
	return out, nil
}
