package s3

import (
	"testing"

	"rivus-agent-backend/internal/config"
)

func TestNewClientRequiresEndpoint(t *testing.T) {
	cfg := config.Default()
	cfg.S3Endpoint = ""
	if _, err := NewClient(cfg); err == nil {
		t.Fatal("expected error for empty endpoint, got nil")
	}
}

func TestNewClientFakeEndpoint(t *testing.T) {
	cfg := config.Default()
	cfg.S3Endpoint = "127.0.0.1:19000"
	cfg.S3AccessKeyID = "ak"
	cfg.S3SecretAccessKey = "sk"
	cfg.S3Bucket = "bucket"
	cfg.S3WorkspacePrefix = "ws/"
	cfg.S3KnowledgePrefix = "kb/"

	c, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if !c.Enabled() {
		t.Fatal("Enabled() = false, want true")
	}
	if c.Bucket != "bucket" {
		t.Fatalf("Bucket = %q, want bucket", c.Bucket)
	}
	if c.WorkspacePrefix != "ws/" {
		t.Fatalf("WorkspacePrefix = %q, want ws/", c.WorkspacePrefix)
	}
	if c.KnowledgePrefix != "kb/" {
		t.Fatalf("KnowledgePrefix = %q, want kb/", c.KnowledgePrefix)
	}
}

func TestEnabledFalseForNilAndEmpty(t *testing.T) {
	var nilC *Client
	if nilC.Enabled() {
		t.Fatal("nil client Enabled() = true, want false")
	}
	empty := &Client{}
	if empty.Enabled() {
		t.Fatal("empty endpoint Enabled() = true, want false")
	}
}
