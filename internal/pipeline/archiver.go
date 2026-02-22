package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// Archiver moves old data from the database to S3 cold storage.
type Archiver struct {
	blobArchiver  domain.Archiver
	retentionDays int
	logger        *slog.Logger
}

// NewArchiver creates a new Archiver.
func NewArchiver(blobArchiver domain.Archiver, retentionDays int, logger *slog.Logger) *Archiver {
	return &Archiver{
		blobArchiver:  blobArchiver,
		retentionDays: retentionDays,
		logger:        logger,
	}
}

// Run executes a single archive run. It calculates the cutoff time based on
// retentionDays and archives trades, orders, and arb history older than the
// cutoff.
func (a *Archiver) Run(ctx context.Context) error {
	cutoff := time.Now().UTC().Add(-time.Duration(a.retentionDays) * 24 * time.Hour)
	a.logger.Info("starting archive run",
		slog.Time("cutoff", cutoff),
		slog.Int("retention_days", a.retentionDays),
	)

	tradesArchived, err := a.blobArchiver.ArchiveTrades(ctx, cutoff)
	if err != nil {
		return fmt.Errorf("archiving trades before %v: %w", cutoff, err)
	}
	a.logger.Info("archived trades", slog.Int64("count", tradesArchived))

	ordersArchived, err := a.blobArchiver.ArchiveOrders(ctx, cutoff)
	if err != nil {
		return fmt.Errorf("archiving orders before %v: %w", cutoff, err)
	}
	a.logger.Info("archived orders", slog.Int64("count", ordersArchived))

	arbArchived, err := a.blobArchiver.ArchiveArbHistory(ctx, cutoff)
	if err != nil {
		return fmt.Errorf("archiving arb history before %v: %w", cutoff, err)
	}
	a.logger.Info("archived arb history", slog.Int64("count", arbArchived))

	a.logger.Info("archive run complete",
		slog.Int64("trades_archived", tradesArchived),
		slog.Int64("orders_archived", ordersArchived),
		slog.Int64("arb_archived", arbArchived),
	)

	return nil
}

// RunCron runs the archiver on a cron schedule until the context is cancelled.
// It supports cron expressions in the standard 5-field format:
// "minute hour day-of-month month day-of-week"
//
// Example: "0 3 1 * *" runs at 3:00 AM on the 1st of every month.
func (a *Archiver) RunCron(ctx context.Context, cronExpr string) error {
	a.logger.Info("archiver cron started", slog.String("cron", cronExpr))

	for {
		next, err := nextCronTime(cronExpr, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("parsing cron expression %q: %w", cronExpr, err)
		}

		waitDuration := time.Until(next)
		a.logger.Info("archiver waiting for next cron trigger",
			slog.Time("next_run", next),
			slog.Duration("wait", waitDuration),
		)

		timer := time.NewTimer(waitDuration)
		select {
		case <-ctx.Done():
			timer.Stop()
			a.logger.Info("archiver cron stopped")
			return ctx.Err()
		case <-timer.C:
			if err := a.Run(ctx); err != nil {
				a.logger.Error("archive run failed", slog.String("error", err.Error()))
			}
		}
	}
}

// cronField represents a parsed cron field that can match against a value.
type cronField struct {
	wildcard bool
	values   []int
}

// matches returns true if the given value matches this cron field.
func (f cronField) matches(val int) bool {
	if f.wildcard {
		return true
	}
	for _, v := range f.values {
		if v == val {
			return true
		}
	}
	return false
}

// parseCronField parses a single cron field (e.g. "0", "*", "1,15").
func parseCronField(field string) (cronField, error) {
	if field == "*" {
		return cronField{wildcard: true}, nil
	}

	parts := strings.Split(field, ",")
	values := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		v, err := strconv.Atoi(p)
		if err != nil {
			return cronField{}, fmt.Errorf("invalid cron field value %q: %w", p, err)
		}
		values = append(values, v)
	}
	return cronField{values: values}, nil
}

// parsedCron holds five parsed cron fields.
type parsedCron struct {
	minute     cronField
	hour       cronField
	dayOfMonth cronField
	month      cronField
	dayOfWeek  cronField
}

// matchesTime returns true if the given time matches all five cron fields.
func (c parsedCron) matchesTime(t time.Time) bool {
	return c.minute.matches(t.Minute()) &&
		c.hour.matches(t.Hour()) &&
		c.dayOfMonth.matches(t.Day()) &&
		c.month.matches(int(t.Month())) &&
		c.dayOfWeek.matches(int(t.Weekday()))
}

// parseCron parses a 5-field cron expression into a parsedCron struct.
func parseCron(expr string) (parsedCron, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return parsedCron{}, fmt.Errorf("cron expression must have 5 fields, got %d", len(fields))
	}

	minute, err := parseCronField(fields[0])
	if err != nil {
		return parsedCron{}, fmt.Errorf("parsing minute field: %w", err)
	}
	hour, err := parseCronField(fields[1])
	if err != nil {
		return parsedCron{}, fmt.Errorf("parsing hour field: %w", err)
	}
	dayOfMonth, err := parseCronField(fields[2])
	if err != nil {
		return parsedCron{}, fmt.Errorf("parsing day-of-month field: %w", err)
	}
	month, err := parseCronField(fields[3])
	if err != nil {
		return parsedCron{}, fmt.Errorf("parsing month field: %w", err)
	}
	dayOfWeek, err := parseCronField(fields[4])
	if err != nil {
		return parsedCron{}, fmt.Errorf("parsing day-of-week field: %w", err)
	}

	return parsedCron{
		minute:     minute,
		hour:       hour,
		dayOfMonth: dayOfMonth,
		month:      month,
		dayOfWeek:  dayOfWeek,
	}, nil
}

// nextCronTime calculates the next time after 'after' that matches the given
// cron expression. It searches minute-by-minute up to one year ahead.
func nextCronTime(cronExpr string, after time.Time) (time.Time, error) {
	cron, err := parseCron(cronExpr)
	if err != nil {
		return time.Time{}, err
	}

	// Start from the next minute boundary.
	candidate := after.Truncate(time.Minute).Add(time.Minute)

	// Search up to one year ahead to avoid infinite loops.
	limit := after.Add(366 * 24 * time.Hour)

	for candidate.Before(limit) {
		if cron.matchesTime(candidate) {
			return candidate, nil
		}
		candidate = candidate.Add(time.Minute)
	}

	return time.Time{}, fmt.Errorf("no matching cron time found within one year for %q", cronExpr)
}
