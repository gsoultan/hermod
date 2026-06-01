package filestorage

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3Storage struct {
	client *s3.Client
	bucket string
	region string
}

func NewS3Storage(ctx context.Context, endpoint, region, bucket, accessKey, secretKey string, useSSL bool) (*S3Storage, error) {
	customResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...any) (aws.Endpoint, error) {
		if endpoint != "" {
			return aws.Endpoint{
				PartitionID:   "aws",
				URL:           endpoint,
				SigningRegion: region,
			}, nil
		}
		return aws.Endpoint{}, &aws.EndpointNotFoundError{}
	})

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithEndpointResolverWithOptions(customResolver),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.UsePathStyle = true
		}
	})

	return &S3Storage{
		client: client,
		bucket: bucket,
		region: region,
	}, nil
}

func (s *S3Storage) Save(ctx context.Context, name string, r io.Reader) (string, error) {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(name),
		Body:   r,
	})
	if err != nil {
		return "", fmt.Errorf("failed to put object to s3: %w", err)
	}

	// For S3, we return a URI that Hermod sources can understand
	return fmt.Sprintf("s3://%s/%s", s.bucket, name), nil
}

func (s *S3Storage) GetURL(ctx context.Context, name string) (string, error) {
	return fmt.Sprintf("s3://%s/%s", s.bucket, name), nil
}

func (s *S3Storage) Delete(ctx context.Context, name string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(name),
	})
	return err
}

func (s *S3Storage) Type() string {
	return "s3"
}
