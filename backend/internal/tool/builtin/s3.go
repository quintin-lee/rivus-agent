package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"rivus-agent-backend/internal/s3"
	"rivus-agent-backend/internal/tool"
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
func s3Read(_ context.Context, args json.RawMessage) (string, error) {
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

	// 实际 S3 读取实现：此处返回成功占位
	// 实际实现应调用: s3Client.Client.GetObject(context.Background(), s3Client.Bucket, in.Key, minio.GetObjectOptions{})
	out, _ := json.Marshal(map[string]string{
		"key":    in.Key,
		"status": "success",
		"note":   "S3 read implementation pending minio API compatibility",
	})
	return string(out), nil
}

// s3Write 实现 - 上传内容到 S3
func s3Write(_ context.Context, args json.RawMessage) (string, error) {
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

	// 实际 S3 写入实现：此处返回成功占位
	// 实际实现应调用: s3Client.Client.PutObject(context.Background(), s3Client.Bucket, in.Key, strings.NewReader(content), int64(len(content)), minio.PutObjectOptions{})
	out, _ := json.Marshal(map[string]string{
		"key":       in.Key,
		"status":    "success",
		"note":      "S3 write implementation pending minio API compatibility",
		"truncated": fmt.Sprint(len(in.Content) > in.MaxBytes),
	})
	return string(out), nil
}

// s3List 实现 - 列出存储桶中的对象
func s3List(_ context.Context, args json.RawMessage) (string, error) {
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

	// 实际 S3 列举实现：此处返回占位结果
	// 实际实现应调用: s3Client.Client.ListObjects(context.Background(), s3Client.Bucket, minio.ListObjectOptions{Prefix: in.Prefix, MaxKeys: uint(in.MaxKeys)})
	out, _ := json.Marshal(map[string]any{
		"objects": []string{},
		"note":    "S3 list implementation pending minio API compatibility",
	})
	return string(out), nil
}
