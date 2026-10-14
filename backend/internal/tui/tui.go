// Package tui is the panel's terminal control center: a multi-pane console
// (header + navigation sidebar + content pane + modal dialogs) drawn with plain
// ANSI sequences. It deliberately avoids a full TUI framework so the panel
// binary stays small - the only added module is golang.org/x/term.
//
// The console covers the same ground as the web UI: dashboard, listeners,
// users, share links, traffic, cluster, routing, core, logs, config and
// settings. Everything stored in the database is read directly (through a
// read-only handle), while state that only exists inside the panel process -
// core PID, live rates, log ring buffers, runtime sockets - is pulled from the
// panel's own loopback API, the same way the web UI does it.
package tui

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/kazeyukiro/3m-ui/backend/internal/buildinfo"
	"github.com/kazeyukiro/3m-ui/backend/internal/config"
	"github.com/kazeyukiro/3m-ui/backend/internal/database/models"
	"github.com/kazeyukiro/3m-ui/backend/internal/mihomo"
	mhconfig "github.com/kazeyukiro/3m-ui/backend/internal/mihomo/config"
	"github.com/kazeyukiro/3m-ui/backend/internal/protocol"
	"github.com/kazeyukiro/3m-ui/backend/internal/telegram"
	"gorm.io/gorm"
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

// view is one level of the content pane. Opening a row that carries a Sub
// pushes another view on top, which is how a user's detail page or a
// subscription list is reached from the list view.
//
// It keeps the row *builder* rather than the rows themselves: an action taken
// from a detail page changes the record it describes, and a snapshot taken
// when the page was opened would keep showing "disable" for a user that is
// already disabled (and would act on the stale copy).
type view struct {
	title string
	build func() []Row
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

	// --- panel database (read-only for browsing) ---
	db      *gorm.DB
	dbErr   error
	apiRoot string
	apiOK   bool
	token   string

	// --- database-backed data ---
	users     []models.ProxyUser
	listeners []models.Listener
	servers   []models.RemoteServer
	mirrors   []models.RemoteNodeMirror
	visual    mhconfig.VisualConfig
	serverRt  mhconfig.ServerRoutingConfig
	tg        telegram.Settings
	profile   protocol.AccessProfile

	// --- panel-process state, fetched on demand ---
	coreSt         coreStatus
	coreOK         bool
	coreFetched    bool
	trafficSt      trafficStatus
	trafficOK      bool
	trafficFetched bool
	runtime        map[uint]mihomo.ListenerRuntime
	runtimeOK      bool
	runtimeFetched bool

	active  bool
	enabled bool
	certs   []certInfo

	cats   []category
	catIdx int
	rowIdx int
	focus  focusKind
	stack  []view

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

// rows returns the rows of the pane currently on screen: the top of the
// navigation stack, or the selected category when the stack is empty.
func (c *Console) rows() []Row {
	if len(c.stack) > 0 {
		top := c.stack[len(c.stack)-1]
		if top.build != nil {
			return top.build()
		}
		return nil
	}
	if c.catIdx < 0 || c.catIdx >= len(c.cats) {
		return nil
	}
	return c.cats[c.catIdx].rows()
}

// push opens a sub-view and puts the cursor on its first row.
func (c *Console) push(v view) {
	c.stack = append(c.stack, v)
	c.rowIdx = 0
	c.focus = focusContent
	c.message = ""
}

// pop closes the current sub-view. It reports false when there was none, which
// is the caller's cue to fall back to the sidebar (or to quit).
func (c *Console) pop() bool {
	if len(c.stack) == 0 {
		return false
	}
	c.stack = c.stack[:len(c.stack)-1]
	c.rowIdx = 0
	c.focus = focusContent
	c.message = ""
	return true
}

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
		apiRoot:    apiBase(cfg, false),
	}
	c.cats = []category{
		{title: str.CatDashboard, rows: c.dashboardRows},
		{title: str.CatUsers, rows: c.usersRows},
		{title: str.CatListeners, rows: c.listenersRows},
		{title: str.CatShare, rows: c.shareRows},
		{title: str.CatTraffic, rows: c.trafficRows},
		{title: str.CatCluster, rows: c.clusterRows},
		{title: str.CatRouting, rows: c.routingRows},
		{title: str.CatCore, rows: c.coreRows},
		{title: str.CatLogs, rows: c.logsRows},
		{title: str.CatConfig, rows: c.configRows},
		{title: str.CatSettings, rows: c.settingsRows},
		{title: str.CatService, rows: c.serviceRows},
		{title: str.CatCerts, rows: c.certRows},
		{title: str.CatSystem, rows: c.systemRows},
	}

	// Browsing never needs to write, so the database stays open read-only for
	// the whole session and cannot collide with the panel's own writes.
	c.db, c.dbErr = openPanelDB(cfg.Database.Path, false)
	if c.db != nil {
		defer func() {
			if sqlDB, e := c.db.DB(); e == nil {
				_ = sqlDB.Close()
			}
		}()
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
		c.goBack()
	case KeyRune:
		switch k.Rune {
		case 'q', 'Q':
			c.goBack()
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

// goBack unwinds one level: an open sub-view first, then the content pane,
// then the whole console.
func (c *Console) goBack() {
	if c.pop() {
		return
	}
	if c.focus == focusContent {
		c.focus = focusNav
		return
	}
	c.quit = true
}

func (c *Console) moveUp() {
	if c.focus == focusNav {
		c.moveCategory(-1)
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
		c.moveCategory(1)
		return
	}
	n := len(c.rows())
	if n == 0 {
		return
	}
	c.rowIdx = (c.rowIdx + 1) % n
}

// moveCategory switches the sidebar selection and closes any open sub-view, so
// the pane always shows the category's own rows.
func (c *Console) moveCategory(delta int) {
	if len(c.cats) == 0 {
		return
	}
	c.catIdx = (c.catIdx + delta + len(c.cats)) % len(c.cats)
	c.rowIdx = 0
	c.stack = nil
	c.message = ""
	c.fetchForCategory()
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

// activate runs the highlighted row: open its sub-view, ask for a choice, or
// execute its action (with a confirmation dialog when the row asks for one).
func (c *Console) activate() {
	rows := c.rows()
	if c.rowIdx >= len(rows) {
		return
	}
	row := rows[c.rowIdx]
	if row.Sub != nil {
		c.push(view{title: row.Label, build: row.Sub})
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
				c.runAndReport(action, func() error { return c.runService(action) })
			},
		}
		return
	}
	if row.Run == nil {
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
				c.execRow(row)
			},
		}
		return
	}
	c.execRow(row)
}

// execRow runs a row's action and reports the outcome in the footer.
func (c *Console) execRow(row Row) {
	c.message = ""
	if row.Run != nil {
		label, err := row.Run()
		if err != nil {
			c.message = c.strings.Failed + ": " + err.Error()
			c.msgClr = red
		} else {
			c.message = c.strings.Done
			if label != "" {
				c.message += ": " + label
			}
			c.msgClr = green
		}
	}
	// The action may have changed what the pane shows (a user's state, a
	// listener's toggle), so re-read before drawing again.
	c.reload()
}

func (c *Console) runAndReport(action string, fn func() error) {
	if err := fn(); err != nil {
		c.message = c.strings.Failed + ": " + err.Error()
		c.msgClr = red
	} else {
		c.message = c.strings.Done + ": " + action
		c.msgClr = green
	}
	c.reload()
}

func (c *Console) showOutput(title string, lines []string) {
	if len(lines) == 0 {
		lines = []string{c.strings.NoLogs}
	}
	c.output = &outputState{title: title, lines: lines}
}

// --- data -----------------------------------------------------------------

// reload refreshes service state, certificates and database-backed panes, and
// drops every cached API response so the next frame re-reads live state.
func (c *Console) reload() {
	c.active = c.serviceActive()
	c.enabled = c.serviceEnabled()
	c.certs = loadCerts(c.certDir)
	c.coreFetched, c.coreOK = false, false
	c.trafficFetched, c.trafficOK = false, false
	c.runtimeFetched, c.runtimeOK = false, false
	c.runtime = nil
	c.loadData()
	c.fetchForCategory()
}

// fetchForCategory pulls the panel-process state the selected category needs.
// Doing it per category (instead of up front) keeps the console responsive when
// the service is down, because a stopped panel is never dialled at all.
func (c *Console) fetchForCategory() {
	if !c.active {
		c.apiOK = false
		return
	}
	switch {
	case c.catIdx == c.catIndex(c.strings.CatCore), c.catIdx == c.catIndex(c.strings.CatDashboard):
		st, err := c.fetchCoreStatus()
		c.coreSt, c.coreFetched, c.coreOK = st, true, err == nil
		c.apiOK = c.apiOK || err == nil
	case c.catIdx == c.catIndex(c.strings.CatTraffic):
		st, err := c.fetchTrafficStatus()
		c.trafficSt, c.trafficFetched, c.trafficOK = st, true, err == nil
		c.apiOK = c.apiOK || err == nil
	case c.catIdx == c.catIndex(c.strings.CatListeners):
		rt, err := c.fetchRuntime()
		c.runtime, c.runtimeFetched, c.runtimeOK = rt, true, err == nil
		c.apiOK = c.apiOK || err == nil
	}
}

// catIndex resolves a category by title so the fetch switch above stays
// readable when categories are reordered.
func (c *Console) catIndex(title string) int {
	for i, cat := range c.cats {
		if cat.title == title {
			return i
		}
	}
	return -1
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
	right := paint(versionTag()+"  ● "+state, stateClr)
	sb.WriteString(rowLR(paint(" "+c.strings.AppTitle, bold), right, w))
	sb.WriteString("\r\n")

	sb.WriteString(paint(c.breadcrumb(), dim))
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

// breadcrumb mirrors the web UI's header: panel / section / detail.
func (c *Console) breadcrumb() string {
	title := ""
	if c.catIdx >= 0 && c.catIdx < len(c.cats) {
		title = c.cats[c.catIdx].title
	}
	out := " 3m-ui / " + title
	for _, v := range c.stack {
		out += " / " + v.title
	}
	return out
}

func (c *Console) renderBody(w, bh int) []string {
	sw := 12
	sideW := sw + 2
	gap := 2
	// Content width is whatever is left after the sidebar and the gap, so a
	// joined row (side + gap + content) spans exactly w columns and lines up
	// with the header/footer rules. The old "- 1" left every body row one
	// column short of the full-width separators.
	cw := w - sideW - gap
	if cw < 8 {
		cw = 8
	}

	// Navigation sidebar ----------------------------------------------------
	// The box only wraps the categories (plus its two borders) instead of
	// stretching down the whole screen with empty rows. With more categories
	// than fit on screen the box scrolls to keep the selection visible.
	sh := len(c.cats) + 2
	if bh < sh {
		sh = bh
	}
	start := 0
	if len(c.cats) > sh-2 {
		start = c.catIdx - (sh-2)/2
		if start < 0 {
			start = 0
		}
		if start > len(c.cats)-(sh-2) {
			start = len(c.cats) - (sh - 2)
		}
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
	for i := 0; i+start < len(c.cats) && i < sh-2; i++ {
		idx := i + start
		if i+1 >= len(side) {
			break
		}
		cat := c.cats[idx]
		label := " " + truncate(cat.title, sw-1)
		cell := padRight(label, sw)
		switch {
		case idx == c.catIdx && c.focus == focusNav:
			cell = paint(cell, rev)
		case idx == c.catIdx:
			cell = paint(cell, cyan)
		}
		side[i+1] = "│" + cell + "│"
	}

	// Content pane ----------------------------------------------------------
	rows := c.rows()
	content := make([]string, 0, bh)
	title := c.cats[c.catIdx].title
	if len(c.stack) > 0 {
		title = c.stack[len(c.stack)-1].title
	}
	content = append(content, padRight(paint(truncate(" "+title, cw), bold+green), cw))
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

// versionTag renders the panel version for the header. Release builds embed
// the git tag, which already carries the "v" prefix (the release workflow
// passes vX.Y.Z), so blind-prefixing again rendered "vv1.3.5". Dev builds and
// bare "1.3.5" values are normalised the other way.
func versionTag() string {
	v := buildinfo.Version
	switch {
	case v == "" || v == "dev":
		return v
	case strings.HasPrefix(v, "v"):
		return v
	default:
		return "v" + v
	}
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
