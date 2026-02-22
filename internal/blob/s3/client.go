// Package s3blob implements the domain blob interfaces using AWS SDK v2,
// with compatibility for S3-compatible storage providers such as iDrive e2,
// MinIO, and Cloudflare R2.
package s3blob

import (
	"context"
	"fmt"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ClientConfig holds the configuration for connecting to an S3-compatible
// object store. All S3-compatible providers (iDrive e2, MinIO, R2) are
// supported via the Endpoint field.
type ClientConfig struct {
	// Endpoint is the S3-compatible endpoint URL, e.g. "https://e2.idy.idrivee2.com".
	// Leave empty for standard AWS S3.
	Endpoint string

	// Region is the AWS region or equivalent for the provider.
	Region string

	// Bucket is the default bucket name for all operations.
	Bucket string

	// AccessKey is the access key ID for authentication.
	AccessKey string

	// SecretKey is the secret access key for authentication.
	SecretKey string

	// UseSSL controls whether HTTPS is used when constructing the endpoint.
	// Only relevant when Endpoint is provided without a scheme.
	UseSSL bool

	// ForcePathStyle forces path-style addressing (bucket in path rather than
	// subdomain). Required by iDrive e2 and many S3-compatible providers.
	ForcePathStyle bool
}

// Client wraps the AWS S3 SDK client and stores the default bucket name.
// It implements the connectivity layer used by the reader and writer types.
type Client struct {
	s3     *s3.Client
	bucket string
}

// New creates a new S3 client from the given configuration. It configures
// custom credentials, endpoint resolution, path-style addressing, and region
// to support both standard AWS S3 and compatible providers like iDrive e2.
func New(ctx context.Context, cfg ClientConfig) (*Client, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("s3blob: bucket name is required")
	}
	if cfg.Region == "" {
		return nil, fmt.Errorf("s3blob: region is required")
	}

	// Build static credentials from access key / secret key.
	creds := credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")

	// Load the base AWS config with our credentials and region.
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(cfg.Region),
		config.WithCredentialsProvider(creds),
	)
	if err != nil {
		return nil, fmt.Errorf("s3blob: load aws config: %w", err)
	}

	// Build S3-specific options, optionally overriding the endpoint for
	// S3-compatible providers.
	var s3Opts []func(*s3.Options)

	if cfg.Endpoint != "" {
		endpoint := normaliseEndpoint(cfg.Endpoint, cfg.UseSSL)
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(endpoint)
		})
	}

	if cfg.ForcePathStyle {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.UsePathStyle = true
		})
	}

	client := s3.NewFromConfig(awsCfg, s3Opts...)

	return &Client{
		s3:     client,
		bucket: cfg.Bucket,
	}, nil
}

// Health performs a HeadBucket call to verify connectivity and permissions.
// Returns nil if the bucket is accessible.
func (c *Client) Health(ctx context.Context) error {
	_, err := c.s3.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(c.bucket),
	})
	if err != nil {
		return fmt.Errorf("s3blob: health check failed for bucket %s: %w", c.bucket, err)
	}
	return nil
}

// Close is a no-op included for interface consistency. The underlying S3
// HTTP client does not require explicit teardown.
func (c *Client) Close() error {
	return nil
}

// S3 returns the underlying AWS SDK S3 client for use by the reader and
// writer implementations within this package.
func (c *Client) S3() *s3.Client {
	return c.s3
}

// Bucket returns the configured default bucket name.
func (c *Client) Bucket() string {
	return c.bucket
}

// normaliseEndpoint ensures the endpoint has a scheme. If the provided
// endpoint already has a scheme it is returned as-is; otherwise https:// or
// http:// is prepended based on useSSL.
func normaliseEndpoint(endpoint string, useSSL bool) string {
	parsed, err := url.Parse(endpoint)
	if err == nil && parsed.Scheme != "" {
		return endpoint
	}
	scheme := "http"
	if useSSL {
		scheme = "https"
	}
	return scheme + "://" + endpoint
}
