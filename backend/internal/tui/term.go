package tui

import (
	"bufio"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// The console draws with plain ANSI escape sequences instead of pulling in a
// full TUI framework. That keeps the panel binary small - the only new module
// is golang.org/x/term, which rides on golang.org/x/sys already in go.mod.
const (
	reset       = "\033[0m"
	bold        = "\033[1m"
	dim         = "\033[2m"
	rev         = "\033[7m"
	green       = "\033[32m"
	brightGreen = "\033[92m"
	red         = "\033[31m"
	yellow      = "\033[33m"
	cyan        = "\033[36m"
	blue        = "\033[34m"
	white       = "\033[37m"
	bgRed       = "\033[41m"

	clearHome  = "\033[H\033[2J"
	enterAlt   = "\033[?1049h"
	exitAlt    = "\033[?1049l"
	hideCursor = "\033[25l"
	showCursor = "\033[25h"

	// Mouse reporting: 1000 = button press/release tracking, 1006 = SGR
	// extended coordinates - the only encoding that survives past column 223
	// and the one every modern terminal agrees on. Both are announced on
	// entry and withdrawn on exit, so the shell after `3m-ui tui` is clean.
	// Touchscreens arrive here as plain left-button clicks.
	enableMouse  = "\033[?1000h\033[?1006h"
	disableMouse = "\033[?1000l\033[?1006l"
)

type KeyType int

const (
	KeyRune KeyType = iota
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyPgUp
	KeyPgDn
	KeyEnter
	KeyEsc
	KeyMouse
)

// MouseEvent is one decoded mouse report. X/Y are 1-based terminal columns and
// rows; Button follows the xterm numbering (0 left, 1 middle, 2 right, 64
// wheel up, 65 wheel down); Press distinguishes a press from a release, which
// is what makes a click (press+release on the same spot) detectable.
type MouseEvent struct {
	X, Y   int
	Button int
	Press  bool
}

type Key struct {
	Type  KeyType
	Rune  rune
	Mouse MouseEvent
}

// KeyReader turns raw bytes into keys. Reading one byte at a time is enough:
// the console never accepts typed text, only single keys.
type KeyReader struct {
	r *bufio.Reader
}

func NewKeyReader() *KeyReader {
	return &KeyReader{r: bufio.NewReaderSize(os.Stdin, 256)}
}

func (kr *KeyReader) Read() (Key, error) {
	b, err := kr.r.ReadByte()
	if err != nil {
		return Key{}, err
	}
	switch b {
	case 0x0d, 0x0a:
		return Key{Type: KeyEnter}, nil
	case 0x1b:
		// An arrow key arrives as one burst: ESC [ A. When nothing else is
		// buffered the user really did press Escape.
		if kr.r.Buffered() >= 2 {
			b2, _ := kr.r.ReadByte()
			b3, _ := kr.r.ReadByte()
			if b2 == '[' {
				switch b3 {
				case 'A':
					return Key{Type: KeyUp}, nil
				case 'B':
					return Key{Type: KeyDown}, nil
				case 'C':
					return Key{Type: KeyRight}, nil
				case 'D':
					return Key{Type: KeyLeft}, nil
				case '5':
					// PgUp arrives as ESC [ 5 ~
					_, _ = kr.r.ReadByte()
					return Key{Type: KeyPgUp}, nil
				case '6':
					// PgDn arrives as ESC [ 6 ~
					_, _ = kr.r.ReadByte()
					return Key{Type: KeyPgDn}, nil
				case '<':
					// SGR mouse: ESC [ < b ; x ; y (M|m)
					return kr.readSGRMouse()
				case 'M':
					// X10 mouse: ESC [ M + 3 raw bytes (coords + 32).
					return kr.readX10Mouse()
				}
			}
		}
		return Key{Type: KeyEsc}, nil
	}
	return Key{Type: KeyRune, Rune: rune(b)}, nil
}

// readSGRMouse consumes "b;x;yM" (press) or "b;x;ym" (release) after the
// "[<" prefix has been read. Modifier bits (shift/ctrl/meta) are masked off:
// the console only ever cares about which button moved.
func (kr *KeyReader) readSGRMouse() (Key, error) {
	var buf []byte
	for {
		c, err := kr.r.ReadByte()
		if err != nil {
			return Key{}, err
		}
		if c == 'M' || c == 'm' {
			parts := strings.SplitN(string(buf), ";", 3)
			if len(parts) != 3 {
				return Key{Type: KeyEsc}, nil
			}
			b, err1 := strconv.Atoi(parts[0])
			x, err2 := strconv.Atoi(parts[1])
			y, err3 := strconv.Atoi(parts[2])
			if err1 != nil || err2 != nil || err3 != nil {
				return Key{Type: KeyEsc}, nil
			}
			// Bits 0-1 select the button, bit 6/7 mark wheel events; keep
			// the raw value so wheel scrolling is recognisable too.
			return Key{Type: KeyMouse, Mouse: MouseEvent{
				X: x, Y: y, Button: b, Press: c == 'M',
			}}, nil
		}
		buf = append(buf, c)
	}
}

// readX10Mouse decodes the legacy encoding: three bytes, each offset by 32.
// Terminals that ignore mode 1006 fall back to this, so the console still
// receives clicks instead of garbage keys.
func (kr *KeyReader) readX10Mouse() (Key, error) {
	raw := make([]byte, 3)
	for i := range raw {
		c, err := kr.r.ReadByte()
		if err != nil {
			return Key{}, err
		}
		raw[i] = c
	}
	b := int(raw[0]) - 32
	return Key{Type: KeyMouse, Mouse: MouseEvent{
		X:      int(raw[1]) - 32,
		Y:      int(raw[2]) - 32,
		Button: b & 3,
		Press:  true, // X10 only ever reports presses
	}}, nil
}

// isWide reports whether a rune takes two terminal columns (CJK, fullwidth
// forms, emoji). Getting this right is what keeps the frame borders aligned.
func isWide(r rune) bool {
	return (r >= 0x1100 && r <= 0x115F) ||
		(r >= 0x2E80 && r <= 0x303E) ||
		(r >= 0x3041 && r <= 0x33FF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0xA000 && r <= 0xA4CF) ||
		(r >= 0xAC00 && r <= 0xD7A3) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFE10 && r <= 0xFE19) ||
		(r >= 0xFE30 && r <= 0xFE6F) ||
		(r >= 0xFF00 && r <= 0xFF60) ||
		(r >= 0xFFE0 && r <= 0xFFE6) ||
		(r >= 0x1F300 && r <= 0x1F64F) ||
		(r >= 0x1F900 && r <= 0x1F9FF) ||
		(r >= 0x20000 && r <= 0x3FFFD)
}

func runeWidth(r rune) int {
	if isWide(r) {
		return 2
	}
	return 1
}

func dispWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

// truncate cuts s so it occupies at most max columns without ever splitting a
// rune in half.
func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if dispWidth(s) <= max {
		return s
	}
	var sb strings.Builder
	w := 0
	for _, r := range s {
		rw := runeWidth(r)
		if w+rw > max {
			break
		}
		sb.WriteRune(r)
		w += rw
	}
	return sb.String()
}

// truncateVisible cuts s to at most max visible columns while keeping any ANSI
// sequences intact. Layout math must never count escape bytes as columns,
// otherwise right-aligned values get clipped - which is exactly what made
// "running" render as "runni" before this existed.
func truncateVisible(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if plainWidth(s) <= max {
		return s
	}
	runes := []rune(s)
	var sb strings.Builder
	w := 0
	for i := 0; i < len(runes); {
		if runes[i] == '\033' {
			// Copy the escape sequence verbatim; it costs zero columns.
			sb.WriteRune(runes[i])
			i++
			for i < len(runes) && runes[i] != 'm' {
				sb.WriteRune(runes[i])
				i++
			}
			if i < len(runes) {
				sb.WriteRune(runes[i])
				i++
			}
			continue
		}
		rw := runeWidth(runes[i])
		if w+rw > max {
			break
		}
		sb.WriteRune(runes[i])
		w += rw
		i++
	}
	return sb.String()
}

// padRight appends spaces until s occupies exactly w visible columns. It uses
// plainWidth because s usually already carries colour codes.
func padRight(s string, w int) string {
	cur := plainWidth(s)
	if cur >= w {
		return s
	}
	return s + strings.Repeat(" ", w-cur)
}

func repeat(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(s, n)
}

func paint(s, code string) string {
	if s == "" {
		return s
	}
	return code + s + reset
}

// boxLine renders one row of a light box: │<content>│
func boxLine(content string, inner int) string {
	return "│" + padRight(truncateVisible(content, inner), inner) + "│"
}

func boxTop(inner int) string    { return "┌" + repeat("─", inner) + "┐" }
func boxBottom(inner int) string { return "└" + repeat("─", inner) + "┘" }

// Size reports the terminal size with fallbacks for dumb or unknown terminals.
func Size() (int, int) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 || h <= 0 {
		w, h = 80, 24
	}
	if w > 200 {
		w = 200
	}
	if h > 64 {
		h = 64
	}
	if w < 40 {
		w = 40
	}
	if h < 12 {
		h = 12
	}
	return w, h
}

// makeRaw switches stdin to raw mode and returns the restore function.
func makeRaw() (func(), error) {
	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return func() {}, err
	}
	return func() { _ = term.Restore(fd, old) }, nil
}

func stdinIsTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}
