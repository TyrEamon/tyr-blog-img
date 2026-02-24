package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type R2Config struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
}

type R2Client struct {
	bucket string
	s3     *s3.Client
}

func NewR2Client(ctx context.Context, cfg R2Config) (*R2Client, error) {
	cfg.Endpoint = strings.TrimSpace(cfg.Endpoint)
	cfg.Region = strings.TrimSpace(cfg.Region)
	cfg.Bucket = strings.TrimSpace(cfg.Bucket)
	cfg.AccessKey = strings.TrimSpace(cfg.AccessKey)
	cfg.SecretKey = strings.TrimSpace(cfg.SecretKey)

	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, fmt.Errorf("r2 config incomplete")
	}
	if cfg.Region == "" {
		cfg.Region = "auto"
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")),
	)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = true
		o.BaseEndpoint = &cfg.Endpoint
	})

	return &R2Client{
		bucket: cfg.Bucket,
		s3:     client,
	}, nil
}

func (c *R2Client) PutObject(ctx context.Context, key string, data []byte, contentType string) error {
	return c.PutObjectWithCacheControl(ctx, key, data, contentType, "public, max-age=31536000, immutable")
}

func (c *R2Client) PutObjectWithCacheControl(ctx context.Context, key string, data []byte, contentType, cacheControl string) error {
	key = strings.TrimSpace(key)
	contentType = strings.TrimSpace(contentType)
	cacheControl = strings.TrimSpace(cacheControl)
	if key == "" {
		return fmt.Errorf("empty key")
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if cacheControl == "" {
		cacheControl = "public, max-age=31536000, immutable"
	}

	_, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:       &c.bucket,
		Key:          &key,
		Body:         bytes.NewReader(data),
		ContentType:  &contentType,
		CacheControl: &cacheControl,
	})
	return err
}

func (c *R2Client) GetObject(ctx context.Context, key string) ([]byte, string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, "", fmt.Errorf("empty key")
	}
	out, err := c.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &c.bucket,
		Key:    &key,
	})
	if err != nil {
		return nil, "", err
	}
	defer out.Body.Close()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, "", err
	}
	contentType := ""
	if out.ContentType != nil {
		contentType = strings.TrimSpace(*out.ContentType)
	}
	return data, contentType, nil
}

func (c *R2Client) DeleteObject(ctx context.Context, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &c.bucket,
		Key:    &key,
	})
	return err
}

func strPtr(v string) *string { return &v }
