package wsadapter

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/gastownhall/tmux-adapter/internal/agentio"
	"github.com/gastownhall/tmux-adapter/internal/agents"
	"github.com/gastownhall/tmux-adapter/internal/tmux"
	"github.com/gastownhall/tmux-adapter/internal/wsbase"
)

// Server is the WebSocket server that manages client connections.
type Server struct {
	registry       *agents.Registry
	pipeMgr        *tmux.PipePaneManager
	ctrl           *tmux.ControlMode
	prompter       *agentio.Prompter
	authToken      string
	originPatterns []string
	clients        map[*Client]struct{}
	mu             sync.Mutex
}

// NewServer creates a new WebSocket server.
func NewServer(registry *agents.Registry, pipeMgr *tmux.PipePaneManager, ctrl *tmux.ControlMode, authToken string, originPatterns []string) *Server {
	return &Server{
		registry:       registry,
		pipeMgr:        pipeMgr,
		ctrl:           ctrl,
		prompter:       agentio.NewPrompter(ctrl, registry),
		authToken:      strings.TrimSpace(authToken),
		originPatterns: originPatterns,
		clients:        make(map[*Client]struct{}),
	}
}

// ServeHTTP handles WebSocket upgrade requests at /ws.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !wsbase.IsAuthorizedRequest(s.authToken, r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := wsbase.AcceptWebSocket(w, r, s.originPatterns)
	if err != nil {
		return
	}
	conn.SetReadLimit(int64(agentio.MaxFileUploadBytes + 64*1024))

	ctx, cancel := context.WithCancel(r.Context())
	client := NewClient(conn, s, ctx, cancel)

	s.mu.Lock()
	s.clients[client] = struct{}{}
	count := len(s.clients)
	s.mu.Unlock()

	log.Printf("client connected (%d total)", count)

	// Run read/write pumps — blocks until client disconnects
	go client.WritePump()
	client.ReadPump()

	// Cleanup on disconnect
	s.RemoveClient(client)
}

// BroadcastToAgentSubscribers sends a lifecycle event to subscribed clients,
// filtered per-client by session name regex. For added/removed events, also
// broadcasts an agents-count event to ALL subscribed clients (unfiltered) so
// dashboards can show totals. Skips agents-count for agent-updated events.
func (s *Server) BroadcastToAgentSubscribers(agentName string, eventType string, msg []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// agents-count event: only on added/removed, not on updated
	var countData []byte
	if eventType != "agent-updated" {
		total := s.registry.Count()
		countResp := Response{Type: "agents-count", TotalAgents: &total}
		var err error
		countData, err = json.Marshal(countResp)
		if err != nil {
			log.Printf("wsadapter: failed to marshal agents-count: %v", err)
			return
		}
	}

	for client := range s.clients {
		client.mu.Lock()
		subscribed := client.agentSub
		include := client.includeSessionFilter
		exclude := client.excludeSessionFilter
		client.mu.Unlock()

		if !subscribed {
			continue
		}

		// agents-count to all subscribed clients (only on added/removed)
		if countData != nil {
			client.SendText(countData)
		}

		// Lifecycle event only if agent passes this client's filter
		if wsbase.PassesFilter(agentName, include, exclude) {
			client.SendText(msg)
		}
	}
}

// RemoveClient unsubscribes and removes a client from the server.
func (s *Server) RemoveClient(client *Client) {
	s.mu.Lock()
	delete(s.clients, client)
	count := len(s.clients)
	s.mu.Unlock()

	client.Close()
	log.Printf("client disconnected (%d remaining)", count)
}

// CloseAll closes all connected clients.
func (s *Server) CloseAll() {
	s.mu.Lock()
	clients := make([]*Client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()

	for _, c := range clients {
		s.RemoveClient(c)
	}
}
