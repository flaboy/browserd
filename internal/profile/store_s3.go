package profile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type S3StoreConfig struct {
	Endpoint        string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	ForcePathStyle  bool
	Bucket          string
}

type S3Store struct {
	client *s3.Client
	bucket string
}

func NewS3Store(cfg S3StoreConfig) (*S3Store, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, fmt.Errorf("s3 endpoint is required")
	}
	if strings.TrimSpace(cfg.Region) == "" {
		cfg.Region = "us-east-1"
	}
	creds := credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")
	awsCfg, err := awsConfig.LoadDefaultConfig(
		context.Background(),
		awsConfig.WithRegion(cfg.Region),
		awsConfig.WithCredentialsProvider(creds),
		awsConfig.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired),
	)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = cfg.ForcePathStyle
		o.BaseEndpoint = aws.String(cfg.Endpoint)
	})
	bucket := strings.TrimSpace(cfg.Bucket)
	if bucket == "" {
		return nil, fmt.Errorf("profile bucket is required")
	}
	return &S3Store{client: client, bucket: bucket}, nil
}

func (s *S3Store) Get(ctx context.Context, path string) (data []byte, version string, found bool, err error) {
	key, err := parseS3ProfilePath(path)
	if err != nil {
		return nil, "", false, err
	}
	head, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isS3NotFound(err) {
			return nil, "", false, nil
		}
		return nil, "", false, err
	}
	obj, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isS3NotFound(err) {
			return nil, "", false, nil
		}
		return nil, "", false, err
	}
	defer func() { _ = obj.Body.Close() }()
	body, err := io.ReadAll(obj.Body)
	if err != nil {
		return nil, "", false, err
	}
	return body, trimETag(head.ETag), true, nil
}

func (s *S3Store) Put(ctx context.Context, path string, data []byte, ifMatchVersion string) (newVersion string, err error) {
	if strings.TrimSpace(ifMatchVersion) == "" {
		return "", ErrIfMatchRequired
	}
	key, err := parseS3ProfilePath(path)
	if err != nil {
		return "", err
	}
	head, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	found := true
	if err != nil {
		if isS3NotFound(err) {
			found = false
		} else {
			return "", err
		}
	}
	if found {
		current := trimETag(head.ETag)
		if current != strings.TrimSpace(ifMatchVersion) {
			return "", ErrVersionConflict
		}
	} else if strings.TrimSpace(ifMatchVersion) != "new" {
		return "", ErrVersionConflict
	}

	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(data),
		ContentLength: aws.Int64(int64(len(data))),
	})
	if err != nil {
		return "", err
	}
	after, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", err
	}
	return trimETag(after.ETag), nil
}

func parseS3ProfilePath(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("s3 profile path is required")
	}
	if strings.Contains(trimmed, "://") {
		return "", fmt.Errorf("s3 profile path must be logical path without scheme")
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "", fmt.Errorf("s3 profile path must start with /")
	}
	key := strings.Trim(trimmed, "/")
	if key == "" {
		return "", fmt.Errorf("s3 profile key is required")
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("invalid s3 profile key segment")
		}
	}
	cleanKey := path.Clean(key)
	if cleanKey == "." || strings.HasPrefix(cleanKey, "../") || cleanKey == ".." || path.IsAbs(cleanKey) {
		return "", fmt.Errorf("invalid s3 profile key")
	}
	if !strings.HasSuffix(cleanKey, "profile.tgz") {
		return "", fmt.Errorf("s3 profile key must end with profile.tgz")
	}
	return cleanKey, nil
}

func trimETag(etag *string) string {
	if etag == nil {
		return ""
	}
	return strings.Trim(*etag, "\"")
}

func isS3NotFound(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		return code == "NotFound" || code == "NoSuchKey" || code == "NoSuchBucket"
	}
	return false
}
