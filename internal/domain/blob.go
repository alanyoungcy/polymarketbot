package domain

import (
	"context"
	"io"
	"time"
)

// BlobInfo describes a stored object.
type BlobInfo struct {
	Path         string
	Size         int64
	ContentType  string
	LastModified time.Time
}

// BlobWriter uploads data to object storage.
type BlobWriter interface {
	Put(ctx context.Context, path string, data io.Reader, contentType string) error
	PutMultipart(ctx context.Context, path string, data io.Reader, partSize int64) error
}

// BlobReader retrieves data from object storage.
type BlobReader interface {
	Get(ctx context.Context, path string) (io.ReadCloser, error)
	List(ctx context.Context, prefix string) ([]BlobInfo, error)
	Exists(ctx context.Context, path string) (bool, error)
}

// Archiver moves old data from the database to cold storage.
type Archiver interface {
	ArchiveTrades(ctx context.Context, before time.Time) (int64, error)
	ArchiveOrders(ctx context.Context, before time.Time) (int64, error)
	ArchiveArbHistory(ctx context.Context, before time.Time) (int64, error)
}
