package agents

import "github.com/gastownhall/tmux-adapter/internal/tmux"

// ControlModeInterface abstracts the tmux control mode operations
// needed by Registry, enabling testing with mock implementations.
type ControlModeInterface interface {
	ListSessions() ([]tmux.SessionInfo, error)
	GetPaneInfo(session string) (tmux.PaneInfo, error)
	ShowEnvironment(session, key string) (string, error)
	Notifications() <-chan tmux.Notification
}
