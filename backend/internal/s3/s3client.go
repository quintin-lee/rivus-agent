package s3

import (
	"context"
	"fmt"
	"os"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Client S3 客户端封装
type Client struct {
	*minio.Client
	endpoint string
	region   string
	Bucket   string
	useSSL   bool
}

// NewClient 初始化 S3 客户端
func NewClient(ctx context.Context) (*Client, error) {
	endpoint := os.Getenv("AGENT_S3_ENDPOINT")
	accessKey := os.Getenv("AGENT_S3_ACCESS_KEY_ID")
	secretKey := os.Getenv("AGENT_S3_SECRET_ACCESS_KEY")
	useSSL, err := parseBool(os.Getenv("AGENT_S3_USE_SSL"))
	if err != nil {
		useSSL = true // 默认使用 SSL
	}

	// minio-go v7: New 使用 Options{Creds, Secure, Region}；BucketLookup 默认 Auto，
	// 自定义端点自动走 path-style（MinIO/Ceph 无需额外配置）。
	c, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
		Region: os.Getenv("AGENT_S3_REGION"),
	})
	if err != nil {
		return nil, fmt.Errorf("minio new client: %w", err)
	}

	// 确保存储桶存在
	bucket := os.Getenv("AGENT_S3_BUCKET")
	// 预留：将在工具中使用

	return &Client{Client: c, endpoint: endpoint, Bucket: bucket, useSSL: useSSL}, nil
}

// parseBool 解析布尔值（兼容 various string 格式）
func parseBool(s string) (bool, error) {
	switch s {
	case "true", "1", "yes", "y", "on":
		return true, nil
	case "false", "0", "no", "n", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid bool: %s", s)
	}
}
