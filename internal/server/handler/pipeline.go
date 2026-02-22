package handler

import (
	"log/slog"
	"net/http"
	"time"
)

// PipelineHandler serves pipeline trigger endpoints.
type PipelineHandler struct {
	logger    *slog.Logger
	triggerCh chan<- struct{} // when non-nil, sending triggers one pipeline run
}

// NewPipelineHandler creates a PipelineHandler with the given logger.
func NewPipelineHandler(logger *slog.Logger) *PipelineHandler {
	return &PipelineHandler{logger: logger}
}

// WithTriggerChannel sets the channel to send on when a trigger is requested.
// The pipeline loop must receive from this channel to run one cycle.
func (h *PipelineHandler) WithTriggerChannel(ch chan<- struct{}) *PipelineHandler {
	h.triggerCh = ch
	return h
}

// TriggerPipeline enqueues one pipeline run. If a trigger channel is configured,
// a non-blocking send is performed so the pipeline loop runs one cycle.
// POST /api/pipeline/trigger
func (h *PipelineHandler) TriggerPipeline(w http.ResponseWriter, r *http.Request) {
	h.logger.InfoContext(r.Context(), "handler: pipeline trigger requested")
	if h.triggerCh != nil {
		select {
		case h.triggerCh <- struct{}{}:
		default:
			// already triggered and not yet consumed
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":       "accepted",
		"message":      "pipeline trigger enqueued",
		"requested_at": time.Now().UTC().Format(time.RFC3339),
	})
}
