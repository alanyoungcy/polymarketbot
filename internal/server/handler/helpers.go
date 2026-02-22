package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// writeJSON marshals v as JSON and writes it to the response with the given
// HTTP status code. If marshaling fails, it falls back to a plain-text 500.
func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write(data)
}

// writeError sends a JSON-formatted error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// parseListOpts extracts standard pagination parameters from the query string.
// Defaults: limit=50 (max 500), offset=0.
func parseListOpts(r *http.Request) domain.ListOpts {
	q := r.URL.Query()

	limit := 50
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 500 {
		limit = 500
	}

	offset := 0
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	return domain.ListOpts{
		Limit:  limit,
		Offset: offset,
	}
}

// pathParam extracts a named path parameter from the request using Go 1.22+
// built-in routing (http.Request.PathValue).
func pathParam(r *http.Request, name string) string {
	return r.PathValue(name)
}

// logHandler is a convenience to attach slog fields in handler code.
func logHandler(logger *slog.Logger, handler string) *slog.Logger {
	return logger.With(slog.String("handler", handler))
}
