package mcp

import (
	"context"
	"testing"
)

func TestFakeTransportFactory(t *testing.T) {
	f := NewFakeTransportFactory()
	f.ByServer["fs"] = &FakeTransport{Tools: []string{"read"}, CallResult: "ok"}
	tt, err := f.New(context.Background(), ServerSpec{Name: "fs", Command: "npx"})
	if err != nil {
		t.Fatal(err)
	}
	if ft, ok := tt.(*FakeTransport); !ok || ft != f.ByServer["fs"] {
		t.Fatalf("factory must return the configured fake, got %T", tt)
	}
}
