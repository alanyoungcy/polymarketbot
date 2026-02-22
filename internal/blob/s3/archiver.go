package s3blob

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// ---------------------------------------------------------------------------
// Narrow store interfaces required by the archiver.
//
// These follow the Interface Segregation Principle: the archiver only
// requires the query methods it actually calls, not the full domain store
// interfaces. Implementations (e.g. the Postgres stores) satisfy these
// implicitly through their existing ListByMarket / ListRecent methods, but
// callers should provide purpose-built adapters that use time-ranged queries.
// ---------------------------------------------------------------------------

// TradeArchiveStore provides read access to trades for archival purposes.
type TradeArchiveStore interface {
	// ListBefore returns all trades with a timestamp strictly before the
	// given cutoff time.
	ListBefore(ctx context.Context, before time.Time) ([]domain.Trade, error)
}

// OrderArchiveStore provides read access to orders for archival purposes.
type OrderArchiveStore interface {
	// ListBefore returns all orders created strictly before the given cutoff
	// time.
	ListBefore(ctx context.Context, before time.Time) ([]domain.Order, error)
}

// ArbArchiveStore provides read access to arbitrage history for archival
// purposes.
type ArbArchiveStore interface {
	// ListBefore returns all arb opportunities detected strictly before the
	// given cutoff time.
	ListBefore(ctx context.Context, before time.Time) ([]domain.ArbOpportunity, error)
}

// ---------------------------------------------------------------------------
// ArchiveImpl
// ---------------------------------------------------------------------------

// ArchiveImpl implements domain.Archiver by querying the domain stores for
// old records, serializing them to JSONL, and uploading the result to S3.
//
// Deletion of the archived records from the primary store is intentionally
// NOT performed here -- that is a separate, explicit step to be executed
// after the archive has been verified.
type ArchiveImpl struct {
	writer domain.BlobWriter
	trades TradeArchiveStore
	orders OrderArchiveStore
	arb    ArbArchiveStore
	audit  domain.AuditStore
}

// NewArchiver creates a new ArchiveImpl.
func NewArchiver(
	writer domain.BlobWriter,
	trades TradeArchiveStore,
	orders OrderArchiveStore,
	arb ArbArchiveStore,
	audit domain.AuditStore,
) *ArchiveImpl {
	return &ArchiveImpl{
		writer: writer,
		trades: trades,
		orders: orders,
		arb:    arb,
		audit:  audit,
	}
}

// ArchiveTrades queries all trades before the cutoff, serializes them to
// JSONL, and uploads the file to S3 at archive/trades/YYYY-MM.jsonl. The
// archival event is recorded in the audit log and the count of archived
// records is returned.
func (a *ArchiveImpl) ArchiveTrades(ctx context.Context, before time.Time) (int64, error) {
	trades, err := a.trades.ListBefore(ctx, before)
	if err != nil {
		return 0, fmt.Errorf("s3blob: archive trades query: %w", err)
	}
	if len(trades) == 0 {
		return 0, nil
	}

	buf, err := marshalJSONL(trades)
	if err != nil {
		return 0, fmt.Errorf("s3blob: archive trades marshal: %w", err)
	}

	path := archivePath("trades", before)
	if err := a.writer.Put(ctx, path, bytes.NewReader(buf), "application/x-ndjson"); err != nil {
		return 0, fmt.Errorf("s3blob: archive trades upload: %w", err)
	}

	count := int64(len(trades))

	if err := a.audit.Log(ctx, "archive.trades", map[string]any{
		"path":   path,
		"count":  count,
		"before": before.Format(time.RFC3339),
	}); err != nil {
		return count, fmt.Errorf("s3blob: archive trades audit log: %w", err)
	}

	return count, nil
}

// ArchiveOrders queries all orders before the cutoff, serializes them to
// JSONL, and uploads the file to S3 at archive/orders/YYYY-MM.jsonl. The
// archival event is recorded in the audit log and the count of archived
// records is returned.
func (a *ArchiveImpl) ArchiveOrders(ctx context.Context, before time.Time) (int64, error) {
	orders, err := a.orders.ListBefore(ctx, before)
	if err != nil {
		return 0, fmt.Errorf("s3blob: archive orders query: %w", err)
	}
	if len(orders) == 0 {
		return 0, nil
	}

	buf, err := marshalJSONL(orders)
	if err != nil {
		return 0, fmt.Errorf("s3blob: archive orders marshal: %w", err)
	}

	path := archivePath("orders", before)
	if err := a.writer.Put(ctx, path, bytes.NewReader(buf), "application/x-ndjson"); err != nil {
		return 0, fmt.Errorf("s3blob: archive orders upload: %w", err)
	}

	count := int64(len(orders))

	if err := a.audit.Log(ctx, "archive.orders", map[string]any{
		"path":   path,
		"count":  count,
		"before": before.Format(time.RFC3339),
	}); err != nil {
		return count, fmt.Errorf("s3blob: archive orders audit log: %w", err)
	}

	return count, nil
}

// ArchiveArbHistory queries all arbitrage opportunities before the cutoff,
// serializes them to JSONL, and uploads the file to S3 at
// archive/arb_history/YYYY-MM.jsonl. The archival event is recorded in the
// audit log and the count of archived records is returned.
func (a *ArchiveImpl) ArchiveArbHistory(ctx context.Context, before time.Time) (int64, error) {
	opps, err := a.arb.ListBefore(ctx, before)
	if err != nil {
		return 0, fmt.Errorf("s3blob: archive arb history query: %w", err)
	}
	if len(opps) == 0 {
		return 0, nil
	}

	buf, err := marshalJSONL(opps)
	if err != nil {
		return 0, fmt.Errorf("s3blob: archive arb history marshal: %w", err)
	}

	path := archivePath("arb_history", before)
	if err := a.writer.Put(ctx, path, bytes.NewReader(buf), "application/x-ndjson"); err != nil {
		return 0, fmt.Errorf("s3blob: archive arb history upload: %w", err)
	}

	count := int64(len(opps))

	if err := a.audit.Log(ctx, "archive.arb_history", map[string]any{
		"path":   path,
		"count":  count,
		"before": before.Format(time.RFC3339),
	}); err != nil {
		return count, fmt.Errorf("s3blob: archive arb history audit log: %w", err)
	}

	return count, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// archivePath builds the S3 key for an archive file, partitioned by the
// year-month of the cutoff time.
//
//	archive/trades/2025-01.jsonl
//	archive/orders/2025-01.jsonl
//	archive/arb_history/2025-01.jsonl
func archivePath(kind string, before time.Time) string {
	return fmt.Sprintf("archive/%s/%s.jsonl", kind, before.Format("2006-01"))
}

// marshalJSONL serialises a slice of values as newline-delimited JSON (JSONL).
// Each element is marshalled as a single compact JSON line followed by '\n'.
func marshalJSONL[T any](records []T) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	for i, rec := range records {
		if err := enc.Encode(rec); err != nil {
			return nil, fmt.Errorf("jsonl encode record %d: %w", i, err)
		}
	}
	return buf.Bytes(), nil
}
