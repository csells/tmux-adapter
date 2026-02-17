package wsconv

import (
	"encoding/json"
	"testing"

	"github.com/gastownhall/tmux-adapter/internal/conv"
)

func TestBuildFilter(t *testing.T) {
	// nil input → zero-value filter
	f := buildFilter(nil)
	if f.Types != nil || f.ExcludeThinking || f.ExcludeProgress {
		t.Fatalf("nil input: got %+v, want zero filter", f)
	}

	// empty clientFilter → no types map
	f = buildFilter(&clientFilter{})
	if f.Types != nil {
		t.Fatalf("empty filter: Types = %v, want nil", f.Types)
	}

	// with types
	f = buildFilter(&clientFilter{Types: []string{"user", "assistant"}})
	if len(f.Types) != 2 || !f.Types["user"] || !f.Types["assistant"] {
		t.Fatalf("types filter: got %v, want {user:true, assistant:true}", f.Types)
	}

	// excludeThinking
	tr := true
	f = buildFilter(&clientFilter{ExcludeThinking: &tr})
	if !f.ExcludeThinking {
		t.Fatal("expected ExcludeThinking=true")
	}

	// excludeProgress
	f = buildFilter(&clientFilter{ExcludeProgress: &tr})
	if !f.ExcludeProgress {
		t.Fatal("expected ExcludeProgress=true")
	}
}

func TestExtractAgentFromConvID(t *testing.T) {
	// valid 3-part with known runtime
	if got := extractAgentFromConvID("claude:myagent:abc123"); got != "myagent" {
		t.Fatalf("got %q, want myagent", got)
	}

	// unknown runtime prefix → empty
	if got := extractAgentFromConvID("unknown:myagent:abc"); got != "" {
		t.Fatalf("got %q, want empty for unknown runtime", got)
	}

	// too few parts (1 part)
	if got := extractAgentFromConvID("justonepart"); got != "" {
		t.Fatalf("got %q, want empty for single part", got)
	}

	// 2 parts → empty (need exactly 3)
	if got := extractAgentFromConvID("claude:myagent"); got != "" {
		t.Fatalf("got %q, want empty for 2 parts", got)
	}

	// other known runtimes
	if got := extractAgentFromConvID("gemini:agent1:id1"); got != "agent1" {
		t.Fatalf("got %q, want agent1 for gemini runtime", got)
	}
}

func TestCapSnapshot(t *testing.T) {
	// under limit
	events := make([]conv.ConversationEvent, 5)
	result := capSnapshot(events)
	if len(result) != 5 {
		t.Fatalf("len = %d, want 5", len(result))
	}

	// exactly at limit
	events = make([]conv.ConversationEvent, maxSnapshotEvents)
	result = capSnapshot(events)
	if len(result) != maxSnapshotEvents {
		t.Fatalf("len = %d, want %d", len(result), maxSnapshotEvents)
	}

	// over limit — should truncate to tail
	events = make([]conv.ConversationEvent, maxSnapshotEvents+100)
	for i := range events {
		events[i].Seq = int64(i)
	}
	result = capSnapshot(events)
	if len(result) != maxSnapshotEvents {
		t.Fatalf("len = %d, want %d", len(result), maxSnapshotEvents)
	}
	if result[0].Seq != 100 {
		t.Fatalf("first seq = %d, want 100 (tail)", result[0].Seq)
	}
}

func TestMakeCursor(t *testing.T) {
	// nil events → empty
	if got := makeCursor("conv1", nil); got != "" {
		t.Fatalf("got %q, want empty for nil events", got)
	}

	// empty slice → empty
	if got := makeCursor("conv1", []conv.ConversationEvent{}); got != "" {
		t.Fatalf("got %q, want empty for empty events", got)
	}

	// non-empty events → cursor from last event
	events := []conv.ConversationEvent{
		{Seq: 1, EventID: "e1"},
		{Seq: 5, EventID: "e5"},
	}
	cursor := makeCursor("conv1", events)
	if cursor == "" {
		t.Fatal("expected non-empty cursor")
	}

	var c conv.Cursor
	if err := json.Unmarshal([]byte(cursor), &c); err != nil {
		t.Fatalf("unmarshal cursor: %v", err)
	}
	if c.ConversationID != "conv1" {
		t.Fatalf("cursor convID = %q, want conv1", c.ConversationID)
	}
	if c.Seq != 5 {
		t.Fatalf("cursor seq = %d, want 5", c.Seq)
	}
	if c.EventID != "e5" {
		t.Fatalf("cursor eventID = %q, want e5", c.EventID)
	}
}

func TestEncodeCursor(t *testing.T) {
	c := conv.Cursor{ConversationID: "test", Seq: 42, EventID: "ev42"}
	result := encodeCursor(c)

	var decoded conv.Cursor
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ConversationID != "test" || decoded.Seq != 42 || decoded.EventID != "ev42" {
		t.Fatalf("decoded = %+v, want {ConversationID:test, Seq:42, EventID:ev42}", decoded)
	}
}

func TestSubID(t *testing.T) {
	if got := subID(1); got != "sub-1" {
		t.Fatalf("subID(1) = %q, want sub-1", got)
	}
	if got := subID(42); got != "sub-42" {
		t.Fatalf("subID(42) = %q, want sub-42", got)
	}
}
