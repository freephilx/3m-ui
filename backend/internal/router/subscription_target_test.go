package router_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/kazeyukiro/3m-ui/backend/internal/config"
	"github.com/kazeyukiro/3m-ui/backend/internal/database"
	"github.com/kazeyukiro/3m-ui/backend/internal/database/models"
	"github.com/kazeyukiro/3m-ui/backend/internal/mihomo"
	"github.com/kazeyukiro/3m-ui/backend/internal/router"
	"github.com/kazeyukiro/3m-ui/backend/internal/system"
	"github.com/kazeyukiro/3m-ui/backend/internal/traffic"
)

// The external subconverter integration has been removed: every supported
// target is rendered natively by the subscription handler, and an unknown
// target must be rejected with 400 instead of being forwarded to an external
// conversion service (which used to answer on 127.0.0.1:25500).
func TestSubscriptionRejectsUnsupportedTargetAfterSubconverterRemoval(t *testing.T) {
	dir := "/tmp/3m-ui-sub-target-test"
	_ = os.RemoveAll(dir)
	defer os.RemoveAll(dir)

	cfg := &config.Config{}
	cfg.Server.Mode = "debug"
	cfg.Database.Path = dir + "/db.sqlite"
	cfg.Mihomo.Binary = "/tmp/dummy-nonexistent"
	cfg.Mihomo.Config = dir + "/config.yaml"
	cfg.JWT.Secret = "test-secret-for-sub-target"

	db, err := database.InitDB(cfg.Database.Path)
	if err != nil {
		t.Fatalf("init db: %v", err)
	}

	listener := models.Listener{
		Name:        "sub-target-listener",
		Protocol:    "vless",
		BindAddress: "0.0.0.0",
		Port:        "10086",
		Enabled:     true,
		Config:      `{"users":[{"username":"u1","uuid":"11111111-1111-1111-1111-111111111111"}]}`,
	}
	if err := db.Create(&listener).Error; err != nil {
		t.Fatalf("create listener: %v", err)
	}
	token := models.AccessToken{
		Name:       "sub-target token",
		Token:      "subtarget-token-0001",
		Enabled:    true,
		ListenerID: listener.ID,
	}
	if err := db.Create(&token).Error; err != nil {
		t.Fatalf("create access token: %v", err)
	}

	r := router.SetupRouterWithDeps(router.Deps{
		DB:      db,
		Config:  cfg,
		Mihomo:  mihomo.NewService(cfg),
		System:  system.NewService(),
		Traffic: traffic.NewService(),
	})

	// Unknown target → 400 with an explicit error, no external service call.
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/client/sub/"+token.Token+"?target=stash-clashr", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown target: expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "unsupported subscription target") {
		t.Fatalf("unknown target: expected explicit error, got %s", body)
	}

	// Native targets keep rendering locally: clash/meta serve mihomo YAML.
	for _, target := range []string{"", "clash"} {
		w2 := httptest.NewRecorder()
		url := "/api/v1/client/sub/" + token.Token
		if target != "" {
			url += "?target=" + target
		}
		req2, _ := http.NewRequest("GET", url, nil)
		r.ServeHTTP(w2, req2)
		if w2.Code != http.StatusOK {
			t.Fatalf("target %q: expected 200, got %d body=%s", target, w2.Code, w2.Body.String())
		}
		if ct := w2.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/yaml") {
			t.Fatalf("target %q: expected yaml content type, got %q", target, ct)
		}
	}
}
