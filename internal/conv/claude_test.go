package conv

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

func TestClaudeParserUserMessage(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	raw := []byte(`{"type":"user","uuid":"u1","timestamp":"2026-02-14T01:44:54.253Z","message":{"role":"user","content":[{"type":"text","text":"hello world"}]}}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	e := events[0]
	if e.Type != EventUser {
		t.Fatalf("Type = %q, want %q", e.Type, EventUser)
	}
	if e.Role != "user" {
		t.Fatalf("Role = %q, want %q", e.Role, "user")
	}
	if e.EventID != "u1" {
		t.Fatalf("EventID = %q, want %q", e.EventID, "u1")
	}
	if len(e.Content) != 1 || e.Content[0].Text != "hello world" {
		t.Fatalf("Content = %+v, want text block with 'hello world'", e.Content)
	}
	if e.Runtime != "claude" {
		t.Fatalf("Runtime = %q, want %q", e.Runtime, "claude")
	}
}

func TestClaudeParserAssistantMessage(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	raw := []byte(`{"type":"assistant","uuid":"a1","requestId":"req1","timestamp":"2026-02-14T01:45:00.362Z","message":{"model":"claude-opus-4-6","role":"assistant","content":[{"type":"text","text":"Here is my response."}],"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":10,"cache_creation_input_tokens":5}}}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	e := events[0]
	if e.Type != EventAssistant {
		t.Fatalf("Type = %q, want %q", e.Type, EventAssistant)
	}
	if e.Model != "claude-opus-4-6" {
		t.Fatalf("Model = %q, want %q", e.Model, "claude-opus-4-6")
	}
	if e.RequestID != "req1" {
		t.Fatalf("RequestID = %q, want %q", e.RequestID, "req1")
	}
	if e.TokenUsage == nil {
		t.Fatal("TokenUsage is nil")
	}
	if e.TokenUsage.InputTokens != 100 {
		t.Fatalf("InputTokens = %d, want 100", e.TokenUsage.InputTokens)
	}
	if e.TokenUsage.OutputTokens != 50 {
		t.Fatalf("OutputTokens = %d, want 50", e.TokenUsage.OutputTokens)
	}
}

func TestClaudeParserToolUse(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	raw := []byte(`{"type":"assistant","uuid":"a2","timestamp":"2026-02-14T01:45:01.055Z","message":{"model":"claude-opus-4-6","role":"assistant","content":[{"type":"tool_use","id":"toolu_123","name":"Read","input":{"file_path":"/tmp/test.go"}}]}}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	e := events[0]
	if e.Type != EventToolUse {
		t.Fatalf("Type = %q, want %q", e.Type, EventToolUse)
	}
	if e.Content[0].ToolName != "Read" {
		t.Fatalf("ToolName = %q, want %q", e.Content[0].ToolName, "Read")
	}
	if e.Content[0].ToolID != "toolu_123" {
		t.Fatalf("ToolID = %q, want %q", e.Content[0].ToolID, "toolu_123")
	}
}

func TestClaudeParserThinking(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	raw := []byte(`{"type":"assistant","uuid":"a3","timestamp":"2026-02-14T01:44:59.309Z","message":{"model":"claude-opus-4-6","role":"assistant","content":[{"type":"thinking","thinking":"Let me think about this...","signature":"sig123"}]}}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	e := events[0]
	if e.Type != EventThinking {
		t.Fatalf("Type = %q, want %q", e.Type, EventThinking)
	}
	if e.Content[0].Text != "Let me think about this..." {
		t.Fatalf("Text = %q, want thinking text", e.Content[0].Text)
	}
	if e.Content[0].Signature != "sig123" {
		t.Fatalf("Signature = %q, want %q", e.Content[0].Signature, "sig123")
	}
}

func TestClaudeParserToolResult(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	raw := []byte(`{"type":"user","uuid":"u2","timestamp":"2026-02-14T01:45:01.076Z","message":{"role":"user","content":[{"tool_use_id":"toolu_123","type":"tool_result","content":"file contents here"}]}}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	e := events[0]
	if e.Type != EventToolResult {
		t.Fatalf("Type = %q, want %q", e.Type, EventToolResult)
	}
	if e.Content[0].ToolID != "toolu_123" {
		t.Fatalf("ToolID = %q, want %q", e.Content[0].ToolID, "toolu_123")
	}
	if e.Content[0].Output != "file contents here" {
		t.Fatalf("Output = %q, want tool output", e.Content[0].Output)
	}
}

func TestClaudeParserProgress(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	raw := []byte(`{"type":"progress","uuid":"p1","timestamp":"2026-02-14T01:44:54.307Z","data":{"type":"hook_progress","hookEvent":"SessionStart","hookName":"SessionStart:clear","command":"bd prime"}}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	e := events[0]
	if e.Type != EventProgress {
		t.Fatalf("Type = %q, want %q", e.Type, EventProgress)
	}
	if e.Metadata["progressType"] != "hook_progress" {
		t.Fatalf("progressType = %v, want %q", e.Metadata["progressType"], "hook_progress")
	}
}

func TestClaudeParserQueueOp(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	raw := []byte(`{"type":"queue-operation","operation":"enqueue","timestamp":"2026-02-14T01:44:54.458Z","sessionId":"abc","content":"background task completed"}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	e := events[0]
	if e.Type != EventQueueOp {
		t.Fatalf("Type = %q, want %q", e.Type, EventQueueOp)
	}
	if e.Metadata["operation"] != "enqueue" {
		t.Fatalf("operation = %v, want %q", e.Metadata["operation"], "enqueue")
	}
}

func TestClaudeParserFileHistorySnapshotSkipped(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	raw := []byte(`{"type":"file-history-snapshot","messageId":"m1","snapshot":{}}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("got %d events, want 0 (file-history-snapshot should be skipped)", len(events))
	}
}

func TestClaudeParserMalformedJSON(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	raw := []byte(`{invalid json here`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v (should return error event, not error)", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1 error event", len(events))
	}
	if events[0].Type != EventError {
		t.Fatalf("Type = %q, want %q", events[0].Type, EventError)
	}
}

func TestClaudeParserUnknownType(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	raw := []byte(`{"type":"future-new-type","uuid":"f1","timestamp":"2026-02-14T01:44:54.253Z"}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1 system event", len(events))
	}
	if events[0].Type != EventSystem {
		t.Fatalf("Type = %q, want %q", events[0].Type, EventSystem)
	}
	if events[0].Metadata["originalType"] != "future-new-type" {
		t.Fatalf("originalType = %v, want %q", events[0].Metadata["originalType"], "future-new-type")
	}
}

func TestClaudeParserRealSamples(t *testing.T) {
	f, err := os.Open("testdata/claude/sample.jsonl")
	if err != nil {
		t.Skipf("test data not available: %v", err)
	}
	defer func() { _ = f.Close() }()

	parser := NewClaudeParser("test-agent", "claude:test-agent:sample")
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 2*1024*1024), 2*1024*1024)

	lineNum := 0
	totalEvents := 0
	for scanner.Scan() {
		lineNum++
		events, err := parser.Parse(scanner.Bytes())
		if err != nil {
			t.Fatalf("line %d: Parse() error = %v", lineNum, err)
		}
		for _, e := range events {
			if e.Runtime != "claude" {
				t.Fatalf("line %d: Runtime = %q, want %q", lineNum, e.Runtime, "claude")
			}
			if e.AgentName != "test-agent" {
				t.Fatalf("line %d: AgentName = %q, want %q", lineNum, e.AgentName, "test-agent")
			}
		}
		totalEvents += len(events)
	}

	if lineNum == 0 {
		t.Fatal("no lines read from sample file")
	}
	if totalEvents == 0 {
		t.Fatal("no events parsed from sample file")
	}
}

func TestClaudeParserRuntime(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")
	if got := parser.Runtime(); got != "claude" {
		t.Fatalf("Runtime() = %q, want %q", got, "claude")
	}
}

func TestClaudeParserReset(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")
	// Reset should not panic — that's the only contract.
	parser.Reset()
}

func TestTruncateContent(t *testing.T) {
	// Under MaxContentSize — returned as-is
	short := "hello"
	if got := truncateContent(short); got != short {
		t.Fatalf("truncateContent(%q) = %q, want %q", short, got, short)
	}

	// Exactly MaxContentSize — returned as-is
	exact := strings.Repeat("x", MaxContentSize)
	if got := truncateContent(exact); got != exact {
		t.Fatalf("truncateContent(len=%d) length = %d, want %d", len(exact), len(got), MaxContentSize)
	}

	// Over MaxContentSize — truncated
	big := strings.Repeat("y", MaxContentSize+100)
	got := truncateContent(big)
	if len(got) != MaxContentSize {
		t.Fatalf("truncateContent(len=%d) length = %d, want %d", len(big), len(got), MaxContentSize)
	}
}

func TestExtractToolResultContentArray(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	// tool_result with content as array of blocks (the array-of-blocks path)
	raw := []byte(`{"type":"user","uuid":"u3","timestamp":"2026-02-14T01:45:01.076Z","message":{"role":"user","content":[{"tool_use_id":"toolu_456","type":"tool_result","content":[{"type":"text","text":"result from array"}]}]}}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	e := events[0]
	if e.Type != EventToolResult {
		t.Fatalf("Type = %q, want %q", e.Type, EventToolResult)
	}
	if e.Content[0].Output != "result from array" {
		t.Fatalf("Output = %q, want %q", e.Content[0].Output, "result from array")
	}
}

func TestParseAssistantNilMessage(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	raw := []byte(`{"type":"assistant","uuid":"a-nil","timestamp":"2026-02-14T01:45:00.000Z"}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if events != nil {
		t.Fatalf("got %d events, want nil for assistant with nil message", len(events))
	}
}

func TestParseUserNilMessage(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	raw := []byte(`{"type":"user","uuid":"u-nil","timestamp":"2026-02-14T01:45:00.000Z"}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if events != nil {
		t.Fatalf("got %d events, want nil for user with nil message", len(events))
	}
}

func TestParseUserStringContent(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	// User message with content as a plain string (not array)
	raw := []byte(`{"type":"user","uuid":"u-str","timestamp":"2026-02-14T01:44:54.253Z","message":{"role":"user","content":"plain text message"}}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Type != EventUser {
		t.Fatalf("Type = %q, want %q", events[0].Type, EventUser)
	}
	if len(events[0].Content) != 1 || events[0].Content[0].Text != "plain text message" {
		t.Fatalf("Content = %+v, want single text block", events[0].Content)
	}
}

func TestParseAssistantEmptyContent(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	// Assistant message with empty content array → nil
	raw := []byte(`{"type":"assistant","uuid":"a-empty","timestamp":"2026-02-14T01:45:00.000Z","message":{"role":"assistant","content":[]}}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if events != nil {
		t.Fatalf("got %d events, want nil for empty content", len(events))
	}
}

func TestParseParentEventID(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	raw := []byte(`{"type":"user","uuid":"u-child","parentUuid":"u-parent","timestamp":"2026-02-14T01:44:54.253Z","message":{"role":"user","content":"reply"}}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].ParentEventID != "u-parent" {
		t.Fatalf("ParentEventID = %q, want %q", events[0].ParentEventID, "u-parent")
	}
}

func TestParseProgressNoData(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	// Progress event with no data field
	raw := []byte(`{"type":"progress","uuid":"p-nodata","timestamp":"2026-02-14T01:44:54.307Z"}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Type != EventProgress {
		t.Fatalf("Type = %q, want %q", events[0].Type, EventProgress)
	}
	// Metadata should be empty (no data fields set)
	if len(events[0].Metadata) != 0 {
		t.Fatalf("Metadata = %v, want empty", events[0].Metadata)
	}
}

func TestParseQueueOpNoContent(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	raw := []byte(`{"type":"queue-operation","operation":"dequeue","timestamp":"2026-02-14T01:44:54.458Z","sessionId":"abc"}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Metadata["operation"] != "dequeue" {
		t.Fatalf("operation = %v, want %q", events[0].Metadata["operation"], "dequeue")
	}
	// content key should not be in metadata when empty
	if _, ok := events[0].Metadata["content"]; ok {
		t.Fatal("Metadata should not include 'content' when it's empty")
	}
}

func TestParseMessageIDFallback(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	// When uuid is absent, messageId is used as EventID
	raw := []byte(`{"type":"user","messageId":"msg-123","timestamp":"2026-02-14T01:44:54.253Z","message":{"role":"user","content":"hi"}}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].EventID != "msg-123" {
		t.Fatalf("EventID = %q, want %q", events[0].EventID, "msg-123")
	}
}

func TestParseAssistantMultipleBlocks(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	// Assistant message with both text and tool_use → should be EventAssistant (not tool_use since >1 block)
	raw := []byte(`{"type":"assistant","uuid":"a-multi","timestamp":"2026-02-14T01:45:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Let me read that file."},{"type":"tool_use","id":"toolu_789","name":"Read","input":{"path":"/tmp/foo"}}]}}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Type != EventAssistant {
		t.Fatalf("Type = %q, want %q (multiple blocks)", events[0].Type, EventAssistant)
	}
	if len(events[0].Content) != 2 {
		t.Fatalf("Content blocks = %d, want 2", len(events[0].Content))
	}
}

func TestParseCacheUsage(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	raw := []byte(`{"type":"assistant","uuid":"a-cache","timestamp":"2026-02-14T01:45:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":50,"output_tokens":20,"cache_read_input_tokens":30,"cache_creation_input_tokens":10}}}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	u := events[0].TokenUsage
	if u == nil {
		t.Fatal("TokenUsage is nil")
	}
	if u.CacheRead != 30 {
		t.Fatalf("CacheRead = %d, want 30", u.CacheRead)
	}
	if u.CacheCreate != 10 {
		t.Fatalf("CacheCreate = %d, want 10", u.CacheCreate)
	}
}

func TestParseAssistantMalformedMessage(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	// message field is not valid JSON for claudeMessage
	raw := []byte(`{"type":"assistant","uuid":"a-bad","timestamp":"2026-02-14T01:45:00.000Z","message":"not-an-object"}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1 error event", len(events))
	}
	if events[0].Type != EventError {
		t.Fatalf("Type = %q, want %q", events[0].Type, EventError)
	}
}

func TestParseUserMalformedMessage(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	raw := []byte(`{"type":"user","uuid":"u-bad","timestamp":"2026-02-14T01:45:00.000Z","message":"not-an-object"}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1 error event", len(events))
	}
	if events[0].Type != EventError {
		t.Fatalf("Type = %q, want %q", events[0].Type, EventError)
	}
}

func TestParseToolResultWithNilContent(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	// tool_result with null content
	raw := []byte(`{"type":"user","uuid":"u-tnc","timestamp":"2026-02-14T01:45:01.076Z","message":{"role":"user","content":[{"tool_use_id":"toolu_nil","type":"tool_result","content":null}]}}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Content[0].Output != "" {
		t.Fatalf("Output = %q, want empty for nil content", events[0].Content[0].Output)
	}
}

func TestParseContentBlocksEmptyText(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	// text block with empty text should be skipped
	raw := []byte(`{"type":"assistant","uuid":"a-et","timestamp":"2026-02-14T01:45:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":""}]}}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	// empty text block is skipped, so no blocks → nil events
	if events != nil {
		t.Fatalf("got %d events, want nil for empty text block", len(events))
	}
}

func TestParseContentBlocksEmptyString(t *testing.T) {
	parser := NewClaudeParser("test-agent", "claude:test-agent:abc123")

	// content as empty string → no content blocks but still returns a user event
	raw := []byte(`{"type":"user","uuid":"u-es","timestamp":"2026-02-14T01:44:54.253Z","message":{"role":"user","content":""}}`)
	events, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Type != EventUser {
		t.Fatalf("Type = %q, want %q", events[0].Type, EventUser)
	}
	if len(events[0].Content) != 0 {
		t.Fatalf("Content len = %d, want 0 for empty string content", len(events[0].Content))
	}
}
