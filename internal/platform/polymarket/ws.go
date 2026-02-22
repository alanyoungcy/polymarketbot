package polymarket

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
	"github.com/gorilla/websocket"
)

const (
	// writeWait is the time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// pongWait is the time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// pingPeriod sends pings to the peer at this interval. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// reconnectDelay is the base delay before attempting to reconnect.
	reconnectDelay = 2 * time.Second

	// maxReconnectDelay caps the exponential backoff for reconnection.
	maxReconnectDelay = 60 * time.Second
)

// BookUpdateHandler is called when a full orderbook snapshot is received.
type BookUpdateHandler func(domain.OrderbookSnapshot)

// PriceChangeHandler is called when an incremental price level update is received.
type PriceChangeHandler func(domain.PriceChange)

// LastTradePriceHandler is called when a last trade price message is received.
type LastTradePriceHandler func(domain.LastTradePrice)

// WSClient is a WebSocket client for the Polymarket CLOB real-time data feed.
// It manages the connection lifecycle, subscriptions, and dispatches messages
// to registered handlers.
type WSClient struct {
	wsURL string
	conn  *websocket.Conn

	mu     sync.RWMutex
	closed bool

	// Subscriptions to restore on reconnect.
	subscriptions []WSCommand

	// Handlers
	bookHandlers      []BookUpdateHandler
	priceHandlers     []PriceChangeHandler
	lastTradeHandlers []LastTradePriceHandler
	handlerMu         sync.RWMutex

	// done is closed when the client is shut down.
	done chan struct{}
}

// NewWSClient creates a new WebSocket client for the given WebSocket URL.
//
// wsURL is the CLOB WebSocket endpoint, e.g. "wss://ws-subscriptions-clob.polymarket.com/ws/market".
func NewWSClient(wsURL string) *WSClient {
	return &WSClient{
		wsURL: wsURL,
		done:  make(chan struct{}),
	}
}

// Connect establishes a WebSocket connection to the Polymarket CLOB WebSocket.
func (w *WSClient) Connect(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return fmt.Errorf("polymarket/ws: %w", domain.ErrWSDisconnect)
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, w.wsURL, nil)
	if err != nil {
		return fmt.Errorf("polymarket/ws: connect: %w", err)
	}

	w.conn = conn

	// Set up pong handler for keep-alive.
	w.conn.SetReadDeadline(time.Now().Add(pongWait))
	w.conn.SetPongHandler(func(string) error {
		w.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	// Start the read loop and ping loop.
	go w.readLoop()
	go w.pingLoop()

	// Restore any previous subscriptions after reconnect.
	for _, cmd := range w.subscriptions {
		if err := w.sendCommand(cmd); err != nil {
			return fmt.Errorf("polymarket/ws: restore subscription: %w", err)
		}
	}

	return nil
}

// Subscribe subscribes to the given channels for the specified asset IDs.
// Valid channels include "book", "price_change", "last_trade_price".
func (w *WSClient) Subscribe(ctx context.Context, channels []string, assetIDs []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.conn == nil {
		return fmt.Errorf("polymarket/ws: not connected")
	}

	for _, ch := range channels {
		cmd := WSCommand{
			Type:    "subscribe",
			Channel: ch,
			Assets:  assetIDs,
		}

		if err := w.sendCommand(cmd); err != nil {
			return fmt.Errorf("polymarket/ws: subscribe to %s: %w", ch, err)
		}

		// Track subscription for reconnection.
		w.subscriptions = append(w.subscriptions, cmd)
	}

	return nil
}

// Unsubscribe unsubscribes from the given channels for the specified asset IDs.
func (w *WSClient) Unsubscribe(ctx context.Context, channels []string, assetIDs []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.conn == nil {
		return fmt.Errorf("polymarket/ws: not connected")
	}

	for _, ch := range channels {
		cmd := WSCommand{
			Type:    "unsubscribe",
			Channel: ch,
			Assets:  assetIDs,
		}

		if err := w.sendCommand(cmd); err != nil {
			return fmt.Errorf("polymarket/ws: unsubscribe from %s: %w", ch, err)
		}
	}

	// Remove matching subscriptions from the tracked list.
	assetSet := make(map[string]struct{}, len(assetIDs))
	for _, a := range assetIDs {
		assetSet[a] = struct{}{}
	}
	channelSet := make(map[string]struct{}, len(channels))
	for _, c := range channels {
		channelSet[c] = struct{}{}
	}

	filtered := w.subscriptions[:0]
	for _, sub := range w.subscriptions {
		if _, chMatch := channelSet[sub.Channel]; chMatch {
			// Remove assets from this subscription.
			remaining := make([]string, 0, len(sub.Assets))
			for _, a := range sub.Assets {
				if _, found := assetSet[a]; !found {
					remaining = append(remaining, a)
				}
			}
			if len(remaining) > 0 {
				sub.Assets = remaining
				filtered = append(filtered, sub)
			}
		} else {
			filtered = append(filtered, sub)
		}
	}
	w.subscriptions = filtered

	return nil
}

// Close shuts down the WebSocket connection and stops the read loop.
func (w *WSClient) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}

	w.closed = true
	close(w.done)

	if w.conn != nil {
		// Send a close message to the server.
		_ = w.conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		)
		return w.conn.Close()
	}

	return nil
}

// OnBookUpdate registers a handler that is called for every full orderbook
// snapshot received on the "book" channel.
func (w *WSClient) OnBookUpdate(handler BookUpdateHandler) {
	w.handlerMu.Lock()
	defer w.handlerMu.Unlock()
	w.bookHandlers = append(w.bookHandlers, handler)
}

// OnPriceChange registers a handler that is called for every incremental
// price level update received on the "price_change" channel.
func (w *WSClient) OnPriceChange(handler PriceChangeHandler) {
	w.handlerMu.Lock()
	defer w.handlerMu.Unlock()
	w.priceHandlers = append(w.priceHandlers, handler)
}

// OnLastTradePrice registers a handler that is called for every last trade
// price message received on the "last_trade_price" channel.
func (w *WSClient) OnLastTradePrice(handler LastTradePriceHandler) {
	w.handlerMu.Lock()
	defer w.handlerMu.Unlock()
	w.lastTradeHandlers = append(w.lastTradeHandlers, handler)
}

// --------------------------------------------------------------------------
// Internal methods
// --------------------------------------------------------------------------

// sendCommand sends a JSON command to the WebSocket. Caller must hold w.mu.
func (w *WSClient) sendCommand(cmd WSCommand) error {
	w.conn.SetWriteDeadline(time.Now().Add(writeWait))

	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal command: %w", err)
	}

	return w.conn.WriteMessage(websocket.TextMessage, data)
}

// readLoop continuously reads messages from the WebSocket and dispatches
// them to the appropriate handlers. It runs in its own goroutine.
// On disconnect, it attempts to reconnect with exponential backoff.
func (w *WSClient) readLoop() {
	defer func() {
		w.mu.RLock()
		conn := w.conn
		w.mu.RUnlock()
		if conn != nil {
			conn.Close()
		}
	}()

	for {
		select {
		case <-w.done:
			return
		default:
		}

		w.mu.RLock()
		conn := w.conn
		w.mu.RUnlock()

		if conn == nil {
			return
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			// Check if we've been shut down.
			select {
			case <-w.done:
				return
			default:
			}

			// Attempt reconnection.
			w.reconnect()
			return // readLoop will be restarted by reconnect -> Connect
		}

		w.handleMessage(message)
	}
}

// pingLoop sends periodic ping messages to keep the WebSocket alive.
func (w *WSClient) pingLoop() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			w.mu.RLock()
			conn := w.conn
			w.mu.RUnlock()

			if conn == nil {
				return
			}

			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleMessage parses a raw WebSocket message and routes it to the
// appropriate handler based on the message type.
func (w *WSClient) handleMessage(raw []byte) {
	// First, try to identify the message type from the outer envelope.
	var envelope struct {
		MsgType string `json:"msg_type"`
		Event   string `json:"event_type"`
	}

	if err := json.Unmarshal(raw, &envelope); err != nil {
		return // Silently drop unparseable messages.
	}

	// Route based on message type.
	msgType := envelope.MsgType
	if msgType == "" {
		msgType = envelope.Event
	}

	switch msgType {
	case "book":
		var book BookMessage
		if err := json.Unmarshal(raw, &book); err != nil {
			return
		}
		snap := BookToDomainSnapshot(&book)

		w.handlerMu.RLock()
		handlers := w.bookHandlers
		w.handlerMu.RUnlock()

		for _, h := range handlers {
			h(snap)
		}

	case "price_change":
		var pc PriceChangeMessage
		if err := json.Unmarshal(raw, &pc); err != nil {
			return
		}
		change := PriceChangeToDomain(&pc)

		w.handlerMu.RLock()
		handlers := w.priceHandlers
		w.handlerMu.RUnlock()

		for _, h := range handlers {
			h(change)
		}

	case "last_trade_price":
		var ltp PriceMessage
		if err := json.Unmarshal(raw, &ltp); err != nil {
			return
		}
		trade := PriceToDomainLastTrade(&ltp)

		w.handlerMu.RLock()
		handlers := w.lastTradeHandlers
		w.handlerMu.RUnlock()

		for _, h := range handlers {
			h(trade)
		}
	}
}

// reconnect attempts to re-establish the WebSocket connection with
// exponential backoff. It blocks until successful or the client is closed.
func (w *WSClient) reconnect() {
	delay := reconnectDelay

	for {
		select {
		case <-w.done:
			return
		default:
		}

		time.Sleep(delay)

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := w.Connect(ctx)
		cancel()

		if err == nil {
			return
		}

		// Exponential backoff.
		delay *= 2
		if delay > maxReconnectDelay {
			delay = maxReconnectDelay
		}
	}
}
