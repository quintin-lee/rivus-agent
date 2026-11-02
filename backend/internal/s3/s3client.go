package s3

import (
	"fmt"

	"rivus-agent-backend/internal/config"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Client S3 客户端封装
type Client struct {
	*minio.Client
	endpoint        string
	region          string
	Bucket          string
	useSSL          bool
	WorkspacePrefix string
	KnowledgePrefix string
}

// Enabled 是否在配置中启用（endpoint 非空即启用）。
func (c *Client) Enabled() bool {
	return c != nil && c.endpoint != ""
}

// NewClient 从 config.Config 构造 S3 客户端。
// env 已由 config.Load/applyEnv 灌入各字段，此处不再直接读 os.Getenv。
func NewClient(cfg config.Config) (*Client, error) {
	endpoint := cfg.S3Endpoint
	accessKey := cfg.S3AccessKeyID
	secretKey := cfg.S3SecretAccessKey
	useSSL := cfg.S3UseSSL
	region := cfg.S3Region
	bucket := cfg.S3Bucket
	if endpoint == "" {
		return nil, fmt.Errorf("s3 endpoint is empty")
	}

	// minio-go v7: New 使用 Options{Creds, Secure, Region}；BucketLookup 默认 Auto，
	// 自定义端点自动走 path-style（MinIO/Ceph 无需额外配置）。
	c, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("minio new client: %w", err)
	}

	return &Client{
		Client:          c,
		endpoint:        endpoint,
		region:          region,
		Bucket:          bucket,
		useSSL:          useSSL,
		WorkspacePrefix: cfg.S3WorkspacePrefix,
		KnowledgePrefix: cfg.S3KnowledgePrefix,
	}, nil
}
