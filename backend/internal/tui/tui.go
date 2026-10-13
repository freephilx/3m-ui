// Package tui is the panel's terminal control center: a multi-pane console
// (header + navigation sidebar + content pane + modal dialogs) drawn with plain
// ANSI sequences. It deliberately avoids a full TUI framework so the panel
// binary stays small - the only added module is golang.org/x/term.
package tui

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/kazeyukiro/3m-ui/backend/internal/buildinfo"
	"github.com/kazeyukiro/3m-ui/backend/internal/config"
)

type focusKind int

const (
	focusNav focusKind = iota
	focusContent
)

type category struct {
	title string
	rows  func() []Row
}

type modalState struct {
	title   string
	body    string
	options []string
	idx     int
	onPick  func(int)
}

type outputState struct {
	title string
	lines []string
}

// Console is the whole control center state. Every frame is drawn from it, so
// there is no incremental screen bookkeeping to get out of sync.
type Console struct {
	cfg        *config.Config
	configPath string
	certDir    string
	strings    Strings
	init       initKind

	active  bool
	enabled bool
	certs   []certInfo

	cats   []category
	catIdx int
	rowIdx int
	focus  focusKind

	modal   *modalState
	output  *outputState
	message string
	msgClr  string

	width  int
	height int
	quit   bool
}

var ansiRe = regexp.MustCompile("\033\\[[0-9;]*[a-zA-Z]")

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// plainWidth is the visible width of an already-coloured string.
func plainWidth(s string) int { return dispWidth(stripANSI(s)) }

// Run opens the console. It needs a real terminal and gives up with a clear
// message when there is none, so it never scribbles escapes into a pipe.
func Run(cfg *config.Config, configPath string) error {
	str := pickStrings()
	if !stdinIsTTY() {
		return errors.New(str.NeedTTY)
	}
	if os.Getenv("TERM") == "dumb" {
		return errors.New(str.DumbTerm)
	}
	restore, err := makeRaw()
	if err != nil {
		return err
	}
	defer restore()

	fmt.Print(enterAlt + hideCursor)
	defer fmt.Print(exitAlt + showCursor)

	c := &Console{
		cfg:        cfg,
		configPath: configPath,
		certDir:    filepathJoin(configPath, "certs"),
		strings:    str,
		init:       detectInit(),
		focus:      focusNav,
	}
	c.cats = []category{
		{title: str.CatOverview, rows: c.overviewRows},
		{title: str.CatService, rows: c.serviceRows},
		{title: str.CatCerts, rows: c.certRows},
		{title: str.CatSystem, rows: c.systemRows},
	}
	c.reload()

	kr := NewKeyReader()
	for !c.quit {
		fmt.Print(c.render())
		key, err := kr.Read()
		if err != nil {
			break
		}
		c.handle(key)
	}
	fmt.Print("\r\n")
	fmt.Println(str.ExitMsg)
	return nil
}

// filepathJoin returns <dir of configPath>/name without importing path/filepath
// twice - kept as a tiny helper so the cert dir follows the config location.
func filepathJoin(configPath, name string) string {
	i := strings.LastIndex(configPath, "/")
	if i < 0 {
		return name
	}
	return configPath[:i+1] + name
}

func (c *Console) rows() []Row {
	if c.catIdx < 0 || c.catIdx >= len(c.cats) {
		return nil
	}
	return c.cats[c.catIdx].rows()
}

// --- key handling ----------------------------------------------------------

func (c *Console) handle(k Key) {
	// A modal grabs every key while it is open.
	if c.modal != nil {
		c.handleModal(k)
		return
	}
	if c.output != nil {
		c.output = nil
		return
	}
	switch k.Type {
	case KeyUp:
		c.moveUp()
	case KeyDown:
		c.moveDown()
	case KeyLeft:
		if c.focus == focusContent {
			c.focus = focusNav
		}
	case KeyRight:
		if c.focus == focusNav {
			c.focus = focusContent
		}
	case KeyEnter:
		if c.focus == focusNav {
			c.rowIdx = 0
			if len(c.rows()) > 0 {
				c.focus = focusContent
			}
		} else {
			c.activate()
		}
	case KeyEsc:
		if c.focus == focusContent {
			c.focus = focusNav
		} else {
			c.quit = true
		}
	case KeyRune:
		switch k.Rune {
		case 'q', 'Q':
			if c.focus == focusContent {
				c.focus = focusNav
			} else {
				c.quit = true
			}
		case 'j':
			c.moveDown()
		case 'k':
			c.moveUp()
		case 'h':
			if c.focus == focusContent {
				c.focus = focusNav
			}
		case 'l':
			if c.focus == focusNav {
				c.focus = focusContent
			}
		case 'r', 'R':
			c.reload()
			c.message = ""
		default:
			if k.Rune >= '0' && k.Rune <= '9' {
				c.jump(int(k.Rune - '0'))
			}
		}
	}
}

func (c *Console) moveUp() {
	if c.focus == focusNav {
		c.catIdx = (c.catIdx + len(c.cats) - 1) % len(c.cats)
		c.rowIdx = 0
		return
	}
	n := len(c.rows())
	if n == 0 {
		return
	}
	c.rowIdx = (c.rowIdx + n - 1) % n
}

func (c *Console) moveDown() {
	if c.focus == focusNav {
		c.catIdx = (c.catIdx + 1) % len(c.cats)
		c.rowIdx = 0
		return
	}
	n := len(c.rows())
	if n == 0 {
		return
	}
	c.rowIdx = (c.rowIdx + 1) % n
}

// jump selects a row by its 1-based number, like the classic menu.
func (c *Console) jump(digit int) {
	n := len(c.rows())
	if n == 0 {
		return
	}
	if c.focus == focusNav {
		c.focus = focusContent
	}
	if digit == 0 {
		c.rowIdx = n - 1
		return
	}
	if digit <= n {
		c.rowIdx = digit - 1
	}
}

func (c *Console) handleModal(k Key) {
	m := c.modal
	switch k.Type {
	case KeyLeft, KeyUp:
		m.idx = (m.idx + len(m.options) - 1) % len(m.options)
	case KeyRight, KeyDown:
		m.idx = (m.idx + 1) % len(m.options)
	case KeyEnter:
		pick := m.idx
		c.modal = nil
		m.onPick(pick)
	case KeyEsc:
		c.modal = nil
	case KeyRune:
		switch k.Rune {
		case 'q', 'Q', 0x1b:
			c.modal = nil
		case 'h':
			m.idx = (m.idx + len(m.options) - 1) % len(m.options)
		case 'l', 'j':
			m.idx = (m.idx + 1) % len(m.options)
		}
	}
}

// activate runs the highlighted row, opening a modal first when the row asks
// for confirmation or offers options.
func (c *Console) activate() {
	rows := c.rows()
	if c.rowIdx >= len(rows) {
		return
	}
	row := rows[c.rowIdx]
	if row.Action == "" {
		return
	}
	if len(row.Options) > 0 {
		opts := row.Options
		c.modal = &modalState{
			title:   row.Label,
			options: opts,
			idx:     0,
			onPick: func(i int) {
				action := "disable"
				if i == 0 {
					action = "enable"
				}
				c.runAndReport(action)
			},
		}
		return
	}
	if row.Confirm != "" {
		c.modal = &modalState{
			title:   row.Label,
			body:    row.Confirm,
			options: []string{c.strings.Confirm, c.strings.Cancel},
			idx:     1,
			onPick: func(i int) {
				if i != 0 {
					return
				}
				if row.Action == "logs" {
					c.showLogs()
					return
				}
				c.runAndReport(row.Action)
			},
		}
		return
	}
	if row.Action == "logs" {
		c.showLogs()
		return
	}
	c.runAndReport(row.Action)
}

func (c *Console) runAndReport(action string) {
	if err := c.runService(action); err != nil {
		c.message = c.strings.Failed + ": " + err.Error()
		c.msgClr = red
	} else {
		c.message = c.strings.Done + ": " + action
		c.msgClr = green
	}
	c.reload()
}

func (c *Console) showLogs() {
	out, err := c.serviceLogs(c.height - 10)
	if err != nil {
		c.message = c.strings.Failed + ": " + err.Error()
		c.msgClr = red
		return
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	c.output = &outputState{title: c.strings.LogsTitle, lines: lines}
}

// --- rendering -------------------------------------------------------------

func rowLR(left, right string, w int) string {
	gap := w - plainWidth(left) - plainWidth(right)
	if gap < 1 {
		gap = 1
	}
	return left + repeat(" ", gap) + right
}

func (c *Console) render() string {
	w, h := Size()
	c.width, c.height = w, h

	var sb strings.Builder
	sb.WriteString(clearHome)

	// Header ----------------------------------------------------------------
	sb.WriteString(paint(repeat("━", w), green))
	sb.WriteString("\r\n")

	state := c.strings.Running
	stateClr := green
	if !c.active {
		state = c.strings.Stopped
		stateClr = red
	}
	right := paint("v"+buildinfo.Version+"  ● "+state, stateClr)
	sb.WriteString(rowLR(paint(" "+c.strings.AppTitle, bold), right, w))
	sb.WriteString("\r\n")

	crumb := " 3m-ui / " + c.cats[c.catIdx].title
	sb.WriteString(paint(crumb, dim))
	sb.WriteString("\r\n")

	// Body ------------------------------------------------------------------
	bh := h - 5
	if bh < 4 {
		bh = 4
	}
	body := c.renderBody(w, bh)
	sb.WriteString(strings.Join(body, "\r\n"))
	sb.WriteString("\r\n")

	// Footer ----------------------------------------------------------------
	sb.WriteString(paint(repeat("─", w), green))
	sb.WriteString("\r\n")
	foot := c.strings.Hints
	footClr := dim
	if c.modal != nil {
		foot = c.strings.ModalHint
	} else if c.output != nil {
		foot = c.strings.AnyKey
	} else if c.message != "" {
		foot = c.message
		footClr = c.msgClr
	}
	sb.WriteString(paint(truncate(foot, w), footClr))
	return sb.String()
}

func (c *Console) renderBody(w, bh int) []string {
	sw := 12
	sideW := sw + 2
	gap := 2
	cw := w - sideW - gap - 1
	if cw < 8 {
		cw = 8
	}

	// Navigation sidebar ----------------------------------------------------
	// The box only wraps the categories (plus its two borders) instead of
	// stretching down the whole screen with empty rows.
	sh := len(c.cats) + 2
	if bh < sh {
		sh = bh
	}
	side := make([]string, 0, bh)
	side = append(side, boxTop(sw))
	for i := sh - 2; i > 0; i-- {
		side = append(side, boxLine("", sw))
	}
	side = append(side, boxBottom(sw))
	for len(side) < bh {
		side = append(side, repeat(" ", sw+2))
	}
	for i, cat := range c.cats {
		if i+1 >= len(side) {
			break
		}
		label := " " + truncate(cat.title, sw-1)
		cell := padRight(label, sw)
		switch {
		case i == c.catIdx && c.focus == focusNav:
			cell = paint(cell, rev)
		case i == c.catIdx:
			cell = paint(cell, cyan)
		}
		side[i+1] = "│" + cell + "│"
	}

	// Content pane ----------------------------------------------------------
	rows := c.rows()
	content := make([]string, 0, bh)
	title := c.cats[c.catIdx].title
	content = append(content, paint(truncate(" "+title, cw), bold+green))
	for i, row := range rows {
		if len(content) >= bh {
			break
		}
		left := "   " + truncateVisible(row.Label, cw-16)
		line := padRight(left, cw)
		if row.Value != "" {
			clr := row.ValueColor
			if clr == "" {
				clr = dim
			}
			line = rowLR(left, paint(row.Value, clr), cw)
		}
		if i == c.rowIdx && c.focus == focusContent {
			line = paint(line, rev)
		} else if i == c.rowIdx {
			line = paint(line, cyan)
		}
		content = append(content, line)
	}
	for len(content) < bh {
		content = append(content, "")
	}

	// Join panes ------------------------------------------------------------
	out := make([]string, bh)
	for i := 0; i < bh; i++ {
		s := side[i]
		ct := ""
		if i < len(content) {
			ct = content[i]
		}
		out[i] = truncateVisible(s+repeat(" ", gap)+ct, w)
	}

	if c.modal != nil {
		c.overlay(out, w, bh, c.modalLines(w))
	}
	if c.output != nil {
		c.overlay(out, w, bh, c.outputLines(w, bh))
	}
	return out
}

// overlay writes centred lines over the body. Overlaying whole rows (instead of
// splicing into them) keeps the ANSI bookkeeping trivial.
func (c *Console) overlay(body []string, w, bh int, lines []string) {
	start := (bh - len(lines)) / 2
	if start < 0 {
		start = 0
	}
	for i, l := range lines {
		idx := start + i
		if idx >= bh {
			break
		}
		pad := (w - plainWidth(l)) / 2
		if pad < 0 {
			pad = 0
		}
		body[idx] = repeat(" ", pad) + l
	}
}

func (c *Console) modalLines(w int) []string {
	m := c.modal
	inner := 46
	if w-6 < inner {
		inner = w - 6
	}
	if inner < 12 {
		inner = 12
	}
	lines := []string{boxTop(inner)}
	lines = append(lines, boxLine(" "+truncate(m.title, inner-1), inner))
	lines = append(lines, boxLine(" "+truncate(m.body, inner-1), inner))
	lines = append(lines, boxLine("", inner))

	var sb strings.Builder
	for i, opt := range m.options {
		label := " [ " + opt + " ] "
		if i == m.idx {
			sb.WriteString(paint(label, rev))
		} else {
			sb.WriteString(label)
		}
		sb.WriteString(" ")
	}
	btns := sb.String()
	pad := (inner - plainWidth(btns)) / 2
	if pad < 0 {
		pad = 0
	}
	lines = append(lines, boxLine(repeat(" ", pad)+btns, inner))
	lines = append(lines, boxBottom(inner))
	return lines
}

func (c *Console) outputLines(w, bh int) []string {
	o := c.output
	inner := w - 8
	if inner < 12 {
		inner = 12
	}
	max := bh - 5
	if max < 3 {
		max = 3
	}
	lines := []string{boxTop(inner)}
	lines = append(lines, boxLine(" "+truncate(o.title, inner-1), inner))
	for i := 0; i < max; i++ {
		txt := ""
		if i < len(o.lines) {
			txt = o.lines[i]
		}
		lines = append(lines, boxLine(" "+truncate(txt, inner-1), inner))
	}
	lines = append(lines, boxBottom(inner))
	return lines
}
