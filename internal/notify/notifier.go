// Package notify provides a multi-channel notification system. Notifications
// are dispatched to all registered senders (Telegram, Discord, etc.) and can be
// filtered by event type so operators receive only the alerts they care about.
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// Sender is the interface that each notification channel must implement.
type Sender interface {
	// Send delivers a notification with the given title and message body.
	Send(ctx context.Context, title, message string) error
	// Name returns a human-readable identifier for the sender (e.g. "telegram").
	Name() string
}

// Notifier dispatches notifications to one or more Senders. It maintains a set
// of allowed event types; Notify only forwards messages whose event type is in
// the allowed set, while NotifyAll bypasses the filter.
type Notifier struct {
	senders []Sender
	events  map[string]bool // allowed event types
	logger  *slog.Logger
}

// NewNotifier creates a Notifier that will deliver to the given senders. Only
// events whose type appears in the events slice will be forwarded by Notify.
// If events is empty, all event types are allowed.
func NewNotifier(senders []Sender, events []string, logger *slog.Logger) *Notifier {
	allowed := make(map[string]bool, len(events))
	for _, e := range events {
		allowed[strings.TrimSpace(e)] = true
	}
	return &Notifier{
		senders: senders,
		events:  allowed,
		logger:  logger.With(slog.String("component", "notifier")),
	}
}

// Notify sends a notification to all senders only if the event type is in the
// allowed list. If no events were configured (empty list), all events pass.
func (n *Notifier) Notify(ctx context.Context, event, title, message string) error {
	// If specific events were configured, filter.
	if len(n.events) > 0 && !n.events[event] {
		n.logger.DebugContext(ctx, "event filtered out",
			slog.String("event", event),
		)
		return nil
	}

	return n.dispatch(ctx, title, message)
}

// NotifyAll sends a notification to all senders regardless of event type.
func (n *Notifier) NotifyAll(ctx context.Context, title, message string) error {
	return n.dispatch(ctx, title, message)
}

// dispatch iterates over all senders and sends the notification. Errors from
// individual senders are collected and returned as a combined error; a single
// sender failure does not prevent delivery to the remaining senders.
func (n *Notifier) dispatch(ctx context.Context, title, message string) error {
	if len(n.senders) == 0 {
		return nil
	}

	var errs []string
	for _, s := range n.senders {
		if err := s.Send(ctx, title, message); err != nil {
			n.logger.ErrorContext(ctx, "sender failed",
				slog.String("sender", s.Name()),
				slog.String("error", err.Error()),
			)
			errs = append(errs, fmt.Sprintf("%s: %v", s.Name(), err))
		} else {
			n.logger.DebugContext(ctx, "notification sent",
				slog.String("sender", s.Name()),
				slog.String("title", title),
			)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("notify: %d sender(s) failed: %s", len(errs), strings.Join(errs, "; "))
	}
	return nil
}
