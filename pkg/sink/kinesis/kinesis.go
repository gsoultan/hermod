package kinesis

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/user/hermod"
)

type KinesisSink struct {
	client     *kinesis.Client
	streamName string
	formatter  hermod.Formatter
}

func NewKinesisSink(region string, streamName string, accessKey, secretKey string, formatter hermod.Formatter) (*KinesisSink, error) {
	opts := []func(*config.LoadOptions) error{
		config.WithRegion(region),
	}

	if accessKey != "" && secretKey != "" {
		opts = append(opts, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")))
	}

	cfg, err := config.LoadDefaultConfig(context.TODO(), opts...)
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config: %w", err)
	}

	client := kinesis.NewFromConfig(cfg)

	return &KinesisSink{
		client:     client,
		streamName: streamName,
		formatter:  formatter,
	}, nil
}

func (s *KinesisSink) Write(ctx context.Context, msg hermod.Message) error {
	data, err := s.formatter.Format(msg)
	if err != nil {
		return fmt.Errorf("failed to format message: %w", err)
	}

	// We can use the table name or some other attribute as part of the partition key if needed
	// but msg.ID() is usually a good default for ordering within a shard.
	partitionKey := msg.ID()
	if partitionKey == "" {
		partitionKey = "default"
	}

	_, err = s.client.PutRecord(ctx, &kinesis.PutRecordInput{
		Data:         data,
		PartitionKey: aws.String(partitionKey),
		StreamName:   aws.String(s.streamName),
	})
	if err != nil {
		return fmt.Errorf("failed to put record to kinesis: %w", err)
	}

	return nil
}

func (s *KinesisSink) Ping(ctx context.Context) error {
	_, err := s.client.DescribeStream(ctx, &kinesis.DescribeStreamInput{
		StreamName: aws.String(s.streamName),
	})
	if err != nil {
		return fmt.Errorf("failed to describe kinesis stream: %w", err)
	}
	return nil
}

func (s *KinesisSink) Close() error {
	return nil
}
