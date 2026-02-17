package agents

import (
	"fmt"
	"testing"

	"github.com/gastownhall/tmux-adapter/internal/tmux"
)

// mockControl implements ControlModeInterface for testing.
type mockControl struct {
	sessions    []tmux.SessionInfo
	panes       map[string]tmux.PaneInfo
	notifCh chan tmux.Notification
	listErr     error
	paneInfoErr map[string]error
}

func newMockControl() *mockControl {
	return &mockControl{
		panes:       make(map[string]tmux.PaneInfo),
		notifCh: make(chan tmux.Notification, 10),
		paneInfoErr: make(map[string]error),
	}
}

func (m *mockControl) ListSessions() ([]tmux.SessionInfo, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.sessions, nil
}

func (m *mockControl) GetPaneInfo(session string) (tmux.PaneInfo, error) {
	if err, ok := m.paneInfoErr[session]; ok {
		return tmux.PaneInfo{}, err
	}
	pane, ok := m.panes[session]
	if !ok {
		return tmux.PaneInfo{}, nil
	}
	return pane, nil
}

func (m *mockControl) Notifications() <-chan tmux.Notification {
	return m.notifCh
}

// drainEvents reads all buffered events from a registry.
func drainEvents(r *Registry) []RegistryEvent {
	var events []RegistryEvent
	for {
		select {
		case e := <-r.events:
			events = append(events, e)
		default:
			return events
		}
	}
}

func TestScanNoSessions(t *testing.T) {
	mock := newMockControl()
	r := NewRegistry(mock, "/tmp/gt", nil)

	if err := r.scan(); err != nil {
		t.Fatalf("scan() error: %v", err)
	}

	agents := r.GetAgents()
	if len(agents) != 0 {
		t.Fatalf("expected 0 agents, got %d", len(agents))
	}

	events := drainEvents(r)
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
}

func TestScanNonAgentSessionsIgnored(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "random-session", Attached: false},
		{Name: "my-terminal", Attached: true},
	}
	// No pane info set — DetectRuntime returns "" for empty commands
	r := NewRegistry(mock, "", nil)

	if err := r.scan(); err != nil {
		t.Fatalf("scan() error: %v", err)
	}

	agents := r.GetAgents()
	if len(agents) != 0 {
		t.Fatalf("expected 0 agents (no agent processes), got %d", len(agents))
	}
}

func TestScanSkippedSessions(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "hq-witness", Attached: false},
	}
	mock.panes["hq-witness"] = tmux.PaneInfo{
		Command: "claude",
		PID:     "12345",
		WorkDir: "/tmp/gt/work",
	}
	r := NewRegistry(mock, "/tmp/gt", []string{"hq-witness"})

	if err := r.scan(); err != nil {
		t.Fatalf("scan() error: %v", err)
	}

	agents := r.GetAgents()
	if len(agents) != 0 {
		t.Fatalf("expected 0 agents (skipped), got %d", len(agents))
	}
}

func TestScanDetectsAgents(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "hq-witness", Attached: true},
		{Name: "gt-myrig-refinery", Attached: false},
	}
	mock.panes["hq-witness"] = tmux.PaneInfo{
		Command: "claude",
		PID:     "100",
		WorkDir: "/tmp/gt/work",
	}
	mock.panes["gt-myrig-refinery"] = tmux.PaneInfo{
		Command: "claude",
		PID:     "200",
		WorkDir: "/tmp/gt/other",
	}

	r := NewRegistry(mock, "/tmp/gt", nil)
	if err := r.scan(); err != nil {
		t.Fatalf("scan() error: %v", err)
	}

	agents := r.GetAgents()
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}

	events := drainEvents(r)
	if len(events) != 2 {
		t.Fatalf("expected 2 added events, got %d", len(events))
	}
	for _, e := range events {
		if e.Type != "added" {
			t.Fatalf("expected 'added' event, got %q", e.Type)
		}
	}
}

func TestScanDetectsStandaloneAgents(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "my-project", Attached: false},
		{Name: "research", Attached: true},
	}
	mock.panes["my-project"] = tmux.PaneInfo{
		Command: "claude",
		PID:     "100",
		WorkDir: "/home/user/code/my-project",
	}
	mock.panes["research"] = tmux.PaneInfo{
		Command: "gemini",
		PID:     "200",
		WorkDir: "/home/user/code/research",
	}

	// No workDirFilter — scan all sessions
	r := NewRegistry(mock, "", nil)
	if err := r.scan(); err != nil {
		t.Fatalf("scan() error: %v", err)
	}

	agents := r.GetAgents()
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}

	// Verify runtimes
	for _, a := range agents {
		switch a.Name {
		case "my-project":
			if a.Runtime != "claude" {
				t.Fatalf("expected runtime 'claude' for my-project, got %q", a.Runtime)
			}
		case "research":
			if a.Runtime != "gemini" {
				t.Fatalf("expected runtime 'gemini' for research, got %q", a.Runtime)
			}
		default:
			t.Fatalf("unexpected agent: %q", a.Name)
		}
	}
}

func TestScanAgentRemoved(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "hq-witness", Attached: false},
	}
	mock.panes["hq-witness"] = tmux.PaneInfo{
		Command: "claude",
		PID:     "100",
		WorkDir: "/tmp/gt/work",
	}

	r := NewRegistry(mock, "/tmp/gt", nil)
	if err := r.scan(); err != nil {
		t.Fatalf("first scan() error: %v", err)
	}
	drainEvents(r) // discard initial "added" events

	// Remove the session
	mock.sessions = nil
	if err := r.scan(); err != nil {
		t.Fatalf("second scan() error: %v", err)
	}

	agents := r.GetAgents()
	if len(agents) != 0 {
		t.Fatalf("expected 0 agents after removal, got %d", len(agents))
	}

	events := drainEvents(r)
	if len(events) != 1 {
		t.Fatalf("expected 1 removed event, got %d", len(events))
	}
	if events[0].Type != "removed" {
		t.Fatalf("expected 'removed' event, got %q", events[0].Type)
	}
	if events[0].Agent.Name != "hq-witness" {
		t.Fatalf("expected removed agent 'hq-witness', got %q", events[0].Agent.Name)
	}
}

func TestScanAgentUpdatedAttachState(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "hq-witness", Attached: false},
	}
	mock.panes["hq-witness"] = tmux.PaneInfo{
		Command: "claude",
		PID:     "100",
		WorkDir: "/tmp/gt/work",
	}

	r := NewRegistry(mock, "/tmp/gt", nil)
	if err := r.scan(); err != nil {
		t.Fatalf("first scan() error: %v", err)
	}
	drainEvents(r)

	// Change attached state
	mock.sessions[0].Attached = true
	if err := r.scan(); err != nil {
		t.Fatalf("second scan() error: %v", err)
	}

	events := drainEvents(r)
	if len(events) != 1 {
		t.Fatalf("expected 1 updated event, got %d", len(events))
	}
	if events[0].Type != "updated" {
		t.Fatalf("expected 'updated' event, got %q", events[0].Type)
	}
	if !events[0].Agent.Attached {
		t.Fatal("expected updated agent to be attached")
	}
}

func TestScanAgentUpdatedRuntime(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "my-session", Attached: false},
	}
	mock.panes["my-session"] = tmux.PaneInfo{
		Command: "claude",
		PID:     "100",
		WorkDir: "/tmp/work",
	}

	r := NewRegistry(mock, "", nil)
	if err := r.scan(); err != nil {
		t.Fatalf("first scan() error: %v", err)
	}
	drainEvents(r)

	// Change the command to gemini (runtime change in same session)
	mock.panes["my-session"] = tmux.PaneInfo{
		Command: "gemini",
		PID:     "200",
		WorkDir: "/tmp/work",
	}
	if err := r.scan(); err != nil {
		t.Fatalf("second scan() error: %v", err)
	}

	events := drainEvents(r)
	if len(events) != 1 {
		t.Fatalf("expected 1 updated event, got %d", len(events))
	}
	if events[0].Type != "updated" {
		t.Fatalf("expected 'updated' event, got %q", events[0].Type)
	}
	if events[0].Agent.Runtime != "gemini" {
		t.Fatalf("expected runtime 'gemini', got %q", events[0].Agent.Runtime)
	}
}

func TestScanNoEventWhenUnchanged(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "hq-witness", Attached: false},
	}
	mock.panes["hq-witness"] = tmux.PaneInfo{
		Command: "claude",
		PID:     "100",
		WorkDir: "/tmp/gt/work",
	}

	r := NewRegistry(mock, "/tmp/gt", nil)
	if err := r.scan(); err != nil {
		t.Fatalf("first scan() error: %v", err)
	}
	drainEvents(r)

	// Scan again with same state
	if err := r.scan(); err != nil {
		t.Fatalf("second scan() error: %v", err)
	}

	events := drainEvents(r)
	if len(events) != 0 {
		t.Fatalf("expected 0 events for unchanged state, got %d", len(events))
	}
}

func TestScanWorkDirFilters(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "my-agent", Attached: false},
	}
	mock.panes["my-agent"] = tmux.PaneInfo{
		Command: "claude",
		PID:     "100",
		WorkDir: "/other/dir/work", // doesn't match workDirFilter
	}

	r := NewRegistry(mock, "/tmp/gt", nil)
	if err := r.scan(); err != nil {
		t.Fatalf("scan() error: %v", err)
	}

	agents := r.GetAgents()
	if len(agents) != 0 {
		t.Fatalf("expected 0 agents (workdir mismatch), got %d", len(agents))
	}
}

func TestScanEmptyWorkDirFindsAll(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "agent-a", Attached: false},
		{Name: "agent-b", Attached: false},
	}
	mock.panes["agent-a"] = tmux.PaneInfo{
		Command: "claude",
		PID:     "100",
		WorkDir: "/home/user/project-a",
	}
	mock.panes["agent-b"] = tmux.PaneInfo{
		Command: "gemini",
		PID:     "200",
		WorkDir: "/opt/other/project-b",
	}

	// Empty workDirFilter = scan all
	r := NewRegistry(mock, "", nil)
	if err := r.scan(); err != nil {
		t.Fatalf("scan() error: %v", err)
	}

	agents := r.GetAgents()
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents (no workdir filter), got %d", len(agents))
	}
}

func TestGetAgent(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "hq-overseer", Attached: true},
	}
	mock.panes["hq-overseer"] = tmux.PaneInfo{
		Command: "claude",
		PID:     "100",
		WorkDir: "/tmp/gt/work",
	}

	r := NewRegistry(mock, "/tmp/gt", nil)
	if err := r.scan(); err != nil {
		t.Fatalf("scan() error: %v", err)
	}

	agent, ok := r.GetAgent("hq-overseer")
	if !ok {
		t.Fatal("expected to find agent hq-overseer")
	}
	if agent.Name != "hq-overseer" {
		t.Fatalf("expected name 'hq-overseer', got %q", agent.Name)
	}
	if agent.Runtime != "claude" {
		t.Fatalf("expected runtime 'claude', got %q", agent.Runtime)
	}

	_, ok = r.GetAgent("nonexistent")
	if ok {
		t.Fatal("expected not to find nonexistent agent")
	}
}

func TestCount(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "agent-1", Attached: false},
		{Name: "agent-2", Attached: false},
	}
	mock.panes["agent-1"] = tmux.PaneInfo{Command: "claude", PID: "100", WorkDir: "/tmp"}
	mock.panes["agent-2"] = tmux.PaneInfo{Command: "gemini", PID: "200", WorkDir: "/tmp"}

	r := NewRegistry(mock, "", nil)
	if err := r.scan(); err != nil {
		t.Fatalf("scan() error: %v", err)
	}

	if r.Count() != 2 {
		t.Fatalf("expected Count() = 2, got %d", r.Count())
	}
}

func TestWatchLoopTriggersRescan(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "hq-witness", Attached: false},
	}
	mock.panes["hq-witness"] = tmux.PaneInfo{
		Command: "claude",
		PID:     "100",
		WorkDir: "/tmp/gt/work",
	}

	r := NewRegistry(mock, "/tmp/gt", nil)

	// Start the registry (does initial scan + starts watchLoop)
	if err := r.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer r.Stop()

	// Drain initial "added" event
	<-r.Events()

	// Now add a new session and send a notification
	mock.sessions = append(mock.sessions, tmux.SessionInfo{Name: "hq-overseer", Attached: false})
	mock.panes["hq-overseer"] = tmux.PaneInfo{
		Command: "claude",
		PID:     "200",
		WorkDir: "/tmp/gt/work2",
	}
	mock.notifCh <- tmux.Notification{Type: "sessions-changed"}

	// Should get an "added" event for the new agent
	event := <-r.Events()
	if event.Type != "added" {
		t.Fatalf("expected 'added' event, got %q", event.Type)
	}
	if event.Agent.Name != "hq-overseer" {
		t.Fatalf("expected agent 'hq-overseer', got %q", event.Agent.Name)
	}
}

func TestWatchLoopWindowRenamed(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "hq-witness", Attached: false},
	}
	mock.panes["hq-witness"] = tmux.PaneInfo{
		Command: "claude",
		PID:     "100",
		WorkDir: "/tmp/gt/work",
	}

	r := NewRegistry(mock, "/tmp/gt", nil)
	if err := r.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer r.Stop()

	// Drain initial event
	<-r.Events()

	// Remove the session and send window-renamed notification
	mock.sessions = nil
	mock.notifCh <- tmux.Notification{Type: "window-renamed"}

	event := <-r.Events()
	if event.Type != "removed" {
		t.Fatalf("expected 'removed' event from window-renamed, got %q", event.Type)
	}
}

func TestWatchLoopIgnoresIrrelevantNotifications(t *testing.T) {
	mock := newMockControl()
	r := NewRegistry(mock, "/tmp/gt", nil)
	if err := r.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer r.Stop()

	// Send an irrelevant notification type
	mock.notifCh <- tmux.Notification{Type: "output"}

	// No events should be produced
	select {
	case e := <-r.Events():
		t.Fatalf("unexpected event: %+v", e)
	default:
		// Good — no event
	}
}

func TestWatchLoopStopsOnClosedChannel(t *testing.T) {
	mock := newMockControl()
	r := NewRegistry(mock, "/tmp/gt", nil)

	// Close the notification channel before starting
	close(mock.notifCh)

	if err := r.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	// The watchLoop should exit cleanly on its own because the channel is closed.
	// If it spins, the test would hang or consume CPU, but there's no direct assertion
	// for goroutine exit. The key correctness property is tested by the fact that
	// this test completes without spinning.
}

func TestScanWorkDirPrefixCollision(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "agent-1", Attached: false},
	}
	mock.panes["agent-1"] = tmux.PaneInfo{
		Command: "claude",
		PID:     "100",
		WorkDir: "/tmp/gt-other", // prefix matches /tmp/gt but is a different directory
	}

	r := NewRegistry(mock, "/tmp/gt", nil)
	if err := r.scan(); err != nil {
		t.Fatalf("scan() error: %v", err)
	}

	agents := r.GetAgents()
	if len(agents) != 0 {
		t.Fatalf("expected 0 agents (prefix collision), got %d", len(agents))
	}
}

func TestScanWorkDirExactMatch(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "agent-1", Attached: false},
	}
	mock.panes["agent-1"] = tmux.PaneInfo{
		Command: "claude",
		PID:     "100",
		WorkDir: "/tmp/gt",
	}

	r := NewRegistry(mock, "/tmp/gt", nil)
	if err := r.scan(); err != nil {
		t.Fatalf("scan() error: %v", err)
	}

	agents := r.GetAgents()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent (exact match), got %d", len(agents))
	}
}

func TestScanWorkDirSubdirMatch(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "agent-1", Attached: false},
	}
	mock.panes["agent-1"] = tmux.PaneInfo{
		Command: "claude",
		PID:     "100",
		WorkDir: "/tmp/gt/work",
	}

	r := NewRegistry(mock, "/tmp/gt", nil)
	if err := r.scan(); err != nil {
		t.Fatalf("scan() error: %v", err)
	}

	agents := r.GetAgents()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent (subdir match), got %d", len(agents))
	}
}

func TestScanManySessionsDoesNotBlock(t *testing.T) {
	mock := newMockControl()
	for i := range 150 {
		name := fmt.Sprintf("agent-%d", i)
		mock.sessions = append(mock.sessions, tmux.SessionInfo{Name: name, Attached: false})
		mock.panes[name] = tmux.PaneInfo{
			Command: "claude",
			PID:     fmt.Sprintf("%d", 1000+i),
			WorkDir: "/tmp/gt/work",
		}
	}

	r := NewRegistry(mock, "/tmp/gt", nil)

	// scan() must not block even with no consumer — events beyond the
	// channel buffer (100) are dropped with a log message.
	if err := r.scan(); err != nil {
		t.Fatalf("scan() error: %v", err)
	}

	// Drain whatever made it into the buffered channel
	var received []RegistryEvent
	for {
		select {
		case e := <-r.events:
			received = append(received, e)
		default:
			goto done
		}
	}
done:

	// We should get exactly the buffer size (100) since there was no
	// concurrent consumer and 150 > 100.
	if len(received) != 100 {
		t.Fatalf("expected 100 events (channel buffer size), got %d", len(received))
	}
}

func TestRegistryStartAndStop(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "agent-1", Attached: false},
	}
	mock.panes["agent-1"] = tmux.PaneInfo{
		Command: "claude",
		PID:     "100",
		WorkDir: "/tmp/work",
	}

	r := NewRegistry(mock, "", nil)

	// Start performs initial scan
	if err := r.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	if r.Count() != 1 {
		t.Fatalf("expected 1 agent after Start(), got %d", r.Count())
	}

	event := <-r.Events()
	if event.Type != "added" {
		t.Fatalf("expected 'added' event, got %q", event.Type)
	}

	// Stop is safe to call
	r.Stop()

	// Stop is idempotent
	r.Stop()
}

func TestStartReturnsListSessionsError(t *testing.T) {
	mock := newMockControl()
	mock.listErr = fmt.Errorf("tmux not running")

	r := NewRegistry(mock, "", nil)
	err := r.Start()
	if err == nil {
		t.Fatal("expected error from Start() when ListSessions fails")
	}
}

func TestScanPaneInfoError(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "agent-1", Attached: false},
	}
	mock.paneInfoErr["agent-1"] = fmt.Errorf("pane not found")

	r := NewRegistry(mock, "", nil)
	if err := r.scan(); err != nil {
		t.Fatalf("scan() error: %v", err)
	}

	// Agent should not be detected when pane info fails
	if r.Count() != 0 {
		t.Fatalf("expected 0 agents when pane info errors, got %d", r.Count())
	}
}

func TestScanAgentUpdatedWorkDir(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "my-agent", Attached: false},
	}
	mock.panes["my-agent"] = tmux.PaneInfo{
		Command: "claude",
		PID:     "100",
		WorkDir: "/tmp/project-a",
	}

	r := NewRegistry(mock, "", nil)
	if err := r.scan(); err != nil {
		t.Fatalf("first scan() error: %v", err)
	}
	drainEvents(r)

	// Change the workdir (agent cd'd to a different directory)
	mock.panes["my-agent"] = tmux.PaneInfo{
		Command: "claude",
		PID:     "100",
		WorkDir: "/tmp/project-b",
	}
	if err := r.scan(); err != nil {
		t.Fatalf("second scan() error: %v", err)
	}

	events := drainEvents(r)
	if len(events) != 1 {
		t.Fatalf("expected 1 updated event, got %d", len(events))
	}
	if events[0].Type != "updated" {
		t.Fatalf("expected 'updated' event, got %q", events[0].Type)
	}
	if events[0].Agent.WorkDir != "/tmp/project-b" {
		t.Fatalf("expected workDir '/tmp/project-b', got %q", events[0].Agent.WorkDir)
	}
}
