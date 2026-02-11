// Package vt provides server-side VT100 terminal emulation with row-level diffing.
// It wraps hinshun/vt10x to process raw terminal bytes and emit styled text rows
// using only ANSI SGR (color/style) escape codes — no cursor movement gunk.
package vt

import (
	"fmt"
	"strings"
	"sync"

	"github.com/hinshun/vt10x"
)

// Glyph mode bits (matching vt10x's unexported constants).
const (
	modeBold      = 4
	modeUnderline = 2
	modeItalic    = 16
)

// ScreenUpdate holds changed rows since the last update.
type ScreenUpdate struct {
	Rows      map[int]string `json:"rows"`
	CursorRow int            `json:"cursorRow"`
	CursorCol int            `json:"cursorCol"`
}

// ScreenSnapshot holds the full terminal screen state.
type ScreenSnapshot struct {
	Rows      map[int]string `json:"rows"`
	Cols      int            `json:"cols"`
	NumRows   int            `json:"numRows"`
	CursorRow int            `json:"cursorRow"`
	CursorCol int            `json:"cursorCol"`
}

// Screen wraps a vt10x terminal emulator and provides row-level diffing.
// Rows are rendered as text with ANSI SGR escape codes for styling.
type Screen struct {
	term     vt10x.Terminal
	cols     int
	rows     int
	mu       sync.Mutex
	prevRows []string // cached rendered rows for diffing
}

// NewScreen creates a new VT100 screen emulator with the given dimensions.
func NewScreen(cols, rows int) *Screen {
	return &Screen{
		term:     vt10x.New(vt10x.WithSize(cols, rows)),
		cols:     cols,
		rows:     rows,
		prevRows: make([]string, rows),
	}
}

// Write feeds raw bytes into the terminal emulator and returns a ScreenUpdate
// containing only the rows that changed. Returns nil if nothing changed.
func (s *Screen) Write(data []byte) *ScreenUpdate {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.term.Write(data)

	update := &ScreenUpdate{
		Rows: make(map[int]string),
	}

	s.term.Lock()
	cursor := s.term.Cursor()
	update.CursorRow = cursor.Y
	update.CursorCol = cursor.X

	for y := 0; y < s.rows; y++ {
		row := s.renderRow(y)
		if row != s.prevRows[y] {
			update.Rows[y] = row
			s.prevRows[y] = row
		}
	}
	s.term.Unlock()

	if len(update.Rows) == 0 {
		return nil
	}
	return update
}

// Snapshot returns the full screen state and syncs the diff baseline.
func (s *Screen) Snapshot() *ScreenSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap := &ScreenSnapshot{
		Rows:    make(map[int]string),
		Cols:    s.cols,
		NumRows: s.rows,
	}

	s.term.Lock()
	cursor := s.term.Cursor()
	snap.CursorRow = cursor.Y
	snap.CursorCol = cursor.X

	for y := 0; y < s.rows; y++ {
		row := s.renderRow(y)
		snap.Rows[y] = row
		s.prevRows[y] = row
	}
	s.term.Unlock()

	return snap
}

// renderRow renders a single row as text with ANSI SGR escape codes.
// Must be called with s.term locked and s.mu held.
func (s *Screen) renderRow(y int) string {
	// Find the last column with non-trivial content (non-space or styled).
	lastCol := -1
	for x := s.cols - 1; x >= 0; x-- {
		cell := s.term.Cell(x, y)
		ch := cell.Char
		if ch == 0 {
			ch = ' '
		}
		if ch != ' ' || cell.FG != vt10x.DefaultFG || cell.BG != vt10x.DefaultBG ||
			cell.Mode&(modeBold|modeItalic|modeUnderline) != 0 {
			lastCol = x
			break
		}
	}

	if lastCol < 0 {
		return "" // entirely empty row
	}

	var b strings.Builder
	var curFG vt10x.Color = vt10x.DefaultFG
	var curBG vt10x.Color = vt10x.DefaultBG
	var curBold, curItalic, curUnderline bool
	styled := false

	for x := 0; x <= lastCol; x++ {
		cell := s.term.Cell(x, y)
		bold := cell.Mode&modeBold != 0
		italic := cell.Mode&modeItalic != 0
		underline := cell.Mode&modeUnderline != 0

		if cell.FG != curFG || cell.BG != curBG || bold != curBold || italic != curItalic || underline != curUnderline {
			var params []string

			// If removing attributes, reset first then re-apply what's needed
			needReset := (!bold && curBold) || (!italic && curItalic) || (!underline && curUnderline) ||
				(cell.FG == vt10x.DefaultFG && curFG != vt10x.DefaultFG) ||
				(cell.BG == vt10x.DefaultBG && curBG != vt10x.DefaultBG)

			if needReset {
				params = append(params, "0")
				curFG = vt10x.DefaultFG
				curBG = vt10x.DefaultBG
				curBold = false
				curItalic = false
				curUnderline = false
			}

			if bold && !curBold {
				params = append(params, "1")
			}
			if italic && !curItalic {
				params = append(params, "3")
			}
			if underline && !curUnderline {
				params = append(params, "4")
			}
			if cell.FG != curFG {
				params = append(params, fgSGR(cell.FG))
			}
			if cell.BG != curBG {
				params = append(params, bgSGR(cell.BG))
			}

			if len(params) > 0 {
				b.WriteString("\x1b[")
				b.WriteString(strings.Join(params, ";"))
				b.WriteString("m")
				styled = true
			}

			curFG = cell.FG
			curBG = cell.BG
			curBold = bold
			curItalic = italic
			curUnderline = underline
		}

		ch := cell.Char
		if ch == 0 {
			ch = ' '
		}
		b.WriteRune(ch)
	}

	// Reset at end of row if any style is active
	if styled && (curFG != vt10x.DefaultFG || curBG != vt10x.DefaultBG || curBold || curItalic || curUnderline) {
		b.WriteString("\x1b[0m")
	}

	return b.String()
}

// fgSGR returns the SGR parameter(s) for a foreground color.
func fgSGR(c vt10x.Color) string {
	if c == vt10x.DefaultFG {
		return "39"
	}
	n := uint32(c)
	if n < 8 {
		return fmt.Sprintf("%d", 30+n)
	}
	if n < 16 {
		return fmt.Sprintf("%d", 90+n-8)
	}
	if n < 256 {
		return fmt.Sprintf("38;5;%d", n)
	}
	r := (n >> 16) & 0xFF
	g := (n >> 8) & 0xFF
	bl := n & 0xFF
	return fmt.Sprintf("38;2;%d;%d;%d", r, g, bl)
}

// bgSGR returns the SGR parameter(s) for a background color.
func bgSGR(c vt10x.Color) string {
	if c == vt10x.DefaultBG {
		return "49"
	}
	n := uint32(c)
	if n < 8 {
		return fmt.Sprintf("%d", 40+n)
	}
	if n < 16 {
		return fmt.Sprintf("%d", 100+n-8)
	}
	if n < 256 {
		return fmt.Sprintf("48;5;%d", n)
	}
	r := (n >> 16) & 0xFF
	g := (n >> 8) & 0xFF
	bl := n & 0xFF
	return fmt.Sprintf("48;2;%d;%d;%d", r, g, bl)
}
