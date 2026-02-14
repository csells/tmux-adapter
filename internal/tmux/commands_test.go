package tmux

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// newStubCM creates a ControlMode with a stub stdin that responds via the provided function.
func newStubCM(respond func(cmd string) commandResponse) *ControlMode {
	cm := &ControlMode{
		responseCh:     make(chan commandResponse, 1),
		done:           make(chan struct{}),
		executeTimeout: 200 * time.Millisecond,
	}
	cm.stdin = writeCloserStub{
		writeFn: func(p []byte) (int, error) {
			cmd := strings.TrimSpace(string(p))
			go func() {
				cm.responseCh <- respond(cmd)
			}()
			return len(p), nil
		},
	}
	return cm
}

func TestCapturePaneVisibleFallsBackWhenNoAlternateScreen(t *testing.T) {
	var executed []string

	cm := &ControlMode{
		responseCh:     make(chan commandResponse, 1),
		done:           make(chan struct{}),
		executeTimeout: 200 * time.Millisecond,
	}
	cm.stdin = writeCloserStub{
		writeFn: func(p []byte) (int, error) {
			cmd := strings.TrimSpace(string(p))
			executed = append(executed, cmd)

			go func(command string) {
				if strings.Contains(command, "capture-pane -p -e -a ") {
					cm.responseCh <- commandResponse{err: fmt.Errorf("tmux: no alternate screen")}
					return
				}
				cm.responseCh <- commandResponse{output: "visible-screen"}
			}(cmd)

			return len(p), nil
		},
	}

	out, err := cm.CapturePaneVisible("hq-mayor")
	if err != nil {
		t.Fatalf("CapturePaneVisible() error = %v", err)
	}
	if out != "visible-screen" {
		t.Fatalf("output = %q, want %q", out, "visible-screen")
	}
	if len(executed) != 2 {
		t.Fatalf("executed command count = %d, want 2", len(executed))
	}
	if !strings.Contains(executed[0], "capture-pane -p -e -a ") {
		t.Fatalf("first command = %q, expected alternate-screen capture", executed[0])
	}
	if strings.Contains(executed[1], "capture-pane -p -e -a ") {
		t.Fatalf("second command = %q, expected non-alternate fallback", executed[1])
	}
}

func TestHasSession_Exists(t *testing.T) {
	cm := newStubCM(func(cmd string) commandResponse {
		return commandResponse{output: ""}
	})

	exists, err := cm.HasSession("my-session")
	if err != nil {
		t.Fatalf("HasSession() error = %v", err)
	}
	if !exists {
		t.Fatal("HasSession() = false, want true")
	}
}

func TestHasSession_NotFound(t *testing.T) {
	cm := newStubCM(func(cmd string) commandResponse {
		return commandResponse{err: fmt.Errorf("tmux: can't find session: my-session")}
	})

	exists, err := cm.HasSession("my-session")
	if err != nil {
		t.Fatalf("HasSession() error = %v, want nil for missing session", err)
	}
	if exists {
		t.Fatal("HasSession() = true, want false for missing session")
	}
}

func TestHasSession_PropagatesOtherErrors(t *testing.T) {
	cm := newStubCM(func(cmd string) commandResponse {
		return commandResponse{err: fmt.Errorf("tmux: server exited unexpectedly")}
	})

	exists, err := cm.HasSession("my-session")
	if err == nil {
		t.Fatal("HasSession() error = nil, want error for server failure")
	}
	if !strings.Contains(err.Error(), "server exited unexpectedly") {
		t.Fatalf("HasSession() error = %q, want server error", err)
	}
	if exists {
		t.Fatal("HasSession() = true, want false on error")
	}
}

func TestShowEnvironment_Found(t *testing.T) {
	cm := newStubCM(func(cmd string) commandResponse {
		return commandResponse{output: "MY_VAR=hello-world"}
	})

	val, err := cm.ShowEnvironment("my-session", "MY_VAR")
	if err != nil {
		t.Fatalf("ShowEnvironment() error = %v", err)
	}
	if val != "hello-world" {
		t.Fatalf("ShowEnvironment() = %q, want %q", val, "hello-world")
	}
}

func TestShowEnvironment_UnknownVariable(t *testing.T) {
	cm := newStubCM(func(cmd string) commandResponse {
		return commandResponse{err: fmt.Errorf("tmux: unknown variable: MY_VAR")}
	})

	val, err := cm.ShowEnvironment("my-session", "MY_VAR")
	if err != nil {
		t.Fatalf("ShowEnvironment() error = %v, want nil for unknown variable", err)
	}
	if val != "" {
		t.Fatalf("ShowEnvironment() = %q, want empty for unknown variable", val)
	}
}

func TestShowEnvironment_PropagatesOtherErrors(t *testing.T) {
	cm := newStubCM(func(cmd string) commandResponse {
		return commandResponse{err: fmt.Errorf("tmux: can't find session: my-session")}
	})

	val, err := cm.ShowEnvironment("my-session", "MY_VAR")
	if err == nil {
		t.Fatal("ShowEnvironment() error = nil, want error for missing session")
	}
	if !strings.Contains(err.Error(), "can't find session") {
		t.Fatalf("ShowEnvironment() error = %q, want session error", err)
	}
	if val != "" {
		t.Fatalf("ShowEnvironment() = %q, want empty on error", val)
	}
}

func TestCapturePaneHistory_HasHistory(t *testing.T) {
	cm := newStubCM(func(cmd string) commandResponse {
		return commandResponse{output: "line1\nline2\nline3"}
	})

	out, err := cm.CapturePaneHistory("my-session")
	if err != nil {
		t.Fatalf("CapturePaneHistory() error = %v", err)
	}
	if out != "line1\nline2\nline3" {
		t.Fatalf("CapturePaneHistory() = %q, want history lines", out)
	}
}

func TestCapturePaneHistory_NothingToCapture(t *testing.T) {
	cm := newStubCM(func(cmd string) commandResponse {
		return commandResponse{err: fmt.Errorf("tmux: nothing to capture")}
	})

	out, err := cm.CapturePaneHistory("my-session")
	if err != nil {
		t.Fatalf("CapturePaneHistory() error = %v, want nil for empty history", err)
	}
	if out != "" {
		t.Fatalf("CapturePaneHistory() = %q, want empty for no history", out)
	}
}

func TestCapturePaneHistory_PropagatesOtherErrors(t *testing.T) {
	cm := newStubCM(func(cmd string) commandResponse {
		return commandResponse{err: fmt.Errorf("tmux: can't find pane: my-session")}
	})

	out, err := cm.CapturePaneHistory("my-session")
	if err == nil {
		t.Fatal("CapturePaneHistory() error = nil, want error for missing pane")
	}
	if !strings.Contains(err.Error(), "can't find pane") {
		t.Fatalf("CapturePaneHistory() error = %q, want pane error", err)
	}
	if out != "" {
		t.Fatalf("CapturePaneHistory() = %q, want empty on error", out)
	}
}
