package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
	"github.com/gorilla/websocket"
)

const (
	// writeWait is the maximum time to wait for a write to complete.
	writeWait = 10 * time.Second

	// pongWait is the maximum time to wait for a pong from the client.
	pongWait = 60 * time.Second

	// pingPeriod sends pings at this interval. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// maxMessageSize is the maximum size of an incoming message.
	maxMessageSize = 4096

	// sendBufferSize is the channel buffer for outgoing messages per client.
	sendBufferSize = 256
)

// defaultChannels are the Redis pub/sub channels that the hub subscribes to.
var defaultChannels = []string{
	"ch:book:*",
	"ch:signal",
	"ch:arb",
	"ch:order",
	"ch:status",
	// Backward-compatible channels used by current services.
	"prices",
	"orders",
	"positions",
	"arb",
	"trades",
	"price_updates",
	"arb_prices",
	"bond_resolved",
}

// upgrader configures the WebSocket upgrade parameters.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// Allow all origins. In production, restrict this to known origins.
		return true
	},
}

// client represents a single WebSocket connection.
type client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
	subs map[string]bool // subscribed channels
	mu   sync.RWMutex
}

// subscribeMsg is the JSON message a client sends to subscribe to channels.
type subscribeMsg struct {
	Action   string   `json:"action"`   // "subscribe" or "unsubscribe"
	Channels []string `json:"channels"` // channel names
	// Compatibility with prior client format:
	// {"subscribe":["ch:book:*","ch:signal"]}
	Subscribe   []string `json:"subscribe"`
	Unsubscribe []string `json:"unsubscribe"`
}

// Hub manages a set of connected WebSocket clients and broadcasts messages
// from the Redis signal bus to all subscribed clients.
type Hub struct {
	clients    map[*client]bool
	broadcast  chan broadcastMsg
	register   chan *client
	unregister chan *client
	bus        domain.SignalBus
	mu         sync.RWMutex
	logger     *slog.Logger
	mode       string
	strategy   string
	startedAt  time.Time
}

// broadcastMsg carries a message along with its source channel so the hub
// can route it only to clients subscribed to that channel.
type broadcastMsg struct {
	channel string
	data    []byte
}

// Config captures runtime metadata used in hub status snapshots sent to
// WebSocket clients on connect.
type Config struct {
	Mode         string
	StrategyName string
	StartedAt    time.Time
}

// NewHub creates a new WebSocket hub that bridges a Redis SignalBus to
// connected WebSocket clients.
func NewHub(bus domain.SignalBus, logger *slog.Logger, cfg Config) *Hub {
	mode := strings.TrimSpace(strings.ToLower(cfg.Mode))
	if mode == "" {
		mode = "unknown"
	}
	strategy := strings.TrimSpace(cfg.StrategyName)
	if strategy == "" {
		strategy = "unknown"
	}
	startedAt := cfg.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}

	return &Hub{
		clients:    make(map[*client]bool),
		broadcast:  make(chan broadcastMsg, 256),
		register:   make(chan *client),
		unregister: make(chan *client),
		bus:        bus,
		logger:     logger,
		mode:       mode,
		strategy:   strategy,
		startedAt:  startedAt,
	}
}

// SetStrategyName updates the strategy name reported in bot_status (e.g. after
// a runtime strategy switch). Safe to call from another goroutine.
func (h *Hub) SetStrategyName(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := strings.TrimSpace(name)
	if n == "" {
		n = "unknown"
	}
	h.strategy = n
}

// Run starts the hub's main event loop. It should be called in a goroutine.
// It handles client registration, unregistration, and message broadcasting.
// The loop exits when the provided context is cancelled.
func (h *Hub) Run(ctx context.Context) error {
	// Start background subscriptions to Redis channels.
	for _, ch := range defaultChannels {
		go h.subscribeToChannel(ctx, ch)
	}

	for {
		select {
		case <-ctx.Done():
			h.mu.Lock()
			for c := range h.clients {
				close(c.send)
				delete(h.clients, c)
			}
			h.mu.Unlock()
			return ctx.Err()

		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = true
			h.mu.Unlock()
			h.logger.Info("ws: client connected",
				slog.Int("total_clients", h.clientCount()),
			)

		case c := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
			h.mu.Unlock()
			h.logger.Info("ws: client disconnected",
				slog.Int("total_clients", h.clientCount()),
			)

		case msg := <-h.broadcast:
			h.mu.RLock()
			for c := range h.clients {
				if c.isSubscribed(msg.channel) {
					select {
					case c.send <- msg.data:
					default:
						// Client's send buffer is full; drop the message.
						h.logger.Warn("ws: dropping message for slow client")
					}
				}
			}
			h.mu.RUnlock()
		}
	}
}

// subscribeToChannel subscribes to a single Redis pub/sub channel and
// forwards received messages to the hub's broadcast channel.
func (h *Hub) subscribeToChannel(ctx context.Context, channel string) {
	msgCh, err := h.bus.Subscribe(ctx, channel)
	if err != nil {
		h.logger.Error("ws: failed to subscribe to channel",
			slog.String("channel", channel),
			slog.String("error", err.Error()),
		)
		return
	}

	h.logger.Info("ws: subscribed to channel", slog.String("channel", channel))

	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-msgCh:
			if !ok {
				h.logger.Warn("ws: channel subscription closed",
					slog.String("channel", channel),
				)
				return
			}
			h.broadcast <- broadcastMsg{
				channel: channel,
				data:    data,
			}
		}
	}
}

// HandleWS upgrades an HTTP request to a WebSocket connection and registers
// the client with the hub.
// GET /ws
func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("ws: upgrade failed", slog.String("error", err.Error()))
		return
	}

	c := &client{
		hub:  h,
		conn: conn,
		send: make(chan []byte, sendBufferSize),
		subs: make(map[string]bool),
	}

	// Subscribe to all default channels initially.
	for _, ch := range defaultChannels {
		c.subs[ch] = true
	}

	h.register <- c
	c.sendInitialStatus()

	// Start read and write pumps in separate goroutines.
	go c.writePump()
	go c.readPump()
}

// clientCount returns the number of currently connected clients.
func (h *Hub) clientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// readPump reads messages from the WebSocket connection. It handles
// subscription management requests (JSON text frames) from the client.
func (c *client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				c.hub.logger.Warn("ws: unexpected close error",
					slog.String("error", err.Error()),
				)
			}
			return
		}

		// Try to parse as a subscription management message.
		var sub subscribeMsg
		if jsonErr := json.Unmarshal(message, &sub); jsonErr == nil &&
			(sub.Action != "" || len(sub.Channels) > 0 || len(sub.Subscribe) > 0 || len(sub.Unsubscribe) > 0) {
			c.handleSubscription(sub)
		}
	}
}

// handleSubscription processes subscribe/unsubscribe requests from the client.
func (c *client) handleSubscription(msg subscribeMsg) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(msg.Subscribe) > 0 {
		for _, ch := range msg.Subscribe {
			c.subs[ch] = true
		}
	}
	if len(msg.Unsubscribe) > 0 {
		for _, ch := range msg.Unsubscribe {
			delete(c.subs, ch)
		}
	}

	switch msg.Action {
	case "subscribe":
		for _, ch := range msg.Channels {
			c.subs[ch] = true
		}
	case "unsubscribe":
		for _, ch := range msg.Channels {
			delete(c.subs, ch)
		}
	}
}

// sendInitialStatus pushes a small JSON envelope so clients can immediately
// mark the connection as healthy even when no market events are flowing yet.
func (c *client) sendInitialStatus() {
	uptime := int64(time.Since(c.hub.startedAt).Seconds())
	if uptime < 0 {
		uptime = 0
	}

	msg, err := json.Marshal(map[string]any{
		"type": "bot_status",
		"payload": map[string]any{
			"mode":           c.hub.mode,
			"ws_connected":   true,
			"uptime_seconds": uptime,
			"open_positions": 0,
			"open_orders":    0,
			"strategy_name":  c.hub.strategy,
		},
	})
	if err != nil {
		return
	}

	select {
	case c.send <- msg:
	default:
	}
}

// isSubscribed checks whether the client is subscribed to the given channel.
func (c *client) isSubscribed(channel string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Direct match.
	if c.subs[channel] {
		return true
	}

	// Wildcard match: "ch:book:*" should match "ch:book:12345".
	for sub := range c.subs {
		if len(sub) > 0 && sub[len(sub)-1] == '*' {
			prefix := sub[:len(sub)-1]
			if len(channel) >= len(prefix) && channel[:len(prefix)] == prefix {
				return true
			}
		}
	}

	return false
}

// writePump pumps messages from the hub to the WebSocket connection.
// It sends protobuf binary frames for data messages and periodic ping
// frames for keepalive.
func (c *client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel.
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			// Send protobuf data as binary frames.
			if err := c.conn.WriteMessage(websocket.BinaryMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
