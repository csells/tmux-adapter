package tmux

import (
	"context"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"sync"
	"time"

	"github.com/gastownhall/tmux-adapter/internal/vt"
)

// PipePaneManager manages pipe-pane output streaming per agent session.
type PipePaneManager struct {
	ctrl    *ControlMode
	mu      sync.Mutex
	streams map[string]*pipeStream
}

type pipeStream struct {
	session     string
	filePath    string
	screen      *vt.Screen
	cancel      context.CancelFunc
	subscribers map[chan *vt.ScreenUpdate]struct{}
	mu          sync.Mutex
}

// NewPipePaneManager creates a new pipe-pane manager.
func NewPipePaneManager(ctrl *ControlMode) *PipePaneManager {
	return &PipePaneManager{
		ctrl:    ctrl,
		streams: make(map[string]*pipeStream),
	}
}

// Subscribe starts streaming output for a session and returns a channel for receiving screen updates.
// If this is the first subscriber, pipe-pane is activated.
func (pm *PipePaneManager) Subscribe(session string) (<-chan *vt.ScreenUpdate, *vt.ScreenSnapshot, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	ch := make(chan *vt.ScreenUpdate, 256)

	stream, exists := pm.streams[session]
	if exists {
		stream.mu.Lock()
		stream.subscribers[ch] = struct{}{}
		snap := stream.screen.Snapshot()
		stream.mu.Unlock()
		return ch, snap, nil
	}

	// First subscriber — activate pipe-pane
	filePath := fmt.Sprintf("/tmp/adapter-%s.pipe", session)

	// Create the file if it doesn't exist
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, nil, fmt.Errorf("create pipe file: %w", err)
	}
	f.Close()

	// Activate pipe-pane
	if err := pm.ctrl.PipePaneStart(session, fmt.Sprintf("cat >> %s", filePath)); err != nil {
		os.Remove(filePath)
		return nil, nil, fmt.Errorf("activate pipe-pane: %w", err)
	}

	screen := vt.NewScreen(80, 24)

	ctx, cancel := context.WithCancel(context.Background())
	stream = &pipeStream{
		session:     session,
		filePath:    filePath,
		screen:      screen,
		cancel:      cancel,
		subscribers: map[chan *vt.ScreenUpdate]struct{}{ch: {}},
	}
	pm.streams[session] = stream

	snap := screen.Snapshot()

	go pm.tailFile(ctx, stream)

	return ch, snap, nil
}

// Unsubscribe removes a subscriber. If it was the last one, pipe-pane is deactivated.
func (pm *PipePaneManager) Unsubscribe(session string, ch <-chan *vt.ScreenUpdate) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	stream, exists := pm.streams[session]
	if !exists {
		return
	}

	// Find and remove the channel
	stream.mu.Lock()
	for sub := range stream.subscribers {
		if (<-chan *vt.ScreenUpdate)(sub) == ch {
			delete(stream.subscribers, sub)
			close(sub)
			break
		}
	}
	remaining := len(stream.subscribers)
	stream.mu.Unlock()

	if remaining == 0 {
		pm.stopStream(stream)
		delete(pm.streams, session)
	}
}

// StopAll deactivates all pipe-panes and cleans up.
func (pm *PipePaneManager) StopAll() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for name, stream := range pm.streams {
		pm.stopStream(stream)
		delete(pm.streams, name)
	}
}

func (pm *PipePaneManager) stopStream(stream *pipeStream) {
	stream.cancel()
	pm.ctrl.PipePaneStop(stream.session)

	stream.mu.Lock()
	for ch := range stream.subscribers {
		close(ch)
	}
	stream.subscribers = nil
	stream.mu.Unlock()

	os.Remove(stream.filePath)
}

// tailFile reads new bytes from the pipe file, processes them through the VT screen,
// and fans out screen updates to subscribers at ~30fps.
func (pm *PipePaneManager) tailFile(ctx context.Context, stream *pipeStream) {
	f, err := os.Open(stream.filePath)
	if err != nil {
		log.Printf("open pipe file %s: %v", stream.filePath, err)
		return
	}
	defer f.Close()

	// Seek to end — we only want new output
	f.Seek(0, io.SeekEnd)

	// Pending update accumulates dirty rows across multiple reads.
	// The read goroutine writes it; the ticker loop reads and clears it.
	var pending *vt.ScreenUpdate
	var pendingMu sync.Mutex

	// Read goroutine: continuously reads bytes and feeds through VT screen
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 4096)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			n, err := f.Read(buf)
			if n > 0 {
				update := stream.screen.Write(buf[:n])
				if update != nil {
					pendingMu.Lock()
					if pending == nil {
						pending = update
					} else {
						maps.Copy(pending.Rows, update.Rows)
						pending.CursorRow = update.CursorRow
						pending.CursorCol = update.CursorCol
					}
					pendingMu.Unlock()
				}
			}

			if err != nil || n == 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(50 * time.Millisecond):
				}
			}
		}
	}()

	// Send loop: flush accumulated updates to subscribers at ~30fps
	ticker := time.NewTicker(33 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-readDone:
			return
		case <-ticker.C:
			pendingMu.Lock()
			update := pending
			pending = nil
			pendingMu.Unlock()

			if update == nil {
				continue
			}

			stream.mu.Lock()
			for ch := range stream.subscribers {
				select {
				case ch <- update:
				default:
					// Subscriber is slow — drop this update
				}
			}
			stream.mu.Unlock()
		}
	}
}
