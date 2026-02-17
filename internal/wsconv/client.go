package wsconv

import (
	"context"
	"encoding/json"
	"log"
	"regexp"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/gastownhall/tmux-adapter/internal/conv"
)

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
