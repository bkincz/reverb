// Package s3 provides an S3-compatible storage adapter backed by aws-sdk-go-v2.
// It works with AWS S3, Cloudflare R2, MinIO, Backblaze B2, and any other
// S3-compatible service.
package s3

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/bkincz/reverb/storage"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type Config struct {
	Bucket string
	Region string
	// Endpoint is a custom endpoint for R2/MinIO/Backblaze — leave empty for AWS.
	Endpoint  string
	AccessKey string
	SecretKey string
	// BaseURL is the public CDN/bucket URL used by URL() — e.g. "https://pub.r2.dev/bucket".
	BaseURL string
	// PathStyle enables path-style URLs, required for MinIO.
	PathStyle bool
}

type Adapter struct {
	cfg    Config
	client *awss3.Client
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

func New(cfg Config) (*Adapter, error) {
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		),
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("s3 storage: load config: %w", err)
	}

	clientOpts := []func(*awss3.Options){
		func(o *awss3.Options) {
			o.UsePathStyle = cfg.PathStyle
		},
	}

	if cfg.Endpoint != "" {
		clientOpts = append(clientOpts, func(o *awss3.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		})
	}

	client := awss3.NewFromConfig(awsCfg, clientOpts...)

	return &Adapter{cfg: cfg, client: client}, nil
}

// ---------------------------------------------------------------------------
// Adapter implementation
// ---------------------------------------------------------------------------

func (a *Adapter) Upload(ctx context.Context, input storage.UploadInput) error {
	_, err := a.client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:        aws.String(a.cfg.Bucket),
		Key:           aws.String(input.Key),
		Body:          input.Body,
		ContentType:   aws.String(input.ContentType),
		ContentLength: aws.Int64(input.Size),
	})
	if err != nil {
		return fmt.Errorf("s3 storage: put object: %w", err)
	}
	return nil
}

func (a *Adapter) Delete(ctx context.Context, key string) error {
	_, err := a.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(a.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("s3 storage: delete object: %w", err)
	}
	return nil
}

func (a *Adapter) URL(key string) string {
	return a.cfg.BaseURL + "/" + key
}

func (a *Adapter) List(ctx context.Context, prefix string, limit int) ([]storage.ListItem, error) {
	input := &awss3.ListObjectsV2Input{
		Bucket: aws.String(a.cfg.Bucket),
	}
	if prefix != "" {
		input.Prefix = aws.String(prefix)
	}
	if limit > 0 {
		input.MaxKeys = aws.Int32(int32(limit))
	}

	out, err := a.client.ListObjectsV2(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("s3 storage: list objects: %w", err)
	}

	items := make([]storage.ListItem, 0, len(out.Contents))
	for _, obj := range out.Contents {
		var item storage.ListItem
		if obj.Key != nil {
			item.Key = *obj.Key
		}
		if obj.Size != nil {
			item.Size = *obj.Size
		}
		if obj.LastModified != nil {
			item.LastModified = *obj.LastModified
		}
		items = append(items, item)
	}

	return items, nil
}

var _ storage.Adapter = (*Adapter)(nil)
