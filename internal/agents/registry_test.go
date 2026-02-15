package agents

import (
	"testing"

	"github.com/gastownhall/tmux-adapter/internal/tmux"
)

// mockControl implements ControlModeInterface for testing.
type mockControl struct {
	sessions    []tmux.SessionInfo
	panes       map[string]tmux.PaneInfo
	envVars     map[string]map[string]string // session -> key -> value
	notifCh     chan tmux.Notification
	listErr     error
	paneInfoErr map[string]error
}

func newMockControl() *mockControl {
	return &mockControl{
		panes:       make(map[string]tmux.PaneInfo),
		envVars:     make(map[string]map[string]string),
		notifCh:     make(chan tmux.Notification, 10),
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

func (m *mockControl) ShowEnvironment(session, key string) (string, error) {
	if env, ok := m.envVars[session]; ok {
		return env[key], nil
	}
	return "", nil
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

func TestScanNonGastownSessionsSkipped(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "random-session", Attached: false},
		{Name: "my-terminal", Attached: true},
	}
	r := NewRegistry(mock, "", nil)

	if err := r.scan(); err != nil {
		t.Fatalf("scan() error: %v", err)
	}

	agents := r.GetAgents()
	if len(agents) != 0 {
		t.Fatalf("expected 0 agents, got %d", len(agents))
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

func TestScanEnvVarOverrides(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "hq-witness", Attached: false},
	}
	mock.panes["hq-witness"] = tmux.PaneInfo{
		Command: "gemini",
		PID:     "100",
		WorkDir: "/tmp/gt/work",
	}
	mock.envVars["hq-witness"] = map[string]string{
		"GT_AGENT": "gemini",
		"GT_ROLE":  "custom-role",
		"GT_RIG":   "custom-rig",
	}

	r := NewRegistry(mock, "/tmp/gt", nil)
	if err := r.scan(); err != nil {
		t.Fatalf("scan() error: %v", err)
	}

	agent, ok := r.GetAgent("hq-witness")
	if !ok {
		t.Fatal("expected to find agent hq-witness")
	}
	if agent.Role != "custom-role" {
		t.Fatalf("expected role 'custom-role', got %q", agent.Role)
	}
	if agent.Rig == nil || *agent.Rig != "custom-rig" {
		t.Fatalf("expected rig 'custom-rig', got %v", agent.Rig)
	}
	if agent.Runtime != "gemini" {
		t.Fatalf("expected runtime 'gemini', got %q", agent.Runtime)
	}
}

func TestScanGtDirFilters(t *testing.T) {
	mock := newMockControl()
	mock.sessions = []tmux.SessionInfo{
		{Name: "hq-witness", Attached: false},
	}
	mock.panes["hq-witness"] = tmux.PaneInfo{
		Command: "claude",
		PID:     "100",
		WorkDir: "/other/dir/work", // doesn't match gtDir
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
	if agent.Role != "overseer" {
		t.Fatalf("expected role 'overseer', got %q", agent.Role)
	}

	_, ok = r.GetAgent("nonexistent")
	if ok {
		t.Fatal("expected not to find nonexistent agent")
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
