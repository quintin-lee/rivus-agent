package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"rivus-agent-backend/internal/s3"
	"rivus-agent-backend/internal/tool"

	"github.com/minio/minio-go/v7"
)

// S3 配置（由 app 装配）
var s3Client *s3.Client

// SetS3Client 注入 S3 客户端（在 RegisterAll 之前调用）
func SetS3Client(c *s3.Client) {
	s3Client = c
}

// s3Tools 返回已注册的 S3 工具定义
var S3Tools = []tool.Definition{
	{
		Name:        "s3_read",
		Description: "读取 S3 对象（只读）",
		Version:     "v1",
		InputSchema: `{"type":"object","properties":{"key":{"type":"string"},"max_bytes":{"type":"integer"}},"required":["key"]}`,
		Risk:        tool.RiskRead, Approval: tool.ApprovalNever, Idempotent: true, TimeoutMs: 10000, MaxResultBytes: 32 * 1024,
		Handler: s3Read,
	},
	{
		Name:        "s3_write",
		Description: "上传内容到 S3（受控写入，默认审约）",
		Version:     "v1",
		InputSchema: `{"type":"object","properties":{"key":{"type":"string"},"content":{"type":"string"},"max_bytes":{"type":"integer"}},"required":["key","content"]}`,
		Risk:        tool.RiskWrite, Approval: tool.ApprovalConditional, Idempotent: false, TimeoutMs: 15000, MaxResultBytes: 4 * 1024,
		Handler: s3Write,
	},
	{
		Name:        "s3_list",
		Description: "列出存储桶中的对象（只读）",
		Version:     "v1",
		InputSchema: `{"type":"object","properties":{"prefix":{"type":"string"},"max_keys":{"type":"integer"}},"required":[]}`,
		Risk:        tool.RiskRead, Approval: tool.ApprovalNever, Idempotent: true, TimeoutMs: 10000, MaxResultBytes: 32 * 1024,
		Handler: s3List,
	},
}

// s3Read 实现 - 读取 S3 对象
func s3Read(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Key      string `json:"key"`
		MaxBytes int    `json:"max_bytes"`
	}
	if err := json.Unmarshal(args, &in); err != nil || in.Key == "" {
		return "", fmt.Errorf("invalid key")
	}

	if s3Client == nil || s3Client.Client == nil {
		return "", fmt.Errorf("s3 client not initialized")
	}

	key := s3Client.WorkspacePrefix + in.Key
	obj, err := s3Client.GetObject(ctx, s3Client.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return "", fmt.Errorf("s3 read: %w", err)
	}
	defer func() { _ = obj.Close() }()

	cap := in.MaxBytes
	if cap <= 0 {
		cap = 32 * 1024
	}
	data, err := io.ReadAll(io.LimitReader(obj, int64(cap)+1))
	if err != nil {
		return "", fmt.Errorf("s3 read body: %w", err)
	}
	truncated := len(data) > cap
	if truncated {
		data = data[:cap]
	}
	out, _ := json.Marshal(map[string]any{
		"key":       in.Key,
		"status":    "success",
		"size":      len(data),
		"truncated": truncated,
		"content":   string(data),
	})
	return string(out), nil
}

// s3Write 实现 - 上传内容到 S3
func s3Write(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Key      string `json:"key"`
		Content  string `json:"content"`
		MaxBytes int    `json:"max_bytes"`
	}
	if err := json.Unmarshal(args, &in); err != nil || in.Key == "" || in.Content == "" {
		return "", fmt.Errorf("invalid input")
	}

	if s3Client == nil || s3Client.Client == nil {
		return "", fmt.Errorf("s3 client not initialized")
	}

	key := s3Client.WorkspacePrefix + in.Key
	_, err := s3Client.PutObject(ctx, s3Client.Bucket, key, strings.NewReader(in.Content), int64(len(in.Content)), minio.PutObjectOptions{})
	if err != nil {
		return "", fmt.Errorf("s3 write: %w", err)
	}
	out, _ := json.Marshal(map[string]any{
		"key":           in.Key,
		"status":        "success",
		"bytes_written": len(in.Content),
	})
	return string(out), nil
}

// s3List 实现 - 列出存储桶中的对象
func s3List(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Prefix  string `json:"prefix"`
		MaxKeys int    `json:"max_keys"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("invalid input")
	}

	if s3Client == nil || s3Client.Client == nil {
		return "", fmt.Errorf("s3 client not initialized")
	}

	limit := in.MaxKeys
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	prefix := s3Client.KnowledgePrefix + in.Prefix

	objects := []map[string]any{}
	for info := range s3Client.ListObjects(ctx, s3Client.Bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
		MaxKeys:   limit,
	}) {
		objects = append(objects, map[string]any{
			"key":           info.Key,
			"size":          info.Size,
			"last_modified": info.LastModified.Format(time.RFC3339),
		})
	}
	out, _ := json.Marshal(map[string]any{
		"objects": objects,
		"count":   len(objects),
	})
	return string(out), nil
}
