package builtin

import (
	"context"
	"strings"
	"testing"

	"rivus-agent-backend/internal/tool"
)

func s3Handler(t *testing.T, name string) tool.Handler {
	t.Helper()
	for _, d := range S3Tools {
		if strings.EqualFold(d.Name, name) {
			return d.Handler
		}
	}
	t.Fatalf("S3 tool %q not found in S3Tools", name)
	return nil
}

func TestS3HandlersNilClient(t *testing.T) {
	SetS3Client(nil)

	for _, tc := range []struct {
		name string
		args string
	}{
		{"s3_read", `{"key":"a/b"}`},
		{"s3_write", `{"key":"a/b","content":"hi"}`},
		{"s3_list", `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := s3Handler(t, tc.name)(context.Background(), []byte(tc.args))
			if err == nil || !strings.Contains(err.Error(), "not initialized") {
				t.Fatalf("err = %v, out = %q; want error containing 'not initialized'", err, out)
			}
		})
	}
}
