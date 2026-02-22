package s3blob

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// Reader implements domain.BlobReader using an S3-compatible backend.
type Reader struct {
	client *s3.Client
	bucket string
}

// NewReader creates a new Reader that retrieves objects from the given
// client's configured bucket.
func NewReader(c *Client) *Reader {
	return &Reader{
		client: c.S3(),
		bucket: c.Bucket(),
	}
}

// Get retrieves the object at the given path and returns its body as an
// io.ReadCloser. The caller is responsible for closing the returned reader.
// Returns domain.ErrNotFound if the object does not exist.
func (r *Reader) Get(ctx context.Context, path string) (io.ReadCloser, error) {
	output, err := r.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(path),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("s3blob: get %s: %w", path, domain.ErrNotFound)
		}
		return nil, fmt.Errorf("s3blob: get %s: %w", path, err)
	}
	return output.Body, nil
}

// List returns metadata for all objects whose key starts with the given
// prefix. It handles pagination transparently, following ContinuationTokens
// until all matching objects have been collected.
func (r *Reader) List(ctx context.Context, prefix string) ([]domain.BlobInfo, error) {
	var infos []domain.BlobInfo

	paginator := s3.NewListObjectsV2Paginator(r.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(r.bucket),
		Prefix: aws.String(prefix),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("s3blob: list prefix %s: %w", prefix, err)
		}

		for _, obj := range page.Contents {
			info := domain.BlobInfo{
				Path: aws.ToString(obj.Key),
				Size: aws.ToInt64(obj.Size),
			}
			if obj.LastModified != nil {
				info.LastModified = *obj.LastModified
			}
			// ListObjectsV2 does not return ContentType; leave it empty.
			infos = append(infos, info)
		}
	}

	return infos, nil
}

// Exists checks whether an object exists at the given path by issuing a
// HeadObject request. Returns true if the object exists, false if it does
// not. Any error other than NoSuchKey / NotFound is propagated.
func (r *Reader) Exists(ctx context.Context, path string) (bool, error) {
	_, err := r.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(path),
	})
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("s3blob: exists %s: %w", path, err)
	}
	return true, nil
}

// Delete removes the object at the given path. Idempotent: no error if the
// object does not exist. Implements domain.BlobDeleter.
func (r *Reader) Delete(ctx context.Context, path string) error {
	_, err := r.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(path),
	})
	if err != nil {
		return fmt.Errorf("s3blob: delete %s: %w", path, err)
	}
	return nil
}

// isNotFound returns true when the error indicates the requested S3 object
// does not exist. It checks for both the SDK typed error (NoSuchKey) and
// the generic 404 response.
func isNotFound(err error) bool {
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}

	// HeadObject does not return NoSuchKey; it returns a generic 404.
	// The SDK wraps this as a *types.NotFound or a smithy ResponseError
	// with status 404.
	var nf *types.NotFound
	if errors.As(err, &nf) {
		return true
	}

	// Fallback: some S3-compatible providers return a ResponseError with
	// HTTP 404. We check via the smithy HTTP response interface.
	type httpResponseError interface {
		HTTPStatusCode() int
	}
	var httpErr httpResponseError
	if errors.As(err, &httpErr) && httpErr.HTTPStatusCode() == 404 {
		return true
	}

	return false
}
