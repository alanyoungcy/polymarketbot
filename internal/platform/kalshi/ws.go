package kalshi

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// kalshiWriteWait is the time allowed to write a message to the peer.
	kalshiWriteWait = 10 * time.Second

	// kalshiPongWait is the time allowed to read the next pong message.
	kalshiPongWait = 30 * time.Second

	// kalshiPingPeriod sends pings at this interval. Must be less than pongWait.
	kalshiPingPeriod = (kalshiPongWait * 9) / 10

	// kalshiReconnectDelay is the base delay before attempting to reconnect.
	kalshiReconnectDelay = 2 * time.Second

	// kalshiMaxReconnectDelay caps the exponential backoff.
	kalshiMaxReconnectDelay = 60 * time.Second
)

// OrderbookHandler is called when an orderbook update is received via WebSocket.
type OrderbookHandler func(KalshiOrderbook)

// WSClient is a WebSocket client for real-time Kalshi market data.
type WSClient struct {
	wsURL string
	conn  *websocket.Conn

	mu     sync.RWMutex
	closed bool

	// Tracked subscriptions for reconnection.
	subscribedTickers []string
	cmdID             int64

	// Handlers
	orderbookHandlers []OrderbookHandler
	handlerMu         sync.RWMutex

	// done is closed when the client shuts down.
	done chan struct{}
}

// NewWSClient creates a new Kalshi WebSocket client.
//
// wsURL is the WebSocket endpoint, e.g. "wss://api.elections.kalshi.com/trade-api/ws/v2".
func NewWSClient(wsURL string) *WSClient {
	return &WSClient{
		wsURL: wsURL,
		done:  make(chan struct{}),
	}
}

// Connect establishes a WebSocket connection to the Kalshi WebSocket API.
func (w *WSClient) Connect(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return fmt.Errorf("kalshi/ws: client is closed")
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, w.wsURL, nil)
	if err != nil {
		return fmt.Errorf("kalshi/ws: connect: %w", err)
	}

	w.conn = conn

	// Configure read deadline and pong handler.
	w.conn.SetReadDeadline(time.Now().Add(kalshiPongWait))
	w.conn.SetPongHandler(func(string) error {
		w.conn.SetReadDeadline(time.Now().Add(kalshiPongWait))
		return nil
	})

	// Start background loops.
	go w.readLoop()
	go w.pingLoop()

	// Re-subscribe to any previously tracked tickers.
	if len(w.subscribedTickers) > 0 {
		if err := w.sendSubscribe(w.subscribedTickers); err != nil {
			return fmt.Errorf("kalshi/ws: restore subscriptions: %w", err)
		}
	}

	return nil
}

// Subscribe subscribes to orderbook updates for the given market tickers.
func (w *WSClient) Subscribe(ctx context.Context, tickers []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.conn == nil {
		return fmt.Errorf("kalshi/ws: not connected")
	}

	if err := w.sendSubscribe(tickers); err != nil {
		return fmt.Errorf("kalshi/ws: subscribe: %w", err)
	}

	// Track subscriptions for reconnection.
	existing := make(map[string]struct{}, len(w.subscribedTickers))
	for _, t := range w.subscribedTickers {
		existing[t] = struct{}{}
	}
	for _, t := range tickers {
		if _, ok := existing[t]; !ok {
			w.subscribedTickers = append(w.subscribedTickers, t)
		}
	}

	return nil
}

// OnOrderbook registers a handler that is called for every orderbook update.
func (w *WSClient) OnOrderbook(handler func(KalshiOrderbook)) {
	w.handlerMu.Lock()
	defer w.handlerMu.Unlock()
	w.orderbookHandlers = append(w.orderbookHandlers, handler)
}

// Close shuts down the WebSocket connection.
func (w *WSClient) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}

	w.closed = true
	close(w.done)

	if w.conn != nil {
		_ = w.conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		)
		return w.conn.Close()
	}

	return nil
}

// --------------------------------------------------------------------------
// Internal methods
// --------------------------------------------------------------------------

// sendSubscribe sends a subscribe command. Caller must hold w.mu.
func (w *WSClient) sendSubscribe(tickers []string) error {
	w.cmdID++

	cmd := KalshiWSSubscribeCmd{
		ID:  w.cmdID,
		Cmd: "subscribe",
		Params: KalshiWSSubscribeParams{
			Channels: []string{"orderbook_delta"},
			Tickers:  tickers,
		},
	}

	w.conn.SetWriteDeadline(time.Now().Add(kalshiWriteWait))

	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal subscribe: %w", err)
	}

	return w.conn.WriteMessage(websocket.TextMessage, data)
}

// readLoop continuously reads messages from the WebSocket and dispatches
// them to handlers. On disconnect it attempts reconnection.
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
			select {
			case <-w.done:
				return
			default:
			}

			w.reconnect()
			return
		}

		w.handleMessage(message)
	}
}

// pingLoop sends periodic pings to keep the connection alive.
func (w *WSClient) pingLoop() {
	ticker := time.NewTicker(kalshiPingPeriod)
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

			conn.SetWriteDeadline(time.Now().Add(kalshiWriteWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleMessage parses a raw WebSocket message and routes it.
func (w *WSClient) handleMessage(raw []byte) {
	var envelope KalshiWSMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return
	}

	switch envelope.Type {
	case "orderbook_snapshot", "orderbook_delta":
		var ob KalshiWSOrderbook
		if err := json.Unmarshal(envelope.Msg, &ob); err != nil {
			return
		}

		orderbook := ob.ToOrderbook()

		w.handlerMu.RLock()
		handlers := w.orderbookHandlers
		w.handlerMu.RUnlock()

		for _, h := range handlers {
			h(orderbook)
		}
	}
}

// reconnect attempts to re-establish the WebSocket connection with
// exponential backoff.
func (w *WSClient) reconnect() {
	delay := kalshiReconnectDelay

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

		delay *= 2
		if delay > kalshiMaxReconnectDelay {
			delay = kalshiMaxReconnectDelay
		}
	}
}
