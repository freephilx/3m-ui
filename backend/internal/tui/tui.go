// Package tui is the panel's terminal control center: a full-screen framed
// console (title in the top border, numbered list, red-bar selection, button
// row at the bottom) drawn with plain ANSI sequences. It deliberately avoids a
// full TUI framework so the panel binary stays small - the only added module
// is golang.org/x/term.
//
// The console covers the same ground as the web UI: dashboard, listeners,
// users, share links, traffic, cluster, routing, core, logs, config and
// settings. Everything stored in the database is read through the panel's own
// HTTP API - the very endpoints the web UI calls - so both interfaces always
// agree; the database is only a fallback for when the panel is stopped.
//
// Input works three ways, all hitting the same handlers: the keyboard (arrows,
// hjkl, digits, Enter, Esc), the mouse wheel, and clicks or taps - mode 1000
// plus the SGR extension (1006), which is how touchscreens report too.
package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kazeyukiro/3m-ui/backend/internal/buildinfo"
	"github.com/kazeyukiro/3m-ui/backend/internal/cluster"
	"github.com/kazeyukiro/3m-ui/backend/internal/config"
	"github.com/kazeyukiro/3m-ui/backend/internal/database/models"
	"github.com/kazeyukiro/3m-ui/backend/internal/listener"
	"github.com/kazeyukiro/3m-ui/backend/internal/mihomo"
	mhconfig "github.com/kazeyukiro/3m-ui/backend/internal/mihomo/config"
	"github.com/kazeyukiro/3m-ui/backend/internal/protocol"
	"github.com/kazeyukiro/3m-ui/backend/internal/subpage"
	"github.com/kazeyukiro/3m-ui/backend/internal/telegram"
	"github.com/kazeyukiro/3m-ui/backend/internal/user"
	"gorm.io/gorm"
)

// view is one level of the content stack. Opening a row that carries a Sub
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

// btnZone is one clickable region of the button row, recorded while the frame
// is drawn and consumed by the next mouse event. Screen coordinates are
// 1-based, as the mouse protocol reports them.
type btnZone struct {
	x0, x1 int
	y      int
	act    string
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
	apiErr  error
	token   string
	// tokenGuessed records that the admin token was minted without reading
	// the admin row (no database handle): the panel may still answer 401.
	tokenGuessed bool

	// --- database-backed data ---
	// users carries user.SafeUser - the very payload the web UI renders - so
	// derived flags (Blocked, effective-online) are identical on both paths,
	// whether the rows came from the API or from the database fallback.
	users     []user.SafeUser
	listeners []models.Listener
	servers   []models.RemoteServer
	mirrors   []models.RemoteNodeMirror
	visual    mhconfig.VisualConfig
	serverRt  mhconfig.ServerRoutingConfig
	tg        telegram.Settings
	profile   protocol.AccessProfile
	settings  map[string]string
	subPage   subpage.Settings
	panelInfo panelServerInfo

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
	scroll int
	stack  []view

	modal     *modalState
	output    *outputState
	outScroll int

	message string
	msgClr  string

	// Mouse hit-testing, refreshed on every frame: the button row zones, and
	// the line budget the row list may fill (clampScroll counts lines because
	// a wrapped value makes a row taller than one line).
	btnZones []btnZone
	rowVis   int
	// lineRow maps every rendered interior line to the row it belongs to
	// (-1 for padding). A row whose value is too long to share its line
	// renders as several lines, so a plain arithmetic y->row mapping no
	// longer works.
	lineRow []int
	outVis  int

	width  int
	height int
	quit   bool
}

var ansiRe = regexp.MustCompile("\033\\[[0-9;]*[a-zA-Z]")

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// plainWidth is the visible width of an already-coloured string.
func plainWidth(s string) int { return dispWidth(stripANSI(s)) }

// buildCats assembles the main-menu categories: one per web-UI section, in the
// same order the sidebar lists them. Every icon must be a single double-width
// rune - the frame aligns by visible columns, so a narrow glyph (or one the
// width table disagrees about) shifts that row's right border. The unit test
// TestCategoryIconsAreWide locks this in.
func (c *Console) buildCats(str Strings) []category {
	return []category{
		{title: str.CatDashboard, icon: "📊", rows: c.dashboardRows, summary: c.summaryDashboard},
		{title: str.CatUsers, icon: "👤", rows: c.usersRows, summary: func() (string, string) {
			return strconv.Itoa(len(c.users)), ""
		}},
		{title: str.CatListeners, icon: "📡", rows: c.listenersRows, summary: func() (string, string) {
			on := 0
			for _, l := range c.listeners {
				if l.Enabled {
					on++
				}
			}
			return fmt.Sprintf("%d/%d", on, len(c.listeners)), ""
		}},
		{title: str.CatShare, icon: "🔗", rows: c.shareRows},
		{title: str.CatTraffic, icon: "📈", rows: c.trafficRows, summary: func() (string, string) {
			var used int64
			for _, u := range c.users {
				used += u.TrafficUsed
			}
			return humanBytes(used), ""
		}},
		{title: str.CatCluster, icon: "🌐", rows: c.clusterRows, summary: func() (string, string) {
			return strconv.Itoa(len(c.servers)), ""
		}},
		{title: str.CatRouting, icon: "🧭", rows: c.routingRows},
		{title: str.CatCore, icon: "🧠", rows: c.coreRows, summary: c.coreSummary},
		{title: str.CatLogs, icon: "📜", rows: c.logsRows},
		{title: str.CatConfig, icon: "📋", rows: c.configRows},
		{title: str.CatSettings, icon: "🔧", rows: c.settingsRows},
		{title: str.CatService, icon: "🔌", rows: c.serviceRows, summary: c.serviceSummary},
		{title: str.CatCerts, icon: "🔐", rows: c.certRows, summary: func() (string, string) {
			return strconv.Itoa(len(c.certs)), ""
		}},
		{title: str.CatSystem, icon: "💻", rows: c.systemRows},
	}
}

// rows returns the rows of the screen currently on display: the main menu
// (categories + exit) when the stack is empty, otherwise the top of the
// navigation stack.
func (c *Console) rows() []Row {
	if len(c.stack) > 0 {
		top := c.stack[len(c.stack)-1]
		if top.build != nil {
			return top.build()
		}
		return nil
	}
	return c.menuRows()
}

// menuRows builds the main menu: every category with its icon and a small
// right-aligned summary, plus the exit row - the layout the reference shell
// menu uses.
func (c *Console) menuRows() []Row {
	rows := make([]Row, 0, len(c.cats)+1)
	for i := range c.cats {
		cat := c.cats[i]
		row := Row{Label: cat.title, Icon: cat.icon}
		if cat.summary != nil {
			val, clr := cat.summary()
			row.Value, row.ValueColor = val, clr
		}
		rows = append(rows, row)
	}
	rows = append(rows, Row{Label: c.strings.ExitRow, Icon: "🔵"})
	return rows
}

// push opens a sub-view and puts the cursor on its first row.
func (c *Console) push(v view) {
	c.stack = append(c.stack, v)
	c.rowIdx = 0
	c.scroll = 0
	c.message = ""
}

// pop closes the current sub-view. It reports false when there was none, which
// is the caller's cue to quit.
func (c *Console) pop() bool {
	if len(c.stack) == 0 {
		return false
	}
	c.stack = c.stack[:len(c.stack)-1]
	c.rowIdx = 0
	c.scroll = 0
	c.message = ""
	return true
}

// Run opens the console. It needs a real terminal and gives up with a clear
// message when there is none, so it never scribbles escapes into a pipe.
func Run(cfg *config.Config, configPath string) error {
	str := pickStrings(filepath.Dir(configPath))
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

	fmt.Print(enterAlt + hideCursor + enableMouse)
	defer fmt.Print(disableMouse + showCursor + exitAlt)

	c := &Console{
		cfg:        cfg,
		configPath: configPath,
		certDir:    filepathJoin(configPath, "certs"),
		strings:    str,
		init:       detectInit(),
		apiRoot:    apiBase(cfg, false),
	}
	c.cats = c.buildCats(str)

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
		c.handleOutput(k)
		return
	}
	switch k.Type {
	case KeyMouse:
		c.handleMouse(k.Mouse)
	case KeyUp:
		c.moveUp()
	case KeyDown:
		c.moveDown()
	case KeyPgUp:
		c.movePage(-1)
	case KeyPgDn:
		c.movePage(1)
	case KeyLeft:
		c.goBack()
	case KeyRight, KeyEnter:
		c.activate()
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
			c.goBack()
		case 'l':
			c.activate()
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

// goBack unwinds one level: an open sub-view first, then the category screen,
// then the whole console.
func (c *Console) goBack() {
	if c.pop() {
		return
	}
	c.quit = true
}

func (c *Console) moveUp() {
	n := len(c.rows())
	if n == 0 {
		return
	}
	c.rowIdx = (c.rowIdx + n - 1) % n
	c.clampScroll(c.rowVis)
}

func (c *Console) moveDown() {
	n := len(c.rows())
	if n == 0 {
		return
	}
	c.rowIdx = (c.rowIdx + 1) % n
	c.clampScroll(c.rowVis)
}

// movePage jumps a whole screen of rows, which is what PgUp/PgDn (and a fat
// wheel flick) should do in a long list. The step counts the rows the last
// frame actually showed, so a screen of wrapped rows still moves a screen.
func (c *Console) movePage(delta int) {
	n := len(c.rows())
	if n == 0 {
		return
	}
	step, used := 0, 0
	budget := c.rowVis
	if budget < 1 {
		budget = 1
	}
	for i := c.scroll; i < n; i++ {
		h := c.rowHeight(i)
		if used+h > budget {
			break
		}
		used += h
		step++
	}
	if step < 1 {
		step = 1
	}
	c.rowIdx += delta * step
	if c.rowIdx < 0 {
		c.rowIdx = 0
	}
	if c.rowIdx >= n {
		c.rowIdx = n - 1
	}
	c.clampScroll(c.rowVis)
}

// jump selects a row by its 1-based number, like the classic menu; 0 means the
// last row.
func (c *Console) jump(digit int) {
	n := len(c.rows())
	if n == 0 {
		return
	}
	if digit == 0 {
		c.rowIdx = n - 1
	} else if digit <= n {
		c.rowIdx = digit - 1
	}
	c.clampScroll(c.rowVis)
}

// clampScroll keeps the cursor fully inside the visible window and the window
// inside the list. The budget is counted in screen lines, not rows: a row
// whose value wraps underneath it occupies several lines, the window must end
// on a row boundary, and the tail is pulled up so the end of the list rests
// at the bottom of the window instead of floating in blank space.
func (c *Console) clampScroll(budget int) {
	n := len(c.rows())
	if budget < 1 {
		budget = 1
	}
	if n == 0 {
		c.rowIdx, c.scroll = 0, 0
		return
	}
	if c.rowIdx >= n {
		c.rowIdx = n - 1
	}
	if c.rowIdx < 0 {
		c.rowIdx = 0
	}
	if c.scroll > n-1 {
		c.scroll = n - 1
	}
	// pref[i] is the height in lines of rows [0, i).
	pref := make([]int, n+1)
	for i := 0; i < n; i++ {
		pref[i+1] = pref[i] + c.rowHeight(i)
	}
	if pref[n] <= budget {
		c.scroll = 0 // everything fits
		return
	}
	if c.rowIdx < c.scroll {
		c.scroll = c.rowIdx
	}
	// Advance until the cursor's row ends within the budget. A row taller
	// than the whole window can only ever show its first lines, so the walk
	// stops at the cursor instead of scrolling past it.
	for c.scroll < c.rowIdx && pref[c.rowIdx+1]-pref[c.scroll] > budget {
		c.scroll++
	}
	// Pull rows back in from above while the tail still fits.
	for c.scroll > 0 && pref[n]-pref[c.scroll-1] <= budget {
		c.scroll--
	}
}

func (c *Console) handleModal(k Key) {
	m := c.modal
	switch k.Type {
	case KeyMouse:
		c.handleMouse(k.Mouse)
	case KeyUp, KeyLeft:
		m.idx = (m.idx + len(m.options) - 1) % len(m.options)
	case KeyDown, KeyRight:
		m.idx = (m.idx + 1) % len(m.options)
	case KeyEnter:
		c.pickModal(m.idx)
	case KeyEsc:
		c.modal = nil
	case KeyRune:
		switch k.Rune {
		case 'q', 'Q':
			c.modal = nil
		case 'h', 'k':
			m.idx = (m.idx + len(m.options) - 1) % len(m.options)
		case 'l', 'j':
			m.idx = (m.idx + 1) % len(m.options)
		}
	}
}

// pickModal runs a modal's choice. The callback is captured before the state
// is cleared: it usually reloads data, which redraws the very screen the
// modal was covering.
func (c *Console) pickModal(idx int) {
	m := c.modal
	if m == nil {
		return
	}
	fn := m.onPick
	c.modal = nil
	if fn != nil {
		fn(idx)
	}
}

func (c *Console) handleOutput(k Key) {
	switch k.Type {
	case KeyMouse:
		c.handleMouse(k.Mouse)
	case KeyUp:
		c.scrollOutput(-1)
	case KeyDown:
		c.scrollOutput(1)
	case KeyPgUp:
		c.scrollOutput(-(c.outVis - 1))
	case KeyPgDn:
		c.scrollOutput(c.outVis - 1)
	case KeyEnter, KeyEsc:
		c.output = nil
	case KeyRune:
		switch k.Rune {
		case 'q', 'Q', 'h':
			c.output = nil
		case 'j':
			c.scrollOutput(1)
		case 'k':
			c.scrollOutput(-1)
		}
	}
}

func (c *Console) scrollOutput(delta int) {
	if c.output == nil {
		return
	}
	c.outScroll += delta
	if max := len(c.output.lines) - c.outVis; c.outScroll > max {
		c.outScroll = max
	}
	if c.outScroll < 0 {
		c.outScroll = 0
	}
}

// --- mouse -----------------------------------------------------------------

// handleMouse maps one mouse report onto the frame. The wheel scrolls; a left
// click either hits a button zone or a row. Rows use the two-tap pattern -
// first tap selects (so a mis-tap only moves the highlight), a second tap on
// the same row activates - which is what keeps a touchscreen from firing
// destructive actions by accident.
func (c *Console) handleMouse(m MouseEvent) {
	switch m.Button {
	case 64: // wheel up
		if c.output != nil {
			c.scrollOutput(-3)
			return
		}
		c.moveUp()
		return
	case 65: // wheel down
		if c.output != nil {
			c.scrollOutput(3)
			return
		}
		c.moveDown()
		return
	}
	if m.Button != 0 || !m.Press {
		return // only left-button presses act
	}
	for _, z := range c.btnZones {
		if m.Y == z.y && m.X >= z.x0 && m.X <= z.x1 {
			c.runButton(z.act)
			return
		}
	}
	if c.output != nil {
		return
	}
	// A tap on a wrapped row's continuation lines selects the same row.
	row := -1
	if li := m.Y - 2; li >= 0 && li < len(c.lineRow) {
		row = c.lineRow[li]
	}
	if c.modal != nil {
		if row >= 0 && row < len(c.modal.options) {
			if row == c.modal.idx {
				c.pickModal(row)
			} else {
				c.modal.idx = row
			}
		}
		return
	}
	rows := c.rows()
	if row >= 0 && row < len(rows) {
		if row == c.rowIdx {
			c.activate()
		} else {
			c.rowIdx = row
			c.clampScroll(c.rowVis)
		}
	}
}

// runButton executes a bottom-button action. The zones carry a symbolic name
// so the handler stays independent of the frame's language.
func (c *Console) runButton(act string) {
	switch act {
	case "row":
		if c.modal != nil {
			c.pickModal(c.modal.idx)
			return
		}
		if c.output != nil {
			c.output = nil
			return
		}
		c.activate()
	case "back":
		if c.modal != nil {
			c.modal = nil
			return
		}
		if c.output != nil {
			c.output = nil
			return
		}
		c.goBack()
	case "refresh":
		c.reload()
		c.message = ""
	case "close":
		c.output = nil
	case "quit":
		if c.modal != nil {
			c.modal = nil
			return
		}
		if c.output != nil {
			c.output = nil
			return
		}
		c.quit = true
	}
}

// --- activation ------------------------------------------------------------

// activate runs the highlighted row: enter a category from the main menu, open
// its sub-view, ask for a choice, or execute its action (with a confirmation
// dialog when the row asks for one).
func (c *Console) activate() {
	if len(c.stack) == 0 {
		// Main menu: the rows are the categories plus exit.
		if c.rowIdx >= len(c.cats) {
			c.quit = true
			return
		}
		c.enterCategory(c.rowIdx)
		return
	}
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

// enterCategory opens a category screen: the category's rows become the top of
// the stack, and the panel-process state it needs is fetched right away.
func (c *Console) enterCategory(i int) {
	if i < 0 || i >= len(c.cats) {
		return
	}
	c.catIdx = i
	c.rowIdx = 0
	c.scroll = 0
	c.message = ""
	c.stack = []view{{title: c.cats[i].title, build: c.cats[i].rows}}
	c.fetchForCategory()
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
	c.outScroll = 0
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
	c.apiErr = nil

	// The panel's own API is the source of truth - the web UI reads the same
	// numbers. The database is only consulted when the panel is not answering,
	// so both interfaces can never disagree about a user's state.
	if c.apiMaybeUp() {
		c.loadFromAPI()
	}
	if !c.apiOK {
		c.loadFromDB()
	}
	c.fetchForCategory()
}

// apiMaybeUp avoids dialling a panel that systemd or OpenRC reports as
// stopped. With no init system at all (Docker, a bare process) the state is
// unknown, so we ask the panel itself - a refused loopback connection costs
// nothing, unlike waiting for a timeout on every pane.
func (c *Console) apiMaybeUp() bool {
	switch c.init {
	case initSystemd, initOpenRC:
		return c.active
	default:
		return true
	}
}

// loadFromAPI fills every pane from the panel's HTTP API. Partial success is
// fine: a pane whose endpoint failed simply falls back to the database, and
// the header reports that the API is unreachable.
func (c *Console) loadFromAPI() {
	c.apiOK = false
	fetched := 0
	note := func(err error) {
		if err != nil && c.apiErr == nil {
			c.apiErr = err
		}
	}

	if rows, err := c.fetchUsers(); err == nil {
		c.users, fetched = rows, fetched+1
	} else {
		note(err)
	}
	if !c.apiOK && fetched > 0 {
		c.apiOK = true
	}
	if rows, err := c.fetchListeners(); err == nil {
		c.listeners = rows
	} else {
		note(err)
	}
	if rows, err := c.fetchServers(); err == nil {
		c.servers = rows
	} else {
		note(err)
	}
	if rows, err := c.fetchMirrors(); err == nil {
		c.mirrors = rows
	} else {
		note(err)
	}
	if v, err := c.fetchVisual(); err == nil {
		c.visual = v
	} else {
		note(err)
	}
	if v, err := c.fetchServerRouting(); err == nil {
		c.serverRt = v
	} else {
		note(err)
	}
	if v, err := c.fetchTelegram(); err == nil {
		c.tg = v
	} else {
		note(err)
	}
	if v, err := c.fetchPanelSettings(); err == nil {
		c.settings = v
	} else {
		note(err)
	}
	if v, err := c.fetchSubPage(); err == nil {
		c.subPage = v
	} else {
		note(err)
	}
	if v, err := c.fetchPanelServer(); err == nil {
		c.panelInfo = v
	} else {
		note(err)
	}
	c.apiOK = fetched > 0
}

// loadFromDB is the fallback path: the panel is not answering (stopped, or
// living in another mount namespace), so the panes are filled from the
// read-only database handle instead. The same helpers the API path relies on
// (ToSafeUser, LoadPageSettings) reproduce the web UI's derived values, so
// both paths agree on what a user's state means.
func (c *Console) loadFromDB() {
	c.users = nil
	c.listeners = nil
	c.servers = nil
	c.mirrors = nil
	c.settings = nil

	if c.db == nil {
		return
	}
	if rows, err := user.NewService(c.db).GetAll(); err == nil {
		out := make([]user.SafeUser, 0, len(rows))
		for i := range rows {
			out = append(out, user.ToSafeUser(&rows[i]))
		}
		c.users = out
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
	c.subPage = subpage.LoadPageSettings(c.db)
	var rows []models.PanelSetting
	if err := c.db.Find(&rows).Error; err == nil {
		out := make(map[string]string, len(rows))
		for _, r := range rows {
			// The API endpoint withholds sensitive keys; the console must not
			// print a bot token the web UI refuses to show.
			if sensitiveSettingKey(r.Key) {
				continue
			}
			out[r.Key] = r.Value
		}
		c.settings = out
	}
}

// fetchForCategory pulls the panel-process state the open category needs.
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

func (c *Console) render() string {
	w, h := Size()
	c.width, c.height = w, h
	c.btnZones = nil

	var sb strings.Builder
	sb.WriteString(clearHome)
	sb.WriteString(strings.Join(c.frame(w, h), "\r\n"))
	return sb.String()
}

// frame builds the whole screen: the top border carrying the title, the
// interior, and the bottom border. Every interior line is padded to exactly
// the interior width, so the right border lines up on every terminal.
func (c *Console) frame(w, h int) []string {
	iw := w - 2
	ih := h - 2
	lines := make([]string, 0, h)
	lines = append(lines, frameTop(c.frameTitle(), w))
	for _, l := range c.interior(iw, ih) {
		lines = append(lines, "│"+padRight(truncateVisible(l, iw), iw)+"│")
	}
	lines = append(lines, boxBottom(iw))
	return lines
}

// frameTop renders the upper border with the title centred inside it, the way
// the reference menu does ("Tmoe manager running on Android").
func frameTop(title string, w int) string {
	inner := w - 2
	tw := plainWidth(title)
	if tw > inner-2 {
		title = truncateVisible(title, inner-2)
		tw = plainWidth(title)
	}
	left := (inner - tw) / 2
	if left < 1 {
		left = 1
	}
	right := inner - tw - left
	if right < 1 {
		right = 1
	}
	return "┌" + repeat("─", left) + title + repeat("─", right) + "┐"
}

// frameTitle is the text embedded in the top border: the app name, version and
// service state on the main menu; the breadcrumb everywhere else.
func (c *Console) frameTitle() string {
	s := c.strings
	switch {
	case c.modal != nil:
		return " " + c.breadcrumb() + " / " + truncate(c.modal.title, 20) + " "
	case c.output != nil:
		return " " + c.breadcrumb() + " / " + truncate(c.output.title, 20) + " "
	case len(c.stack) == 0:
		state, clr := s.Stopped, red
		if c.active {
			state, clr = s.Running, green
		}
		return " " + s.AppTitle + " " + versionTag() + "  ● " + paint(state, clr) + " "
	default:
		return " " + c.breadcrumb() + " "
	}
}

// breadcrumb mirrors the web UI's header: panel / section / detail. Only the
// open views contribute - the main menu has none and shows the app title
// instead.
func (c *Console) breadcrumb() string {
	out := "3m-ui"
	for _, v := range c.stack {
		out += " / " + v.title
	}
	return out
}

// interior builds the lines between the borders: the list, the hint line and
// the button row. Modal and output screens replace the list.
func (c *Console) interior(iw, ih int) []string {
	switch {
	case c.modal != nil:
		return c.interiorModal(iw, ih)
	case c.output != nil:
		return c.interiorOutput(iw, ih)
	default:
		return c.interiorList(iw, ih)
	}
}

// interiorList is the normal screen: optional welcome lines, the numbered row
// list (scrolled to keep the cursor visible), a hint line and the buttons.
//
// The list is filled by *line budget*, not by row count: a row whose value
// wraps underneath it occupies several interior lines, and the window must
// still end on a row boundary. c.lineRow records which row every rendered
// line belongs to so the mouse handler can map a tap back to a row.
func (c *Console) interiorList(iw, ih int) []string {
	header := 1
	if len(c.stack) == 0 {
		header = 3 // welcome x2 + blank
	}
	budget := ih - header - 3 // blank + hint + buttons
	if budget < 1 {
		budget = 1
	}

	lines := make([]string, 0, ih)
	c.lineRow = c.lineRow[:0]
	if len(c.stack) == 0 {
		lines = append(lines,
			" "+c.strings.Welcome1,
			" "+paint(c.strings.Welcome2, dim),
			"")
	} else {
		lines = append(lines, "")
	}
	for range lines {
		c.lineRow = append(c.lineRow, -1) // header lines belong to no row
	}

	rows := c.rows()
	c.clampScroll(budget)
	c.rowVis = budget
	for idx := c.scroll; idx < len(rows) && len(c.lineRow)-header < budget; idx++ {
		rl := c.rowLines(idx+1, rows[idx], iw, idx == c.rowIdx)
		if len(c.lineRow)-header+len(rl) > budget {
			if len(c.lineRow) == header && len(rl) > budget {
				rl = rl[:budget] // a row taller than the window shows its top
			} else {
				break // never split a row across the window edge
			}
		}
		for range rl {
			c.lineRow = append(c.lineRow, idx)
		}
		lines = append(lines, rl...)
	}
	for len(lines) < ih-3 {
		c.lineRow = append(c.lineRow, -1)
		lines = append(lines, "")
	}

	labels, acts := c.buttonSet()
	lines = append(lines,
		"",
		" "+c.footerText(),
		c.buttonLine(labels, acts, iw, ih+1))
	for len(c.lineRow) < len(lines) {
		c.lineRow = append(c.lineRow, -1) // the footer belongs to no row
	}
	return lines
}

// rowLines renders one list row. Most rows are a single line, but a value
// too long to share the line with its label (a subscription URL, say) drops
// to its own indented lines underneath - the web UI stacks label above value
// the same way, and a label truncated to "Mihom" helps nobody.
func (c *Console) rowLines(n int, row Row, iw int, selected bool) []string {
	if row.Rule {
		return []string{paint(" "+repeat("─", iw-2), dim)}
	}
	left := " " + fmt.Sprintf("%2d", n) + "  "
	if row.Icon != "" {
		left += row.Icon + " "
	}
	left += row.Label
	if row.Value != "" {
		clr := row.ValueColor
		if clr == "" {
			clr = dim
		}
		// One column of air before the right border keeps the figure from
		// touching it.
		if plainWidth(left)+plainWidth(row.Value)+2 <= iw {
			gap := iw - plainWidth(left) - plainWidth(row.Value) - 1
			return []string{paintRow(left+repeat(" ", gap)+paint(row.Value, clr), iw, selected)}
		}
		out := []string{paintRow(truncateVisible(left, iw-1), iw, selected)}
		for _, wl := range wrapText(row.Value, iw-6) {
			out = append(out, paintRow("     "+paint(wl, clr), iw, selected))
		}
		return out
	}
	return []string{paintRow(truncateVisible(left, iw-1), iw, selected)}
}

// paintRow applies the selection bar. Unselected lines are returned as drawn;
// the frame pads every interior line to the full width anyway.
func paintRow(line string, iw int, selected bool) string {
	if selected {
		return paint(padRight(line, iw), bgRed+white)
	}
	return line
}

// rowHeight is how many screen lines row idx occupies. It must agree with
// rowLines exactly - clampScroll relies on it to keep the cursor visible.
func (c *Console) rowHeight(idx int) int {
	rows := c.rows()
	if idx < 0 || idx >= len(rows) {
		return 1
	}
	iw := c.width - 2
	if iw < 8 {
		iw = 78 // before the first render the size is still unknown
	}
	return len(c.rowLines(idx+1, rows[idx], iw, false))
}

// footerText is the dim hint line; an action's result temporarily replaces it
// so the user sees what happened without leaving the screen.
func (c *Console) footerText() string {
	if c.message != "" {
		return paint(c.message, c.msgClr)
	}
	return paint(c.hintText(), dim)
}

func (c *Console) hintText() string {
	return c.strings.HintNav
}

// buttonSet is the bottom button row for the current screen. The actions are
// symbolic so mouse and keyboard paths share one implementation.
func (c *Console) buttonSet() ([]string, []string) {
	if len(c.stack) == 0 {
		return []string{c.strings.BtnEnter, c.strings.BtnRefresh, c.strings.BtnQuit},
			[]string{"row", "refresh", "quit"}
	}
	return []string{c.strings.BtnEnter, c.strings.BtnBack, c.strings.BtnQuit},
		[]string{"row", "back", "quit"}
}

// buttonLine renders "< label >" buttons centred on the last interior line and
// records their hit zones for the mouse handler.
func (c *Console) buttonLine(labels, acts []string, iw, y int) string {
	btns := make([]string, len(labels))
	widths := make([]int, len(labels))
	total := 0
	for i, lb := range labels {
		btns[i] = paint("< "+lb+" >", bold)
		widths[i] = plainWidth(btns[i])
		total += widths[i]
	}
	if n := len(labels); n > 1 {
		total += 3 * (n - 1)
	}
	pad := (iw - total) / 2
	if pad < 0 {
		pad = 0
	}
	var sb strings.Builder
	sb.WriteString(repeat(" ", pad))
	x := 2 + pad // interior starts at screen column 2 (column 1 is the border)
	for i, b := range btns {
		if i > 0 {
			sb.WriteString("   ")
			x += 3
		}
		sb.WriteString(b)
		c.btnZones = append(c.btnZones, btnZone{x0: x, x1: x + widths[i] - 1, y: y, act: acts[i]})
		x += widths[i]
	}
	return sb.String()
}

// interiorModal is the choice dialog: the question, the options as a numbered
// list (the highlighted one is the default), and Ok/Cancel buttons.
func (c *Console) interiorModal(iw, ih int) []string {
	m := c.modal
	lines := []string{""}
	for _, b := range wrapText(m.body, iw-6) {
		lines = append(lines, "  "+paint(b, yellow))
	}
	lines = append(lines, "")

	c.rowVis = len(m.options)
	c.lineRow = c.lineRow[:0]
	for range lines {
		c.lineRow = append(c.lineRow, -1)
	}
	for i, opt := range m.options {
		for range c.rowLines(i+1, Row{Label: opt}, iw, i == m.idx) {
			c.lineRow = append(c.lineRow, i)
		}
		lines = append(lines, c.rowLines(i+1, Row{Label: opt}, iw, i == m.idx)...)
	}
	for len(lines) < ih-3 {
		c.lineRow = append(c.lineRow, -1)
		lines = append(lines, "")
	}

	lines = append(lines,
		"",
		" "+paint(c.strings.HintModal, dim),
		c.buttonLine(
			[]string{c.strings.BtnOk, c.strings.Cancel},
			[]string{"row", "back"}, iw, ih+1))
	for len(c.lineRow) < len(lines) {
		c.lineRow = append(c.lineRow, -1) // the footer belongs to no row
	}
	return lines
}

// interiorOutput is the full-screen text viewer (logs): scrollable lines and a
// single close button.
func (c *Console) interiorOutput(iw, ih int) []string {
	o := c.output
	vis := ih - 4 // blank + hint + buttons
	if vis < 1 {
		vis = 1
	}
	c.outVis = vis
	if max := len(o.lines) - vis; c.outScroll > max {
		c.outScroll = max
	}
	if c.outScroll < 0 {
		c.outScroll = 0
	}
	lines := []string{""}
	for i := 0; i < vis; i++ {
		idx := c.outScroll + i
		txt := ""
		if idx >= 0 && idx < len(o.lines) {
			txt = o.lines[idx]
		}
		lines = append(lines, " "+truncateVisible(txt, iw-2))
	}
	lines = append(lines, "")
	lines = append(lines, " "+paint(c.strings.HintOutput, dim))
	lines = append(lines, c.buttonLine(
		[]string{c.strings.BtnClose}, []string{"close"}, iw, ih+1))
	return lines
}

// wrapText breaks s into lines of at most width columns, never splitting a
// double-width rune.
func wrapText(s string, width int) []string {
	if width < 8 {
		width = 8
	}
	if dispWidth(s) <= width {
		return []string{s}
	}
	var lines []string
	cur, curW := "", 0
	for _, r := range s {
		rw := runeWidth(r)
		if curW+rw > width {
			lines = append(lines, cur)
			cur, curW = "", 0
		}
		cur += string(r)
		curW += rw
	}
	if cur != "" {
		lines = append(lines, cur)
	}
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
