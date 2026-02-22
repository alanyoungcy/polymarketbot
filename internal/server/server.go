package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/server/handler"
	"github.com/alanyoungcy/polymarketbot/internal/server/middleware"
	"github.com/alanyoungcy/polymarketbot/internal/server/ws"
)

// Config holds the HTTP server configuration.
type Config struct {
	Port        int
	CORSOrigins []string
	APIKey      string // if empty, authentication is disabled
}

// Handlers aggregates all HTTP handlers that the server needs to register.
type Handlers struct {
	Health   *handler.HealthHandler
	Markets  *handler.MarketHandler
	Orders   *handler.OrderHandler
	Positions *handler.PositionHandler
	Arb      *handler.ArbHandler
	Strategy *handler.StrategyHandler
	Pipeline *handler.PipelineHandler
}

// Server is the headless HTTP + WebSocket API server for the Polymarket bot.
type Server struct {
	httpServer *http.Server
	mux        *http.ServeMux
	logger     *slog.Logger
}

// NewServer creates a new Server with all routes registered on the ServeMux.
// It wires up middleware (logging, CORS, auth) and attaches the WebSocket hub.
func NewServer(cfg Config, handlers Handlers, wsHub *ws.Hub, logger *slog.Logger) *Server {
	mux := http.NewServeMux()

	// --- Register routes ---

	// Health check (no auth required).
	mux.HandleFunc("GET /api/health", handlers.Health.HealthCheck)

	// Market endpoints.
	mux.HandleFunc("GET /api/markets", handlers.Markets.ListMarkets)
	mux.HandleFunc("GET /api/markets/{id}", handlers.Markets.GetMarket)

	// Order endpoints.
	mux.HandleFunc("GET /api/orders", handlers.Orders.ListOrders)
	mux.HandleFunc("POST /api/orders", handlers.Orders.PlaceOrder)
	mux.HandleFunc("DELETE /api/orders/{id}", handlers.Orders.CancelOrder)

	// Position endpoints.
	mux.HandleFunc("GET /api/positions", handlers.Positions.ListPositions)

	// Arbitrage endpoints.
	mux.HandleFunc("GET /api/arbitrage/recent", handlers.Arb.ListRecent)

	// Strategy config endpoints.
	mux.HandleFunc("GET /api/strategy/config", handlers.Strategy.GetConfig)
	mux.HandleFunc("PUT /api/strategy/config", handlers.Strategy.UpdateConfig)

	// Pipeline trigger endpoint.
	mux.HandleFunc("POST /api/pipeline/trigger", handlers.Pipeline.TriggerPipeline)

	// WebSocket endpoint.
	if wsHub != nil {
		mux.HandleFunc("GET /ws", wsHub.HandleWS)
	}

	// Build the middleware chain.
	var h http.Handler = mux

	// Apply auth middleware (skips if APIKey is empty).
	h = middleware.Auth(cfg.APIKey)(h)

	// Apply request logging middleware.
	h = middleware.Logging(logger)(h)

	// Apply CORS middleware.
	h = corsMiddleware(cfg.CORSOrigins)(h)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      h,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return &Server{
		httpServer: srv,
		mux:        mux,
		logger:     logger,
	}
}

// Start begins listening for HTTP requests. It blocks until the server
// encounters an error or is shut down.
func (s *Server) Start() error {
	s.logger.Info("server: starting",
		slog.String("addr", s.httpServer.Addr),
	)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server: listen: %w", err)
	}
	return nil
}

// Shutdown gracefully shuts down the server, waiting for in-flight requests
// to complete within the given context deadline.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("server: shutting down")
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("server: shutdown: %w", err)
	}
	return nil
}

// corsMiddleware returns middleware that sets CORS headers for the allowed
// origins. If no origins are specified, it defaults to allowing all origins.
func corsMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			if origin != "" {
				allowed := len(allowedOrigins) == 0 // allow all if none specified
				for _, o := range allowedOrigins {
					if strings.EqualFold(o, "*") || strings.EqualFold(o, origin) {
						allowed = true
						break
					}
				}

				if allowed {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
					w.Header().Set("Access-Control-Max-Age", "86400")
				}
			}

			// Handle preflight requests.
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
