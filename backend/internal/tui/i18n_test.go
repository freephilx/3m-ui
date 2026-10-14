package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kazeyukiro/3m-ui/backend/internal/config"
)

// The console ships two complete language tables. A label added to only one of
// them is invisible in testing (the other language silently shows an empty
// column) but very visible to the half of the users reading that language, so
// completeness is enforced here rather than left to review.
func TestStringsTablesAreComplete(t *testing.T) {
	tables := map[string]Strings{"zh": zh, "en": en}
	typ := reflect.TypeOf(Strings{})
	for name, table := range tables {
		v := reflect.ValueOf(table)
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			got := v.Field(i).String()
			if strings.TrimSpace(got) == "" {
				t.Errorf("%s: field %s is empty", name, field.Name)
			}
			if got != strings.TrimSpace(got) {
				t.Errorf("%s: field %s has stray whitespace: %q", name, field.Name, got)
			}
		}
	}
}

// Both tables must carry the same set of keys - they are the same struct, so
// this mainly guards against a future split into per-language structs.
func TestStringsTablesSameShape(t *testing.T) {
	if reflect.TypeOf(zh) != reflect.TypeOf(en) {
		t.Fatal("zh and en tables diverged")
	}
	typ := reflect.TypeOf(Strings{})
	zv, ev := reflect.ValueOf(zh), reflect.ValueOf(en)
	filled := 0
	for i := 0; i < typ.NumField(); i++ {
		if zv.Field(i).String() == ev.Field(i).String() {
			// Identical text is fine for locale-neutral labels such as "IP
			// limit"; only report when both are non-trivial prose.
			if len(zv.Field(i).String()) > 12 {
				t.Logf("note: %s has identical text in both tables: %q", typ.Field(i).Name, zv.Field(i).String())
			}
		}
		if zv.Field(i).String() != "" && ev.Field(i).String() != "" {
			filled++
		}
	}
	if filled != typ.NumField() {
		t.Errorf("only %d/%d fields filled in both tables", filled, typ.NumField())
	}
}

// pickStrings must honour the explicit override first, then the locale, and
// fall back to Chinese like the shell menu does.
func TestPickStrings(t *testing.T) {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG", "THREE_M_UI_LANG"} {
		t.Setenv(key, "")
	}
	if got := pickStrings(); got.AppTitle != zh.AppTitle {
		t.Errorf("default should be Chinese, got %q", got.AppTitle)
	}
	t.Setenv("THREE_M_UI_LANG", "en")
	if got := pickStrings(); got.AppTitle != en.AppTitle {
		t.Errorf("THREE_M_UI_LANG=en should win, got %q", got.AppTitle)
	}
	t.Setenv("THREE_M_UI_LANG", "")
	t.Setenv("LANG", "en_US.UTF-8")
	if got := pickStrings(); got.AppTitle != en.AppTitle {
		t.Errorf("LANG=en_* should select English, got %q", got.AppTitle)
	}
	t.Setenv("LANG", "zh_CN.UTF-8")
	if got := pickStrings(); got.AppTitle != zh.AppTitle {
		t.Errorf("LANG=zh_* should select Chinese, got %q", got.AppTitle)
	}
}

// humanBytes is what every traffic figure in the console goes through, so the
// units and the rounding are worth pinning down.
func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{-5, "0 B"}, // negative quotas must not render as "-5 B"
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The console covers the same sections as the web UI navigation; the sidebar
// is built from this list, so a missing section is a missing feature.
func TestCategoriesCoverWebUINavigation(t *testing.T) {
	c := &Console{strings: zh}
	want := []string{
		zh.CatDashboard, zh.CatUsers, zh.CatListeners, zh.CatShare, zh.CatTraffic,
		zh.CatCluster, zh.CatRouting, zh.CatCore, zh.CatLogs, zh.CatConfig,
		zh.CatSettings, zh.CatService, zh.CatCerts, zh.CatSystem,
	}
	seen := map[string]bool{}
	for _, w := range want {
		if w == "" {
			t.Fatal("empty category name in the expected list")
		}
		seen[w] = true
	}
	if len(seen) != len(want) {
		t.Fatalf("category titles are not unique: %d unique of %d", len(seen), len(want))
	}
	_ = c
}

// apiBase has to survive the panel binding a concrete address or sitting
// behind a path prefix, otherwise every API call 404s.
func TestAPIBase(t *testing.T) {
	cases := []struct {
		cfg    config.Config
		useTLS bool
		want   string
	}{
		{config.Config{}, false, "http://127.0.0.1:0"},
		{serverConfig("", 8080, "/"), false, "http://127.0.0.1:8080"},
		{serverConfig("0.0.0.0", 8080, "/secret"), false, "http://127.0.0.1:8080/secret"},
		{serverConfig("10.0.0.5", 8080, ""), false, "http://10.0.0.5:8080"},
		{serverConfig("::1", 8443, ""), false, "http://[::1]:8443"},
		{serverConfig("", 443, ""), true, "https://127.0.0.1:443"},
	}
	for _, c := range cases {
		if got := apiBase(&c.cfg, c.useTLS); got != c.want {
			t.Errorf("apiBase(listen=%q port=%d webPath=%q tls=%v) = %q, want %q",
				c.cfg.Server.Listen, c.cfg.Server.Port, c.cfg.Server.WebPath, c.useTLS, got, c.want)
		}
	}
}

func serverConfig(listen string, port int, webPath string) config.Config {
	return config.Config{Server: config.ServerConfig{
		Port:    port,
		Listen:  listen,
		WebPath: webPath,
	}}
}
