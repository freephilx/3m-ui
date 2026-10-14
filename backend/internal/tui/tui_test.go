package tui

import (
	"strings"
	"testing"

	"github.com/kazeyukiro/3m-ui/backend/internal/buildinfo"
)

// The release workflow embeds the git tag (already "v"-prefixed) into
// buildinfo.Version, so the header must not prefix it again - that rendered
// "vv1.3.5" in every release build.
func TestVersionTag(t *testing.T) {
	orig := buildinfo.Version
	defer func() { buildinfo.Version = orig }()
	cases := []struct{ in, want string }{
		{"dev", "dev"},
		{"", ""},
		{"v1.4.1", "v1.4.1"},
		{"1.4.1", "v1.4.1"},
		{"v1.4.0-rc.1", "v1.4.0-rc.1"},
	}
	for _, c := range cases {
		buildinfo.Version = c.in
		if got := versionTag(); got != c.want {
			t.Errorf("versionTag(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPlainWidthIgnoresANSI(t *testing.T) {
	coloured := paint("running", green)
	if w := plainWidth(coloured); w != len("running") {
		t.Errorf("plainWidth(coloured) = %d, want %d", w, len("running"))
	}
	if plainWidth("\033[32m你好\033[0m") != 4 {
		t.Errorf("CJK coloured width mismatch: %d", plainWidth("\033[32m你好\033[0m"))
	}
}

func TestTruncateVisible(t *testing.T) {
	// never counts escape bytes as columns
	got := truncateVisible(paint("running", green), 4)
	if plainWidth(got) > 4 {
		t.Errorf("visible width %d > 4 (%q)", plainWidth(got), got)
	}
	if !strings.Contains(got, "runn") {
		t.Errorf("truncated to %q, want it to keep \"runn\"", got)
	}
	// never splits a double-width rune: 3 columns fit exactly one CJK char
	if got := truncateVisible("控制中心", 3); got != "控" {
		t.Errorf("truncateVisible(CJK,3) = %q, want 控", got)
	}
	if got := truncateVisible("控制中心", 4); got != "控制" {
		t.Errorf("truncateVisible(CJK,4) = %q, want 控制", got)
	}
	// short enough strings pass through untouched
	if got := truncateVisible("ok", 10); got != "ok" {
		t.Errorf("truncateVisible(ok,10) = %q", got)
	}
}

func TestPadRightVisibleWidth(t *testing.T) {
	// padding a coloured string must use the visible width, so the padded
	// result occupies exactly w columns on screen
	got := padRight(paint("运行中", green), 10)
	if plainWidth(got) != 10 {
		t.Errorf("padRight width = %d, want 10", plainWidth(got))
	}
}

func TestBoxLineCJKWidth(t *testing.T) {
	// a box row around double-width content stays exactly inner+2 columns
	line := boxLine(" 概览 ", 12)
	if plainWidth(line) != 14 {
		t.Errorf("boxLine width = %d, want 14", plainWidth(line))
	}
	if plainWidth(boxTop(12)) != 14 || plainWidth(boxBottom(12)) != 14 {
		t.Errorf("box borders are not 14 columns wide")
	}
}
