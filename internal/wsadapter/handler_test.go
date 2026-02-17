package wsadapter

import (
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/gastownhall/tmux-adapter/internal/agents"
)

// ---------------------------------------------------------------------------
// tmuxKeyNameFromVT — complete coverage of all key mappings
// ---------------------------------------------------------------------------

func TestTmuxKeyNameFromVTAllKeys(t *testing.T) {
	cases := []struct {
		payload string
		wantKey string
	}{
		// Shift+Tab
		{"\x1b[Z", "BTab"},

		// Arrow keys (CSI form)
		{"\x1b[A", "Up"},
		{"\x1b[B", "Down"},
		{"\x1b[C", "Right"},
		{"\x1b[D", "Left"},

		// Arrow keys (SS3 form)
		{"\x1bOA", "Up"},
		{"\x1bOB", "Down"},
		{"\x1bOC", "Right"},
		{"\x1bOD", "Left"},

		// Home/End (CSI form)
		{"\x1b[H", "Home"},
		{"\x1b[F", "End"},

		// Home/End (SS3 form)
		{"\x1bOH", "Home"},
		{"\x1bOF", "End"},

		// Page up/down
		{"\x1b[5~", "PgUp"},
		{"\x1b[6~", "PgDn"},

		// Insert/Delete
		{"\x1b[2~", "IC"},
		{"\x1b[3~", "DC"},

		// F1-F4 (SS3 form)
		{"\x1bOP", "F1"},
		{"\x1bOQ", "F2"},
		{"\x1bOR", "F3"},
		{"\x1bOS", "F4"},

		// F5-F12 (CSI form)
		{"\x1b[15~", "F5"},
		{"\x1b[17~", "F6"},
		{"\x1b[18~", "F7"},
		{"\x1b[19~", "F8"},
		{"\x1b[20~", "F9"},
		{"\x1b[21~", "F10"},
		{"\x1b[23~", "F11"},
		{"\x1b[24~", "F12"},

		// Special keys
		{"\x1b", "Escape"},
		{"\x7f", "BSpace"},
	}

	for _, tc := range cases {
		got, ok := tmuxKeyNameFromVT([]byte(tc.payload))
		if !ok {
			t.Fatalf("tmuxKeyNameFromVT(%q) returned ok=false, want key=%q", tc.payload, tc.wantKey)
		}
		if got != tc.wantKey {
			t.Fatalf("tmuxKeyNameFromVT(%q) = %q, want %q", tc.payload, got, tc.wantKey)
		}
	}
}

func TestTmuxKeyNameFromVTUnknown(t *testing.T) {
	unknowns := []string{
		"not-a-vt-seq",
		"a",
		"\x1b[99~",
		"",
	}
	for _, s := range unknowns {
		if key, ok := tmuxKeyNameFromVT([]byte(s)); ok {
			t.Fatalf("tmuxKeyNameFromVT(%q) returned ok=true key=%q, want ok=false", s, key)
		}
	}
}

// ---------------------------------------------------------------------------
// drainWithQuiescence
// ---------------------------------------------------------------------------

func TestDrainWithQuiescenceSilence(t *testing.T) {
	// No data arrives — silence timer fires and returns empty buffer
	ch := make(chan []byte, 1)
	buf := drainWithQuiescence(ch, 50*time.Millisecond, 5*time.Second)
	if len(buf) != 0 {
		t.Fatalf("expected empty buffer, got %d bytes", len(buf))
	}
}

func TestDrainWithQuiescenceDataThenSilence(t *testing.T) {
	ch := make(chan []byte, 10)
	ch <- []byte("hello")
	ch <- []byte(" world")
	// After these two chunks, no more data → silence timer fires

	buf := drainWithQuiescence(ch, 50*time.Millisecond, 5*time.Second)
	if string(buf) != "hello world" {
		t.Fatalf("expected %q, got %q", "hello world", string(buf))
	}
}

func TestDrainWithQuiescenceHardTimeout(t *testing.T) {
	ch := make(chan []byte, 100)

	// Continuously feed data faster than the silence timer
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case ch <- []byte("x"):
			case <-time.After(200 * time.Millisecond):
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	start := time.Now()
	buf := drainWithQuiescence(ch, 500*time.Millisecond, 100*time.Millisecond)
	elapsed := time.Since(start)
	<-done

	if len(buf) == 0 {
		t.Fatal("expected non-empty buffer from continuous data")
	}
	// Hard timeout is 100ms; should complete well before the 500ms silence timer
	if elapsed > 300*time.Millisecond {
		t.Fatalf("expected hard timeout around 100ms, took %v", elapsed)
	}
}

func TestDrainWithQuiescenceChannelClose(t *testing.T) {
	ch := make(chan []byte, 5)
	ch <- []byte("data")
	close(ch)

	buf := drainWithQuiescence(ch, 1*time.Second, 5*time.Second)
	if string(buf) != "data" {
		t.Fatalf("expected %q, got %q", "data", string(buf))
	}
}

// ---------------------------------------------------------------------------
// filterAgents
// ---------------------------------------------------------------------------

func TestFilterAgentsNilFilters(t *testing.T) {
	all := []agents.Agent{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	}
	result := filterAgents(all, nil, nil)
	if len(result) != 3 {
		t.Fatalf("expected 3 agents, got %d", len(result))
	}
}

func TestFilterAgentsIncludeOnly(t *testing.T) {
	inc := regexp.MustCompile("^agent-")
	all := []agents.Agent{
		{Name: "agent-foo"}, {Name: "other"}, {Name: "agent-bar"},
	}
	result := filterAgents(all, inc, nil)
	if len(result) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(result))
	}
	if result[0].Name != "agent-foo" || result[1].Name != "agent-bar" {
		t.Fatalf("unexpected agents: %v", result)
	}
}

func TestFilterAgentsExcludeOnly(t *testing.T) {
	exc := regexp.MustCompile("debug")
	all := []agents.Agent{
		{Name: "agent-foo"}, {Name: "debug-agent"}, {Name: "agent-bar"},
	}
	result := filterAgents(all, nil, exc)
	if len(result) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(result))
	}
	if result[0].Name != "agent-foo" || result[1].Name != "agent-bar" {
		t.Fatalf("unexpected agents: %v", result)
	}
}

func TestFilterAgentsBoth(t *testing.T) {
	inc := regexp.MustCompile("^agent-")
	exc := regexp.MustCompile("debug")
	all := []agents.Agent{
		{Name: "agent-foo"}, {Name: "agent-debug"}, {Name: "other"}, {Name: "agent-bar"},
	}
	result := filterAgents(all, inc, exc)
	if len(result) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(result))
	}
	if result[0].Name != "agent-foo" || result[1].Name != "agent-bar" {
		t.Fatalf("unexpected agents: %v", result)
	}
}

func TestFilterAgentsEmptyList(t *testing.T) {
	inc := regexp.MustCompile("^agent-")
	result := filterAgents(nil, inc, nil)
	if len(result) != 0 {
		t.Fatalf("expected 0 agents, got %d", len(result))
	}
}

// ---------------------------------------------------------------------------
// MakeAgentEvent
// ---------------------------------------------------------------------------

func TestMakeAgentEventAdded(t *testing.T) {
	agent := agents.Agent{Name: "foo", Runtime: "claude", WorkDir: "/tmp", Attached: true}
	data, err := MakeAgentEvent("added", agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp Response
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.Type != "agent-added" {
		t.Fatalf("type = %q, want %q", resp.Type, "agent-added")
	}
	if resp.Agent == nil {
		t.Fatal("expected non-nil agent")
	}
	if resp.Agent.Name != "foo" {
		t.Fatalf("agent name = %q, want %q", resp.Agent.Name, "foo")
	}
}

func TestMakeAgentEventRemoved(t *testing.T) {
	agent := agents.Agent{Name: "bar"}
	data, err := MakeAgentEvent("removed", agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp Response
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.Type != "agent-removed" {
		t.Fatalf("type = %q, want %q", resp.Type, "agent-removed")
	}
	if resp.Name != "bar" {
		t.Fatalf("name = %q, want %q", resp.Name, "bar")
	}
}

func TestMakeAgentEventUpdated(t *testing.T) {
	agent := agents.Agent{Name: "baz", Runtime: "gemini", WorkDir: "/home", Attached: false}
	data, err := MakeAgentEvent("updated", agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp Response
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.Type != "agent-updated" {
		t.Fatalf("type = %q, want %q", resp.Type, "agent-updated")
	}
	if resp.Agent == nil {
		t.Fatal("expected non-nil agent")
	}
	if resp.Agent.Name != "baz" {
		t.Fatalf("agent name = %q, want %q", resp.Agent.Name, "baz")
	}
	if resp.Agent.Runtime != "gemini" {
		t.Fatalf("agent runtime = %q, want %q", resp.Agent.Runtime, "gemini")
	}
}
