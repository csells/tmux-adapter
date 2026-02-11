package vt

import (
	"strings"
	"testing"
)

func TestNewScreen(t *testing.T) {
	s := NewScreen(80, 24)
	snap := s.Snapshot()
	if snap.Cols != 80 {
		t.Errorf("cols: got %d, want 80", snap.Cols)
	}
	if snap.NumRows != 24 {
		t.Errorf("numRows: got %d, want 24", snap.NumRows)
	}
	if snap.CursorRow != 0 || snap.CursorCol != 0 {
		t.Errorf("cursor: got (%d,%d), want (0,0)", snap.CursorRow, snap.CursorCol)
	}
	// All rows should be empty
	for i := 0; i < 24; i++ {
		if snap.Rows[i] != "" {
			t.Errorf("row %d: got %q, want empty", i, snap.Rows[i])
		}
	}
}

func TestWritePlainText(t *testing.T) {
	s := NewScreen(80, 24)
	update := s.Write([]byte("hello world"))
	if update == nil {
		t.Fatal("expected non-nil update")
	}
	row, ok := update.Rows[0]
	if !ok {
		t.Fatal("expected row 0 in update")
	}
	if row != "hello world" {
		t.Errorf("row 0: got %q, want %q", row, "hello world")
	}
	// Cursor should be after the text
	if update.CursorCol != 11 {
		t.Errorf("cursorCol: got %d, want 11", update.CursorCol)
	}
}

func TestWriteWithNewlines(t *testing.T) {
	s := NewScreen(80, 24)
	update := s.Write([]byte("line one\r\nline two\r\nline three"))
	if update == nil {
		t.Fatal("expected non-nil update")
	}
	if update.Rows[0] != "line one" {
		t.Errorf("row 0: got %q, want %q", update.Rows[0], "line one")
	}
	if update.Rows[1] != "line two" {
		t.Errorf("row 1: got %q, want %q", update.Rows[1], "line two")
	}
	if update.Rows[2] != "line three" {
		t.Errorf("row 2: got %q, want %q", update.Rows[2], "line three")
	}
}

func TestWriteWithColors(t *testing.T) {
	s := NewScreen(80, 24)
	// Red text: ESC[31m
	update := s.Write([]byte("\x1b[31mhello\x1b[0m world"))
	if update == nil {
		t.Fatal("expected non-nil update")
	}
	row := update.Rows[0]
	// Row should contain SGR codes
	if !strings.Contains(row, "\x1b[") {
		t.Errorf("expected SGR codes, got %q", row)
	}
	// Should contain the text (strip ANSI to check)
	plain := stripANSI(row)
	if plain != "hello world" {
		t.Errorf("plain text: got %q, want %q", plain, "hello world")
	}
}

func TestWriteWithBold(t *testing.T) {
	s := NewScreen(80, 24)
	// Bold: ESC[1m
	update := s.Write([]byte("\x1b[1mbold\x1b[0m normal"))
	if update == nil {
		t.Fatal("expected non-nil update")
	}
	row := update.Rows[0]
	// Should have bold SGR (1m)
	if !strings.Contains(row, "\x1b[1m") && !strings.Contains(row, ";1;") && !strings.Contains(row, ";1m") {
		t.Errorf("expected bold SGR, got %q", row)
	}
	plain := stripANSI(row)
	if plain != "bold normal" {
		t.Errorf("plain text: got %q, want %q", plain, "bold normal")
	}
}

func TestCursorMovement(t *testing.T) {
	s := NewScreen(80, 24)
	// Write "hello", then move cursor to position (0,0) and write "HELLO"
	// ESC[H moves cursor to (1,1) in VT100 (1-indexed)
	update := s.Write([]byte("hello\x1b[HHELLO"))
	if update == nil {
		t.Fatal("expected non-nil update")
	}
	row := update.Rows[0]
	plain := stripANSI(row)
	// "hello" overwritten by "HELLO"
	if plain != "HELLO" {
		t.Errorf("row 0: got %q, want %q", plain, "HELLO")
	}
}

func TestScreenClear(t *testing.T) {
	s := NewScreen(80, 24)
	// Write text, then clear screen (ESC[2J) and move home (ESC[H)
	s.Write([]byte("old text"))
	update := s.Write([]byte("\x1b[2J\x1b[Hnew text"))
	if update == nil {
		t.Fatal("expected non-nil update")
	}
	plain := stripANSI(update.Rows[0])
	if plain != "new text" {
		t.Errorf("row 0 after clear: got %q, want %q", plain, "new text")
	}
}

func TestDiffOnlyChangedRows(t *testing.T) {
	s := NewScreen(80, 24)
	// Write to row 0
	s.Write([]byte("initial"))
	// Now write to row 1 only
	update := s.Write([]byte("\r\nsecond line"))
	if update == nil {
		t.Fatal("expected non-nil update for new line")
	}
	// Row 0 should NOT be in the update (it didn't change)
	if _, ok := update.Rows[0]; ok {
		t.Error("row 0 should not be in update (unchanged)")
	}
	// Row 1 should be in the update
	if _, ok := update.Rows[1]; !ok {
		t.Error("row 1 should be in update")
	}
}

func TestNilUpdateWhenNoChange(t *testing.T) {
	s := NewScreen(80, 24)
	s.Write([]byte("hello"))
	// Writing empty data should not change anything
	update := s.Write([]byte(""))
	if update != nil {
		t.Errorf("expected nil update for empty write, got %+v", update)
	}
}

func TestSnapshotSyncsBaseline(t *testing.T) {
	s := NewScreen(80, 24)
	s.Write([]byte("hello"))
	// Take snapshot (this syncs the baseline)
	snap := s.Snapshot()
	if snap.Rows[0] != "hello" {
		t.Errorf("snapshot row 0: got %q, want %q", snap.Rows[0], "hello")
	}
	// Now write the same text again — nothing should change
	update := s.Write([]byte(""))
	if update != nil {
		t.Error("expected nil update after snapshot sync")
	}
}

func TestScrolling(t *testing.T) {
	s := NewScreen(80, 5) // small screen to test scrolling
	// Fill all 5 rows and write one more to trigger scroll
	s.Write([]byte("line1\r\nline2\r\nline3\r\nline4\r\nline5"))
	snap := s.Snapshot()
	if stripANSI(snap.Rows[0]) != "line1" {
		t.Errorf("before scroll row 0: got %q", stripANSI(snap.Rows[0]))
	}
	// Write another line — this should scroll
	update := s.Write([]byte("\r\nline6"))
	if update == nil {
		t.Fatal("expected non-nil update after scroll")
	}
	snap2 := s.Snapshot()
	// line1 should have scrolled off, line2 should now be at row 0
	if stripANSI(snap2.Rows[0]) != "line2" {
		t.Errorf("after scroll row 0: got %q, want %q", stripANSI(snap2.Rows[0]), "line2")
	}
	if stripANSI(snap2.Rows[4]) != "line6" {
		t.Errorf("after scroll row 4: got %q, want %q", stripANSI(snap2.Rows[4]), "line6")
	}
}

func Test256Color(t *testing.T) {
	s := NewScreen(80, 24)
	// 256-color: ESC[38;5;196m (bright red)
	update := s.Write([]byte("\x1b[38;5;196mcolored\x1b[0m"))
	if update == nil {
		t.Fatal("expected non-nil update")
	}
	row := update.Rows[0]
	plain := stripANSI(row)
	if plain != "colored" {
		t.Errorf("plain text: got %q, want %q", plain, "colored")
	}
	// Should encode the color back as 256-color SGR
	if !strings.Contains(row, "38;5;") {
		t.Errorf("expected 256-color SGR, got %q", row)
	}
}

func TestBackgroundColor(t *testing.T) {
	s := NewScreen(80, 24)
	// Green background: ESC[42m
	update := s.Write([]byte("\x1b[42m bg \x1b[0m"))
	if update == nil {
		t.Fatal("expected non-nil update")
	}
	row := update.Rows[0]
	// Should have background color SGR (42 or 48;5;2)
	if !strings.Contains(row, "\x1b[") {
		t.Errorf("expected SGR codes for background, got %q", row)
	}
}

func TestConcurrentWrites(t *testing.T) {
	s := NewScreen(80, 24)
	done := make(chan struct{}, 10)
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			s.Write([]byte("hello\r\n"))
			s.Snapshot()
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	// Just verify no panic/deadlock
}

// stripANSI removes ANSI escape sequences from a string.
func stripANSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			// Skip until 'm'
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
