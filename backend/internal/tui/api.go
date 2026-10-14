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
	"gorm.io/gorm"
)

// --- panel HTTP API ---------------------------------------------------------
//
// Anything that lives in the panel process' memory - core PID, live rates,
// log ring buffers, runtime socket state - cannot be read from a second
// process. The console therefore talks to the panel's own API over loopback,
// exactly like the web UI does, instead of rebuilding those subsystems (which
// would report a stopped core and empty logs for a perfectly healthy panel).
//
// Authentication reuses the panel's own JWT secret to mint a short-lived admin
// token, so no login prompt, password or rate limit is involved.

const apiTimeout = 4 * time.Second

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

// apiClient returns a loopback-only client. The panel may serve a self-signed
// certificate, and the traffic is never left the machine, so verification is
// relaxed on purpose rather than forcing operators to install their own cert.
func apiClient() *http.Client {
	return &http.Client{Timeout: apiTimeout}
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

func (c *Console) apiPost(path string) error {
	return c.apiCall(http.MethodPost, path, map[string]any{}).err
}

// ensureToken mints the admin token once per session.
func (c *Console) ensureToken() (string, error) {
	if c.token != "" {
		return c.token, nil
	}
	tok, err := panelToken(c.cfg, c.db)
	if err != nil {
		return "", err
	}
	c.token = tok
	return tok, nil
}

// --- typed helpers ----------------------------------------------------------

// coreStatus mirrors mihomo.StatusResponse from GET /api/v1/mihomo/status.
type coreStatus struct {
	Running bool   `json:"running"`
	Version string `json:"version"`
	PID     int    `json:"pid"`
	Uptime  string `json:"uptime"`
}

// trafficStatus mirrors traffic.Snapshot from GET /api/v1/traffic/status.
type trafficStatus struct {
	UploadBytes   int64 `json:"upload_bytes"`
	DownloadBytes int64 `json:"download_bytes"`
	UploadRate    int64 `json:"upload_rate"`
	DownloadRate  int64 `json:"download_rate"`
	Connections   int   `json:"connections"`
}

type logEntry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Payload   string `json:"payload"`
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

// fetchPanelLogs returns the panel's in-memory log ring.
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

// fetchCoreLogs returns the core's log ring via the panel API.
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

// fetchRuntime returns per-listener runtime state. It needs the panel process:
// the state is derived by scanning /proc for the core's PID.
func (c *Console) fetchRuntime() (map[uint]mihomo.ListenerRuntime, error) {
	var rows []mihomo.ListenerRuntime
	if err := c.apiGet("/api/v1/nodes/runtime-status", &rows); err != nil {
		return nil, err
	}
	out := make(map[uint]mihomo.ListenerRuntime, len(rows))
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, nil
}
