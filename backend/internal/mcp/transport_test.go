package mcp

import (
	"context"
	"strings"
	"testing"
)

// /bin/cat is deterministic: it echoes each stdin line to stdout verbatim.
func TestStdioTransportRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("requires /bin/cat")
	}
	tt, err := NewStdioTransport(context.Background(), "/bin/cat", nil)
	if err != nil {
		t.Fatalf("start /bin/cat: %v", err)
	}
	defer func() { _ = tt.Stop() }()
	if err := tt.WriteLine([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	line, err := tt.ReadLine()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.TrimSpace(string(line)) != "ping" {
		t.Fatalf("round-trip = %q, want ping", line)
	}
}

func TestSpecFactoryDispatch(t *testing.T) {
	f := NewSpecTransportFactory()
	if _, err := f.New(context.Background(), ServerSpec{Name: "x", Command: "/nonexistent-cmd-xyz"}); err == nil {
		t.Fatal("stdio dispatch expected exec failure")
	}
	if _, err := f.New(context.Background(), ServerSpec{Name: "x", Type: "http", URL: "http://127.0.0.1:1"}); err != nil {
		t.Fatalf("http dispatch must not dial eagerly: %v", err)
	}
}
