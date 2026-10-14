package tui

import (
	"fmt"
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

// Every category icon is rendered inside the frame, so a glyph the width table
// counts differently from the terminal shifts that row's border. Lock the set:
// each icon must be exactly one double-width rune (no narrow glyph, no
// variation selector, no multi-rune sequence).
func TestCategoryIconsAreWide(t *testing.T) {
	c := &Console{}
	cats := c.buildCats(Strings{})
	if len(cats) != 14 {
		t.Fatalf("got %d categories, want 14", len(cats))
	}
	for _, cat := range cats {
		runes := []rune(cat.icon)
		if len(runes) != 1 {
			t.Errorf("%s icon %q is %d runes, want exactly 1", cat.title, cat.icon, len(runes))
			continue
		}
		if !isWide(runes[0]) {
			t.Errorf("%s icon %q (U+%04X) is not double-width", cat.title, cat.icon, runes[0])
		}
	}
}

// A value too long to share its line with its label (a subscription URL) must
// wrap underneath it: the label stays readable - "Mihomo / Clash", not the
// "Mihom" a truncation used to leave - and the row grows taller than one line,
// which clampScroll must count.
func TestLongValueWrapsUnderLabel(t *testing.T) {
	c := &Console{width: 100}
	iw := c.width - 2
	row := Row{Label: "Mihomo / Clash", Value: "http://127.0.0.1:8080/api/v1/client/sub/7a1360e2f0a1b2c3d4e5f6a7b8c9d0e1?target=mihomo"}
	lines := c.rowLines(4, row, iw, false)
	if len(lines) < 2 {
		t.Fatalf("long value rendered on %d lines, want >= 2", len(lines))
	}
	if !strings.Contains(stripANSI(lines[0]), "Mihomo / Clash") {
		t.Errorf("label was truncated: %q", stripANSI(lines[0]))
	}
	if !strings.Contains(stripANSI(lines[1]), "?target=mihomo") {
		t.Errorf("value tail missing from the wrapped lines: %q", stripANSI(lines[1]))
	}
	for _, l := range lines {
		if plainWidth(l) > iw {
			t.Errorf("line %q is %d columns wide, interior is %d", stripANSI(l), plainWidth(l), iw)
		}
	}
	// the selection bar covers every line of a wrapped row
	sel := c.rowLines(4, row, iw, true)
	if len(sel) != len(lines) {
		t.Fatalf("selected row has %d lines, unselected %d", len(sel), len(lines))
	}
	for _, l := range sel {
		if !strings.Contains(l, bgRed) {
			t.Errorf("selected line %q lacks the red bar", stripANSI(l))
		}
	}
}

// lineRow is how a tap finds its row, so it must cover *every* interior line
// the frame draws - header lines included. An earlier version started the
// mapping at the row list, which shifted every tap by the header height.
func TestLineRowCoversEveryInteriorLine(t *testing.T) {
	c := &Console{width: 100, height: 24}
	iw, ih := c.width-2, c.height-2
	rows := []Row{
		{Label: "alpha"},
		{Label: "beta", Value: "a-value-long-enough-to-wrap-underneath-its-label-on-a-narrow-screen-x"},
		{Label: "gamma"},
	}
	c.stack = []view{{title: "t", build: func() []Row { return rows }}}
	lines := c.interiorList(iw, ih)
	if len(c.lineRow) != len(lines) {
		t.Fatalf("lineRow has %d entries for %d interior lines", len(c.lineRow), len(lines))
	}
	for li, r := range c.lineRow {
		if r < -1 || r >= len(rows) {
			t.Fatalf("interior line %d maps to row %d, out of range", li, r)
		}
		if r < 0 {
			continue // header or padding
		}
		plain := stripANSI(lines[li])
		if !strings.Contains(plain, rows[r].Label) && !strings.Contains(plain, "a-value-long") {
			t.Errorf("interior line %d (%q) maps to row %d (%q)", li, plain, r, rows[r].Label)
		}
	}
	// the main menu's welcome block is header, not rows
	c2 := &Console{width: 100, height: 24}
	c2.cats = c2.buildCats(Strings{})
	lines2 := c2.interiorList(iw, ih)
	if len(c2.lineRow) != len(lines2) {
		t.Fatalf("main menu: lineRow has %d entries for %d interior lines", len(c2.lineRow), len(lines2))
	}
	for li := 0; li < 3; li++ {
		if c2.lineRow[li] != -1 {
			t.Errorf("main menu interior line %d maps to row %d, want -1 (welcome header)", li, c2.lineRow[li])
		}
	}
	if c2.lineRow[3] != 0 {
		t.Errorf("main menu interior line 3 maps to row %d, want 0 (first category)", c2.lineRow[3])
	}
}

// A modal's option list is drawn inside the frame too, so its lines must map
// back to option indices for a tap to pick one.
func TestModalLineRowMapsOptions(t *testing.T) {
	c := &Console{width: 100, height: 24}
	c.modal = &modalState{title: "t", body: "proceed?", options: []string{"yes", "no"}, idx: 1}
	lines := c.interiorModal(c.width-2, c.height-2)
	if len(c.lineRow) != len(lines) {
		t.Fatalf("modal lineRow has %d entries for %d interior lines", len(c.lineRow), len(lines))
	}
	seen := map[int]bool{}
	for _, r := range c.lineRow {
		if r >= 0 {
			seen[r] = true
		}
	}
	if !seen[0] || !seen[1] || len(seen) != 2 {
		t.Errorf("modal options mapped as %v, want {0,1}", seen)
	}
}

// clampScroll works in screen lines, not rows: the cursor's row must fit
// entirely inside the budget, the window ends on a row boundary, and the tail
// is pulled up so a short list's end rests at the bottom of the window.
func TestClampScrollCountsLines(t *testing.T) {
	c := &Console{width: 60} // narrow: every third row wraps to three lines
	c.stack = []view{{title: "t", build: func() []Row {
		rows := make([]Row, 0, 20)
		for i := 0; i < 20; i++ {
			r := Row{Label: fmt.Sprintf("row %d", i)}
			if i%3 == 0 {
				r.Value = "a-fairly-long-value-that-wraps-underneath-its-label"
			}
			rows = append(rows, r)
		}
		return rows
	}}}
	window := func() int {
		h := 0
		for i := c.scroll; i < len(c.rows()); i++ {
			h += c.rowHeight(i)
		}
		return h
	}

	budget := 8
	c.rowIdx, c.scroll = 0, 0
	c.clampScroll(budget)
	if c.scroll != 0 {
		t.Errorf("cursor at the top: scroll = %d, want 0", c.scroll)
	}
	c.rowIdx = 19
	c.clampScroll(budget)
	if window() > budget {
		t.Errorf("window is %d lines, budget %d (scroll=%d)", window(), budget, c.scroll)
	}
	end := 0
	for i := c.scroll; i < 19; i++ {
		end += c.rowHeight(i)
	}
	if end+c.rowHeight(19) > budget {
		t.Errorf("cursor row ends %d lines into a %d-line window", end+c.rowHeight(19), budget)
	}
	// the tail is pulled up: the window ends at the last row, not short of it
	if c.scroll > 0 && window() < budget && c.rowHeight(c.scroll-1) > budget-window() {
		t.Errorf("tail not filled: window %d of %d lines at scroll %d", window(), budget, c.scroll)
	}
	// a list that fits entirely shows everything from the top
	c.rowIdx = 5
	c.clampScroll(100)
	if c.scroll != 0 {
		t.Errorf("short list: scroll = %d, want 0", c.scroll)
	}
}
