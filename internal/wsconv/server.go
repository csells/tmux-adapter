package wsconv

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/gastownhall/tmux-adapter/internal/agentio"
	"github.com/gastownhall/tmux-adapter/internal/agents"
	"github.com/gastownhall/tmux-adapter/internal/conv"
	"github.com/gastownhall/tmux-adapter/internal/tmux"
	"github.com/gastownhall/tmux-adapter/internal/wsbase"
)

// maxSnapshotEvents caps the number of events in a single snapshot message.
const maxSnapshotEvents = 20000

// Server manages WebSocket connections for the converter service.
type Server struct {
	watcher        *conv.ConversationWatcher
	ctrl           *tmux.ControlMode
	registry       *agents.Registry
	prompter       *agentio.Prompter
	authToken      string
	originPatterns []string
	clients        map[*Client]struct{}
	mu             sync.Mutex
}

// NewServer creates a new converter WebSocket server.
func NewServer(watcher *conv.ConversationWatcher, authToken string, originPatterns []string, ctrl *tmux.ControlMode, registry *agents.Registry) *Server {
	return &Server{
		watcher:        watcher,
		ctrl:           ctrl,
		registry:       registry,
		prompter:       agentio.NewPrompter(ctrl, registry),
		authToken:      authToken,
		originPatterns: originPatterns,
		clients:        make(map[*Client]struct{}),
	}
}

// HandleWebSocket is the HTTP handler for /ws.
func (s *Server) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	if !wsbase.IsAuthorizedRequest(s.authToken, r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := wsbase.AcceptWebSocket(w, r, s.originPatterns)
	if err != nil {
		return
	}
	conn.SetReadLimit(int64(agentio.MaxFileUploadBytes + 64*1024))

	client := newClient(conn, s)
	s.addClient(client)
	defer s.removeClient(client)

	client.run()
}

// Broadcast sends a watcher event to all connected clients.
func (s *Server) Broadcast(event conv.WatcherEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch event.Type {
	case "agent-added", "agent-removed", "agent-updated":
		s.broadcastAgentLifecycle(event)
	case "conversation-started":
		for c := range s.clients {
			c.deliverConversationStarted(event)
		}
	case "conversation-event":
		if event.Event == nil {
			return
		}
		for c := range s.clients {
			c.deliverConversationEvent(event.Event)
		}
	case "conversation-switched":
		for c := range s.clients {
			c.deliverConversationSwitch(event)
		}
	}
}

// broadcastAgentLifecycle sends agent lifecycle events to subscribed clients
// with per-client session filtering. Also sends agents-count on added/removed.
// Must be called with s.mu held.
func (s *Server) broadcastAgentLifecycle(event conv.WatcherEvent) {
	agentName := ""
	if event.Agent != nil {
		agentName = event.Agent.Name
	}

	var msg serverMessage
	switch event.Type {
	case "agent-added":
		msg = serverMessage{Type: "agent-added", Agent: event.Agent}
	case "agent-removed":
		msg = serverMessage{Type: "agent-removed"}
		if event.Agent != nil {
			msg.Name = event.Agent.Name
		}
	case "agent-updated":
		msg = serverMessage{Type: "agent-updated", Agent: event.Agent}
	}

	// agents-count event on added/removed (total changed)
	sendCount := event.Type != "agent-updated"
	var countMsg serverMessage
	if sendCount {
		total := s.registry.Count()
		countMsg = serverMessage{Type: "agents-count", TotalAgents: &total}
	}

	for c := range s.clients {
		c.mu.Lock()
		subscribed := c.subscribedAgents
		include := c.includeSessionFilter
		exclude := c.excludeSessionFilter
		c.mu.Unlock()

		if !subscribed {
			continue
		}

		if sendCount {
			c.sendJSON(countMsg)
		}
		if wsbase.PassesFilter(agentName, include, exclude) {
			c.sendJSON(msg)
		}
	}
}

func (s *Server) addClient(c *Client) {
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()
}

func (s *Server) removeClient(c *Client) {
	s.mu.Lock()
	delete(s.clients, c)
	s.mu.Unlock()
	c.cleanup()
}

// outMsg wraps a WebSocket message with its type (text or binary).
type outMsg struct {
	typ  websocket.MessageType
	data []byte
}

// pendingConvSub tracks a subscribe-conversation request waiting for tailing to start.
type pendingConvSub struct {
	msgID     string
	agentName string
	filter    *clientFilter
	timer     *time.Timer
}

// Client represents a connected WebSocket client.
type Client struct {
	conn   *websocket.Conn
	server *Server
	send   chan outMsg
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex

	subs    map[string]*subscription // subscriptionId → subscription
	follows map[string]*subscription // agentName → subscription (follow-agent)
	nextSub int

	subscribedAgents     bool
	includeSessionFilter *regexp.Regexp // nil = match all
	excludeSessionFilter *regexp.Regexp // nil = exclude none
	handshakeDone        bool

	pendingConvSubs map[string]*pendingConvSub // conversationID → pending sub
}

type subscription struct {
	id             string
	conversationID string
	agentName      string // non-empty for follow-agent and subscribe-conversation with tailing
	bufSubID       int    // buffer subscription ID for Unsubscribe
	filter         conv.EventFilter
	live           <-chan conv.ConversationEvent
	cancel         context.CancelFunc
}

func newClient(conn *websocket.Conn, server *Server) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		conn:            conn,
		server:          server,
		send:            make(chan outMsg, 256),
		ctx:             ctx,
		cancel:          cancel,
		subs:            make(map[string]*subscription),
		follows:         make(map[string]*subscription),
		pendingConvSubs: make(map[string]*pendingConvSub),
	}
}

func (c *Client) run() {
	go c.writePump()
	c.readPump()
}

func (c *Client) readPump() {
	defer c.cancel()
	for {
		typ, data, err := c.conn.Read(c.ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageBinary {
			c.handleBinaryMessage(data)
			continue
		}
		c.handleTextMessage(data)
	}
}

func (c *Client) writePump() {
	defer func() { _ = c.conn.Close(websocket.StatusNormalClosure, "") }()
	for {
		select {
		case <-c.ctx.Done():
			return
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
			err := c.conn.Write(ctx, msg.typ, msg.data)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func (c *Client) sendJSON(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("wsconv: failed to marshal message: %v", err)
		return
	}
	select {
	case c.send <- outMsg{typ: websocket.MessageText, data: data}:
	default:
		log.Printf("dropping text message for slow client")
	}
}

func (c *Client) handleBinaryMessage(data []byte) {
	msgType, agentName, payload, err := agentio.ParseBinaryEnvelope(data)
	if err != nil {
		c.sendJSON(serverMessage{Type: "error", Error: "invalid binary message: " + err.Error()})
		return
	}

	switch msgType {
	case agentio.BinaryFileUpload:
		payloadCopy := append([]byte(nil), payload...)
		go func() {
			lock := c.server.prompter.GetLock(agentName)
			lock.Lock()
			defer lock.Unlock()
			if err := c.server.prompter.HandleFileUpload(agentName, payloadCopy); err != nil {
				log.Printf("file upload %s error: %v", agentName, err)
				c.sendJSON(serverMessage{Type: "error", Error: "file upload " + agentName + ": " + err.Error()})
			}
		}()
	default:
		c.sendJSON(serverMessage{Type: "error", Error: fmt.Sprintf("unsupported binary message type: 0x%02x", msgType)})
	}
}

func (c *Client) handleTextMessage(data []byte) {
	var msg clientMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		c.sendJSON(serverMessage{Type: "error", Error: "invalid JSON"})
		return
	}

	if !c.handshakeDone {
		if msg.Type != "hello" {
			c.sendJSON(serverMessage{ID: msg.ID, Type: "error", Error: "handshake required: send hello first"})
			return
		}
		c.handleHello(msg)
		return
	}

	switch msg.Type {
	case "hello":
		c.sendJSON(serverMessage{ID: msg.ID, Type: "error", Error: "already handshaked"})
	case "list-agents":
		c.handleListAgents(msg)
	case "subscribe-agents":
		c.handleSubscribeAgents(msg)
	case "list-conversations":
		c.handleListConversations(msg)
	case "subscribe-conversation":
		c.handleSubscribeConversation(msg)
	case "follow-agent":
		c.handleFollowAgent(msg)
	case "unsubscribe":
		c.handleUnsubscribe(msg)
	case "unsubscribe-agent":
		c.handleUnsubscribeAgent(msg)
	case "send-prompt":
		c.handleSendPrompt(msg)
	default:
		c.sendJSON(serverMessage{ID: msg.ID, Type: "error", Error: "unknown message type", UnknownType: msg.Type})
	}
}

func (c *Client) handleHello(msg clientMessage) {
	if msg.Protocol != "tmux-converter.v1" {
		c.sendJSON(serverMessage{ID: msg.ID, Type: "hello", OK: boolPtr(false), Error: "unsupported protocol version"})
		return
	}
	c.handshakeDone = true
	c.sendJSON(serverMessage{ID: msg.ID, Type: "hello", OK: boolPtr(true), Protocol: "tmux-converter.v1", ServerVersion: "0.1.0"})
}

func (c *Client) handleListAgents(msg clientMessage) {
	// Ephemeral filter — does NOT update stored broadcast filter
	include, exclude, err := wsbase.CompileSessionFilters(msg.IncludeSessionFilter, msg.ExcludeSessionFilter)
	if err != nil {
		ok := false
		c.sendJSON(serverMessage{ID: msg.ID, Type: "list-agents", OK: &ok, Error: err.Error()})
		return
	}

	regAgents := c.buildAgentList(include, exclude)
	c.sendJSON(serverMessage{ID: msg.ID, Type: "list-agents", Agents: regAgents})
}

func (c *Client) handleSubscribeAgents(msg clientMessage) {
	// Persistent filter — stored on client, applied to all future broadcasts
	include, exclude, err := wsbase.CompileSessionFilters(msg.IncludeSessionFilter, msg.ExcludeSessionFilter)
	if err != nil {
		ok := false
		c.sendJSON(serverMessage{ID: msg.ID, Type: "subscribe-agents", OK: &ok, Error: err.Error()})
		return
	}

	c.mu.Lock()
	c.subscribedAgents = true
	c.includeSessionFilter = include
	c.excludeSessionFilter = exclude
	c.mu.Unlock()

	regAgents := c.buildAgentList(include, exclude)
	total := c.server.registry.Count()
	c.sendJSON(serverMessage{
		ID:          msg.ID,
		Type:        "subscribe-agents",
		OK:          boolPtr(true),
		Agents:      regAgents,
		TotalAgents: &total,
	})
}

func (c *Client) buildAgentList(include, exclude *regexp.Regexp) []agentInfo {
	allAgents := c.server.watcher.ListAgents()
	result := make([]agentInfo, 0, len(allAgents))
	for _, a := range allAgents {
		if !wsbase.PassesFilter(a.Name, include, exclude) {
			continue
		}
		info := agentInfo{
			Name:     a.Name,
			Runtime:  a.Runtime,
			WorkDir:  a.WorkDir,
			Attached: a.Attached,
		}
		// Attach active conversation ID if one exists
		if convID := c.server.watcher.GetActiveConversation(a.Name); convID != "" {
			info.ConversationID = convID
		}
		result = append(result, info)
	}
	return result
}

func (c *Client) handleListConversations(msg clientMessage) {
	convs := c.server.watcher.ListConversations()
	c.sendJSON(serverMessage{ID: msg.ID, Type: "list-conversations", Conversations: convs})
}

func (c *Client) handleSubscribeConversation(msg clientMessage) {
	if msg.ConversationID == "" {
		c.sendJSON(serverMessage{ID: msg.ID, Type: "error", Error: "conversationId required"})
		return
	}

	// Extract agent name from conversationID format "runtime:agentName:nativeId"
	agentName := extractAgentFromConvID(msg.ConversationID)
	if agentName == "" {
		// Fallback: check watcher's convToAgent map
		agentName = c.server.watcher.GetAgentForConversation(msg.ConversationID)
	}

	if agentName != "" {
		if err := c.server.watcher.EnsureTailing(agentName); err != nil {
			c.sendJSON(serverMessage{ID: msg.ID, Type: "error", Error: err.Error()})
			return
		}
	}

	buf := c.server.watcher.GetBuffer(msg.ConversationID)
	if buf == nil && agentName != "" {
		// Buffer not ready yet (EnsureTailing is async) — create pending subscription
		c.mu.Lock()
		if _, exists := c.pendingConvSubs[msg.ConversationID]; exists {
			c.mu.Unlock()
			c.sendJSON(serverMessage{ID: msg.ID, Type: "error", Error: "already pending subscription for this conversation"})
			return
		}
		pending := &pendingConvSub{
			msgID:     msg.ID,
			agentName: agentName,
			filter:    msg.Filter,
		}
		pending.timer = time.AfterFunc(30*time.Second, func() {
			c.mu.Lock()
			p, ok := c.pendingConvSubs[msg.ConversationID]
			if !ok {
				c.mu.Unlock()
				return // already resolved
			}
			delete(c.pendingConvSubs, msg.ConversationID)
			c.mu.Unlock()
			c.server.watcher.ReleaseTailing(p.agentName)
			c.sendJSON(serverMessage{ID: p.msgID, Type: "error", Error: "conversation not found within timeout"})
		})
		c.pendingConvSubs[msg.ConversationID] = pending
		c.mu.Unlock()
		return // response sent when subscription binds (or times out)
	}

	if buf == nil {
		c.sendJSON(serverMessage{ID: msg.ID, Type: "error", Error: "conversation not found"})
		return
	}

	filter := buildFilter(msg.Filter)
	snapshot, bufSubID, live := buf.Subscribe(filter)

	c.mu.Lock()
	c.nextSub++
	sID := subID(c.nextSub)
	sub := &subscription{
		id:             sID,
		conversationID: msg.ConversationID,
		agentName:      agentName,
		bufSubID:       bufSubID,
		filter:         filter,
		live:           live,
	}
	c.subs[sID] = sub
	c.mu.Unlock()

	snapshot = capSnapshot(snapshot)
	cursor := makeCursor(msg.ConversationID, snapshot)

	c.sendJSON(serverMessage{
		ID:             msg.ID,
		Type:           "conversation-snapshot",
		SubscriptionID: sID,
		ConversationID: msg.ConversationID,
		Events:         snapshot,
		Cursor:         cursor,
	})

	go c.streamLive(sub, buf)
}

func (c *Client) handleFollowAgent(msg clientMessage) {
	if msg.Agent == "" {
		c.sendJSON(serverMessage{ID: msg.ID, Type: "error", Error: "agent required"})
		return
	}

	// Remove existing follow for this agent (same-agent re-follow: release+reacquire)
	c.mu.Lock()
	if existing, ok := c.follows[msg.Agent]; ok {
		// Release tailing ref for the old follow before reacquiring
		c.server.watcher.ReleaseTailing(msg.Agent)
		delete(c.subs, existing.id)
		if existing.cancel != nil {
			existing.cancel()
		}
		if existing.live != nil {
			oldBuf := c.server.watcher.GetBuffer(existing.conversationID)
			if oldBuf != nil {
				oldBuf.Unsubscribe(existing.bufSubID)
			}
		}
		delete(c.follows, msg.Agent)
	}

	// Start tailing for this agent (ref-counted, idempotent if already active)
	if err := c.server.watcher.EnsureTailing(msg.Agent); err != nil {
		c.mu.Unlock()
		c.sendJSON(serverMessage{ID: msg.ID, Type: "error", Error: err.Error()})
		return
	}

	// Check if this agent's runtime supports conversation streaming
	var convSupported *bool
	if agent, ok := c.server.registry.GetAgent(msg.Agent); ok {
		convSupported = boolPtr(c.server.watcher.HasDiscoverer(agent.Runtime))
	}

	filter := buildFilter(msg.Filter)
	c.nextSub++
	sID := subID(c.nextSub)

	convID := c.server.watcher.GetActiveConversation(msg.Agent)

	if convID == "" {
		// No active conversation yet — register a pending follow
		sub := &subscription{
			id:        sID,
			agentName: msg.Agent,
			filter:    filter,
		}
		c.subs[sID] = sub
		c.follows[msg.Agent] = sub
		c.mu.Unlock()

		c.sendJSON(serverMessage{
			ID:                    msg.ID,
			Type:                  "follow-agent",
			OK:                    boolPtr(true),
			SubscriptionID:        sID,
			ConversationSupported: convSupported,
		})
		return
	}

	buf := c.server.watcher.GetBuffer(convID)
	if buf == nil {
		// Conversation ID exists but buffer doesn't yet — pending follow
		sub := &subscription{
			id:        sID,
			agentName: msg.Agent,
			filter:    filter,
		}
		c.subs[sID] = sub
		c.follows[msg.Agent] = sub
		c.mu.Unlock()

		c.sendJSON(serverMessage{
			ID:                    msg.ID,
			Type:                  "follow-agent",
			OK:                    boolPtr(true),
			SubscriptionID:        sID,
			ConversationSupported: convSupported,
		})
		return
	}

	snapshot, bufSubID, live := buf.Subscribe(filter)
	subCtx, subCancel := context.WithCancel(c.ctx)
	sub := &subscription{
		id:             sID,
		conversationID: convID,
		agentName:      msg.Agent,
		bufSubID:       bufSubID,
		filter:         filter,
		live:           live,
		cancel:         subCancel,
	}
	c.subs[sID] = sub
	c.follows[msg.Agent] = sub
	c.mu.Unlock()

	snapshot = capSnapshot(snapshot)
	cursor := makeCursor(convID, snapshot)

	c.sendJSON(serverMessage{
		ID:                    msg.ID,
		Type:                  "follow-agent",
		OK:                    boolPtr(true),
		SubscriptionID:        sID,
		ConversationID:        convID,
		Events:                snapshot,
		Cursor:                cursor,
		ConversationSupported: convSupported,
	})

	go c.streamLiveWithContext(sub, buf, subCtx)
}

func (c *Client) handleUnsubscribe(msg clientMessage) {
	c.mu.Lock()
	sub, ok := c.subs[msg.SubscriptionID]
	if ok {
		delete(c.subs, msg.SubscriptionID)
		if sub.agentName != "" {
			delete(c.follows, sub.agentName)
		}
		if sub.cancel != nil {
			sub.cancel()
		}
	}
	c.mu.Unlock()

	if ok && sub.bufSubID != 0 {
		buf := c.server.watcher.GetBuffer(sub.conversationID)
		if buf != nil {
			buf.Unsubscribe(sub.bufSubID)
		}
	}

	// Release tailing ref for unsubscribed agent
	if ok && sub.agentName != "" {
		c.server.watcher.ReleaseTailing(sub.agentName)
	}

	c.sendJSON(serverMessage{ID: msg.ID, Type: "unsubscribe", OK: boolPtr(true)})
}

func (c *Client) handleUnsubscribeAgent(msg clientMessage) {
	c.mu.Lock()
	sub, ok := c.follows[msg.Agent]
	if ok {
		delete(c.follows, msg.Agent)
		delete(c.subs, sub.id)
		if sub.cancel != nil {
			sub.cancel()
		}
	}

	// Clean up any pending subscribe-conversation requests for this agent
	var pendingToRelease []string
	for convID, pending := range c.pendingConvSubs {
		if pending.agentName == msg.Agent {
			pending.timer.Stop()
			delete(c.pendingConvSubs, convID)
			pendingToRelease = append(pendingToRelease, pending.agentName)
		}
	}
	c.mu.Unlock()

	if ok && sub.bufSubID != 0 {
		buf := c.server.watcher.GetBuffer(sub.conversationID)
		if buf != nil {
			buf.Unsubscribe(sub.bufSubID)
		}
	}

	// Release tailing ref for unfollowed agent
	if ok && sub.agentName != "" {
		c.server.watcher.ReleaseTailing(sub.agentName)
	}

	// Release tailing refs for cleaned-up pending subscriptions
	for _, name := range pendingToRelease {
		c.server.watcher.ReleaseTailing(name)
	}

	c.sendJSON(serverMessage{ID: msg.ID, Type: "unsubscribe-agent", OK: boolPtr(true)})
}

func (c *Client) handleSendPrompt(msg clientMessage) {
	if msg.Agent == "" {
		c.sendJSON(serverMessage{ID: msg.ID, Type: "error", Error: "agent field required"})
		return
	}
	if msg.Prompt == "" {
		c.sendJSON(serverMessage{ID: msg.ID, Type: "error", Error: "prompt field required"})
		return
	}

	lock := c.server.prompter.GetLock(msg.Agent)
	go func() {
		lock.Lock()
		defer lock.Unlock()

		if err := c.server.prompter.SendPrompt(msg.Agent, msg.Prompt); err != nil {
			c.sendJSON(serverMessage{ID: msg.ID, Type: "send-prompt", OK: boolPtr(false), Error: err.Error()})
			return
		}
		c.sendJSON(serverMessage{ID: msg.ID, Type: "send-prompt", OK: boolPtr(true)})
	}()
}

func (c *Client) deliverConversationEvent(event *conv.ConversationEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, sub := range c.subs {
		if sub.live != nil {
			continue // already delivered via streamLiveWithContext
		}
		if sub.conversationID == event.ConversationID && sub.filter.Matches(*event) {
			cursor := conv.Cursor{
				ConversationID: event.ConversationID,
				Seq:            event.Seq,
				EventID:        event.EventID,
			}
			c.sendJSON(serverMessage{
				Type:           "conversation-event",
				SubscriptionID: sub.id,
				ConversationID: event.ConversationID,
				Event:          event,
				Cursor:         encodeCursor(cursor),
			})
		}
	}
}

func (c *Client) deliverConversationStarted(we conv.WatcherEvent) {
	if we.Agent == nil || we.NewConvID == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// 1. Check pending follow-agent subscriptions
	if sub, ok := c.follows[we.Agent.Name]; ok && sub.conversationID == "" {
		buf := c.server.watcher.GetBuffer(we.NewConvID)
		if buf != nil {
			snapshot, bufSubID, live := buf.Subscribe(sub.filter)
			subCtx, subCancel := context.WithCancel(c.ctx)

			sub.conversationID = we.NewConvID
			sub.bufSubID = bufSubID
			sub.live = live
			sub.cancel = subCancel

			snapshot = capSnapshot(snapshot)
			cursor := makeCursor(we.NewConvID, snapshot)

			c.sendJSON(serverMessage{
				Type:           "conversation-snapshot",
				SubscriptionID: sub.id,
				ConversationID: we.NewConvID,
				Events:         snapshot,
				Cursor:         cursor,
			})

			go c.streamLiveWithContext(sub, buf, subCtx)
		}
	}

	// 2. Check pending subscribe-conversation requests
	if pending, hasPending := c.pendingConvSubs[we.NewConvID]; hasPending {
		pending.timer.Stop()
		delete(c.pendingConvSubs, we.NewConvID)

		buf := c.server.watcher.GetBuffer(we.NewConvID)
		if buf == nil {
			c.sendJSON(serverMessage{ID: pending.msgID, Type: "error", Error: "conversation buffer not available"})
			c.server.watcher.ReleaseTailing(pending.agentName)
			return
		}

		filter := buildFilter(pending.filter)
		snapshot, bufSubID, live := buf.Subscribe(filter)
		c.nextSub++
		sID := subID(c.nextSub)
		subCtx, subCancel := context.WithCancel(c.ctx)
		pendingSub := &subscription{
			id:             sID,
			conversationID: we.NewConvID,
			agentName:      pending.agentName,
			bufSubID:       bufSubID,
			filter:         filter,
			live:           live,
			cancel:         subCancel,
		}
		c.subs[sID] = pendingSub

		snapshot = capSnapshot(snapshot)
		cursor := makeCursor(we.NewConvID, snapshot)

		c.sendJSON(serverMessage{
			ID:             pending.msgID,
			Type:           "conversation-snapshot",
			SubscriptionID: sID,
			ConversationID: we.NewConvID,
			Events:         snapshot,
			Cursor:         cursor,
		})

		go c.streamLiveWithContext(pendingSub, buf, subCtx)
	}
}

func (c *Client) deliverConversationSwitch(we conv.WatcherEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if we.Agent == nil {
		return
	}

	sub, ok := c.follows[we.Agent.Name]
	if !ok {
		return
	}

	// Unsubscribe from old buffer
	if sub.bufSubID != 0 {
		oldBuf := c.server.watcher.GetBuffer(sub.conversationID)
		if oldBuf != nil {
			oldBuf.Unsubscribe(sub.bufSubID)
		}
	}
	if sub.cancel != nil {
		sub.cancel()
	}

	// Send switch message
	c.sendJSON(serverMessage{
		Type:           "conversation-switched",
		SubscriptionID: sub.id,
		Agent:          we.Agent,
		From:           we.OldConvID,
		To:             we.NewConvID,
	})

	// Subscribe to new buffer
	newBuf := c.server.watcher.GetBuffer(we.NewConvID)
	if newBuf == nil {
		return
	}

	snapshot, bufSubID, live := newBuf.Subscribe(sub.filter)
	subCtx, subCancel := context.WithCancel(c.ctx)

	sub.conversationID = we.NewConvID
	sub.bufSubID = bufSubID
	sub.live = live
	sub.cancel = subCancel

	snapshot = capSnapshot(snapshot)
	cursor := makeCursor(we.NewConvID, snapshot)

	c.sendJSON(serverMessage{
		Type:           "conversation-snapshot",
		SubscriptionID: sub.id,
		ConversationID: we.NewConvID,
		Events:         snapshot,
		Cursor:         cursor,
		Reason:         "switch",
	})

	go c.streamLiveWithContext(sub, newBuf, subCtx)
}

func (c *Client) streamLive(sub *subscription, buf *conv.ConversationBuffer) {
	c.streamLiveWithContext(sub, buf, c.ctx)
}

func (c *Client) streamLiveWithContext(sub *subscription, _ *conv.ConversationBuffer, ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-sub.live:
			if !ok {
				return
			}
			cursor := conv.Cursor{
				ConversationID: sub.conversationID,
				Seq:            event.Seq,
				EventID:        event.EventID,
			}
			c.sendJSON(serverMessage{
				Type:           "conversation-event",
				SubscriptionID: sub.id,
				ConversationID: sub.conversationID,
				Event:          &event,
				Cursor:         encodeCursor(cursor),
			})
		}
	}
}

func (c *Client) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Release tailing refs via c.subs ONLY (not c.follows — avoids double-release
	// since follow-agent subscriptions are in both c.follows and c.subs)
	for _, sub := range c.subs {
		if sub.bufSubID != 0 {
			buf := c.server.watcher.GetBuffer(sub.conversationID)
			if buf != nil {
				buf.Unsubscribe(sub.bufSubID)
			}
		}
		if sub.cancel != nil {
			sub.cancel()
		}
		if sub.agentName != "" {
			c.server.watcher.ReleaseTailing(sub.agentName)
		}
	}

	// Release refs for pending conv subs (EnsureTailing was called but never resolved)
	for _, pending := range c.pendingConvSubs {
		if pending.timer != nil {
			pending.timer.Stop()
		}
		if pending.agentName != "" {
			c.server.watcher.ReleaseTailing(pending.agentName)
		}
	}

	c.subs = nil
	c.follows = nil
	c.pendingConvSubs = nil
}

// Helper types and functions

type clientMessage struct {
	ID                   string        `json:"id"`
	Type                 string        `json:"type"`
	Protocol             string        `json:"protocol,omitempty"`
	ConversationID       string        `json:"conversationId,omitempty"`
	Agent                string        `json:"agent,omitempty"`
	Prompt               string        `json:"prompt,omitempty"`
	SubscriptionID       string        `json:"subscriptionId,omitempty"`
	Filter               *clientFilter `json:"filter,omitempty"`
	Cursor               string        `json:"cursor,omitempty"`
	IncludeSessionFilter string        `json:"includeSessionFilter,omitempty"`
	ExcludeSessionFilter string        `json:"excludeSessionFilter,omitempty"`
}

type clientFilter struct {
	Types           []string `json:"types,omitempty"`
	ExcludeThinking *bool    `json:"excludeThinking,omitempty"`
	ExcludeProgress *bool    `json:"excludeProgress,omitempty"`
}

type serverMessage struct {
	ID             string                   `json:"id,omitempty"`
	Type           string                   `json:"type"`
	OK             *bool                    `json:"ok,omitempty"`
	Error          string                   `json:"error,omitempty"`
	Protocol       string                   `json:"protocol,omitempty"`
	ServerVersion  string                   `json:"serverVersion,omitempty"`
	UnknownType    string                   `json:"unknownType,omitempty"`
	Agents         []agentInfo              `json:"agents,omitempty"`
	TotalAgents    *int                     `json:"totalAgents,omitempty"`
	Conversations  []conv.ConversationInfo  `json:"conversations,omitempty"`
	SubscriptionID string                   `json:"subscriptionId,omitempty"`
	ConversationID string                   `json:"conversationId,omitempty"`
	Events         []conv.ConversationEvent `json:"events,omitempty"`
	Event          *conv.ConversationEvent  `json:"event,omitempty"`
	Cursor         string                   `json:"cursor,omitempty"`
	Agent          any                      `json:"agent,omitempty"`
	Name           string                   `json:"name,omitempty"`
	From           string                   `json:"from,omitempty"`
	To             string                   `json:"to,omitempty"`
	Reason                string                   `json:"reason,omitempty"`
	ConversationSupported *bool                    `json:"conversationSupported,omitempty"`
}

type agentInfo struct {
	Name           string `json:"name"`
	Runtime        string `json:"runtime"`
	ConversationID string `json:"conversationId,omitempty"`
	WorkDir        string `json:"workDir"`
	Attached       bool   `json:"attached"`
}

func buildFilter(cf *clientFilter) conv.EventFilter {
	if cf == nil {
		return conv.EventFilter{}
	}
	filter := conv.EventFilter{}
	if len(cf.Types) > 0 {
		filter.Types = make(map[string]bool)
		for _, t := range cf.Types {
			filter.Types[t] = true
		}
	}
	if cf.ExcludeThinking != nil {
		filter.ExcludeThinking = *cf.ExcludeThinking
	}
	if cf.ExcludeProgress != nil {
		filter.ExcludeProgress = *cf.ExcludeProgress
	}
	return filter
}

// extractAgentFromConvID parses the conversation ID format "runtime:agentName:nativeId"
// to extract the agent name. Returns "" if the format is not recognized or the runtime
// prefix is unknown.
func extractAgentFromConvID(convID string) string {
	parts := strings.SplitN(convID, ":", 3)
	if len(parts) < 3 {
		return "" // not the standard 3-part format
	}
	if !slices.Contains(agents.RuntimePriority, parts[0]) {
		return "" // unknown runtime prefix
	}
	return parts[1]
}


func subID(n int) string {
	return "sub-" + strconv.Itoa(n)
}

func capSnapshot(events []conv.ConversationEvent) []conv.ConversationEvent {
	if len(events) > maxSnapshotEvents {
		return events[len(events)-maxSnapshotEvents:]
	}
	return events
}

func makeCursor(convID string, events []conv.ConversationEvent) string {
	if len(events) == 0 {
		return ""
	}
	last := events[len(events)-1]
	c := conv.Cursor{
		ConversationID: convID,
		Seq:            last.Seq,
		EventID:        last.EventID,
	}
	return encodeCursor(c)
}

func encodeCursor(c conv.Cursor) string {
	data, err := json.Marshal(c)
	if err != nil {
		log.Printf("wsconv: failed to marshal cursor: %v", err)
		return ""
	}
	return string(data)
}

func boolPtr(b bool) *bool {
	return &b
}
