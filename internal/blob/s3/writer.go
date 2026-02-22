package s3blob

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// minPartSize is the minimum allowed part size for S3 multipart uploads (5 MiB).
const minPartSize int64 = 5 * 1024 * 1024

// Writer implements domain.BlobWriter using an S3-compatible backend.
type Writer struct {
	client *s3.Client
	bucket string
}

// NewWriter creates a new Writer that uploads objects to the given client's
// configured bucket.
func NewWriter(c *Client) *Writer {
	return &Writer{
		client: c.S3(),
		bucket: c.Bucket(),
	}
}

// Put uploads data as a single S3 PutObject request. This is suitable for
// objects small enough to upload in one shot (typically < 5 GiB, but for
// larger payloads prefer PutMultipart).
func (w *Writer) Put(ctx context.Context, path string, data io.Reader, contentType string) error {
	input := &s3.PutObjectInput{
		Bucket:      aws.String(w.bucket),
		Key:         aws.String(path),
		Body:        data,
		ContentType: aws.String(contentType),
	}

	_, err := w.client.PutObject(ctx, input)
	if err != nil {
		return fmt.Errorf("s3blob: put object %s: %w", path, err)
	}
	return nil
}

// PutMultipart uploads data using the S3 multipart upload manager, which
// automatically splits the payload into parts and uploads them concurrently.
// The partSize parameter controls the size of each part in bytes; if it is
// smaller than the S3 minimum (5 MiB) it will be clamped to the minimum.
func (w *Writer) PutMultipart(ctx context.Context, path string, data io.Reader, partSize int64) error {
	if partSize < minPartSize {
		partSize = minPartSize
	}

	uploader := manager.NewUploader(w.client, func(u *manager.Uploader) {
		u.PartSize = partSize
	})

	input := &s3.PutObjectInput{
		Bucket: aws.String(w.bucket),
		Key:    aws.String(path),
		Body:   data,
	}

	_, err := uploader.Upload(ctx, input)
	if err != nil {
		return fmt.Errorf("s3blob: multipart upload %s: %w", path, err)
	}
	return nil
}
