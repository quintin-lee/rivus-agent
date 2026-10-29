package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// SSE 长连接：GET 流推送的通知进入行队列，ReadLine 优先返回通知（rpcLocked 读循环
// 会跳过 ID=0 行并继续等响应），POST 请求-响应配对语义不变。
func TestHTTPTransportSSENotifications(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`))
		case http.MethodGet:
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\",\"params\":{}}\n\n"))
			flusher.Flush()
			_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\"params\":{}}\n\n"))
			flusher.Flush()
			<-r.Context().Done() // 保持长连接直到客户端断开
		}
	}))
	defer srv.Close()

	tr, err := NewHTTPTransport(nil, ServerSpec{Name: "push", Type: "http", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Stop() }()

	// 两条通知应先于任何请求到达（GET 流后台拉取）
	want := []string{"notifications/tools/list_changed", "notifications/message"}
	for _, m := range want {
		deadline := time.Now().Add(2 * time.Second)
		var line []byte
		for {
			line, err = tr.ReadLine()
			if err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("timeout waiting for notification %s: %v", m, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !strings.Contains(string(line), m) {
			t.Fatalf("notification = %s, want containing %s", line, m)
		}
	}

	// 请求-响应配对仍然正常
	if err := tr.WriteLine([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`)); err != nil {
		t.Fatal(err)
	}
	resp, err := tr.ReadLine()
	if err != nil || !strings.Contains(string(resp), `"id":1`) {
		t.Fatalf("call resp = %s, err = %v", resp, err)
	}

	// Stop 后 ReadLine 立即报错（不悬挂）
	if err := tr.Stop(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, e := tr.ReadLine()
		done <- e
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ReadLine after Stop should return immediately")
	}
}
