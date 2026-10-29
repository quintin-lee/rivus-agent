package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// httpCallTimeout is slightly larger than the Manager's 30s per-call timeout
// so the caller-side timeout fires first.
const httpCallTimeout = 35 * time.Second

// HTTPTransportFactory builds HTTP transports for remote MCP servers
// (Streamable-HTTP: one POST per JSON-RPC request; the reply is either a JSON
// body or a text/event-stream whose data payload carries the response).
type HTTPTransportFactory struct{ client *http.Client }

func NewHTTPTransportFactory() *HTTPTransportFactory {
	return &HTTPTransportFactory{client: &http.Client{Timeout: httpCallTimeout}}
}

func (f *HTTPTransportFactory) New(_ context.Context, spec ServerSpec) (Transport, error) {
	return newHTTPTransport(f.client, spec)
}

// NewHTTPTransport builds a standalone HTTP transport with the default client
// (convenience for tests and direct use).
func NewHTTPTransport(_ context.Context, spec ServerSpec) (*httpTransport, error) {
	return newHTTPTransport(&http.Client{Timeout: httpCallTimeout}, spec)
}

// httpTransport: WriteLine POSTs the request; ReadLine returns the full
// response. One write + one read per rpc, matching client.rpc's synchronous
// model. The next WriteLine after a read starts a fresh request.
// A background GET stream (Accept: text/event-stream) feeds server-pushed
// notifications into a line queue; ReadLine drains queued notifications
// before the pending request/response pair (client.rpcLocked skips ID=0
// lines and keeps reading, so ordering stays compatible).
type httpTransport struct {
	spec     ServerSpec
	client   *http.Client
	resp     *http.Response
	read     bool
	notes    chan []byte
	stopCh   chan struct{}
	stopOnce sync.Once
}

const httpNoteQueueCap = 64
const httpStreamRetry = 5 * time.Second

func newHTTPTransport(client *http.Client, spec ServerSpec) (*httpTransport, error) {
	t := &httpTransport{
		spec:   spec,
		client: client,
		notes:  make(chan []byte, httpNoteQueueCap),
		stopCh: make(chan struct{}),
	}
	go t.streamNotifications()
	return t, nil
}

// streamNotifications keeps a long-lived GET stream open and pushes SSE
// data payloads (server notifications) onto the line queue. It retries
// every 5s after any stream error or clean end until Stop.
func (t *httpTransport) streamNotifications() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-t.stopCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	for {
		select {
		case <-t.stopCh:
			return
		default:
		}
		if !t.streamOnce(ctx) {
			return
		}
		select {
		case <-t.stopCh:
			return
		case <-time.After(httpStreamRetry):
		}
	}
}

// isNotificationPayload reports whether p is a JSON-RPC notification
// (method present, id absent/zero). The GET stream carries only
// server-initiated messages; anything else (e.g. response-shaped frames
// from servers that mirror SSE on GET) must not pollute the queue.
func isNotificationPayload(p string) bool {
	var probe struct {
		ID     int    `json:"id"`
		Method string `json:"method"`
	}
	if json.Unmarshal([]byte(p), &probe) != nil {
		return false
	}
	return probe.Method != "" && probe.ID == 0
}

// streamOnce opens one GET stream and pushes notification payloads; it
// returns false when stopped (caller should exit) and true to retry.
func (t *httpTransport) streamOnce(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.spec.URL, nil)
	if err != nil {
		return true
	}
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range t.spec.Headers {
		req.Header.Set(k, v)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return ctx.Err() == nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_ = resp.Body.Close()
		return true
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var data strings.Builder
	inData := false
	push := func() {
		if inData {
			payload := strings.TrimSpace(data.String())
			if payload != "" && isNotificationPayload(payload) {
				select {
				case t.notes <- []byte(payload):
				default: // 队列满：丢弃，防 goroutine 阻塞泄漏
				}
			}
			data.Reset()
			inData = false
		}
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			push()
		case strings.HasPrefix(line, "data:"):
			if inData {
				data.WriteString("\n")
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			inData = true
		}
	}
	_ = resp.Body.Close()
	push()
	return ctx.Err() == nil
}

func (t *httpTransport) WriteLine(p []byte) error {
	req, err := http.NewRequest(http.MethodPost, t.spec.URL, bytes.NewReader(p))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range t.spec.Headers {
		req.Header.Set(k, v)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		_ = resp.Body.Close()
		return fmt.Errorf("mcp http %s: status %d: %s", t.spec.Name, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	t.resp = resp
	t.read = false
	return nil
}

func (t *httpTransport) ReadLine() ([]byte, error) {
	// 先排水一条排队的通知（后台 GET 流推送）；rpcLocked 读循环会跳过 ID=0 行继续等响应。
	select {
	case n := <-t.notes:
		return n, nil
	default:
	}
	if t.resp == nil {
		return nil, fmt.Errorf("mcp http %s: read without write", t.spec.Name)
	}
	if t.read {
		_ = t.resp.Body.Close()
		t.resp = nil
		return nil, io.EOF
	}
	t.read = true
	body, err := io.ReadAll(io.LimitReader(t.resp.Body, 1<<20))
	ctype := t.resp.Header.Get("Content-Type")
	if err != nil {
		_ = t.resp.Body.Close()
		return nil, err
	}
	if strings.HasPrefix(ctype, "text/event-stream") {
		data, derr := sseResponseData(body)
		if derr != nil {
			_ = t.resp.Body.Close()
			return nil, derr
		}
		return data, nil
	}
	return body, nil
}

// sseResponseData scans SSE frames and returns the first data payload that
// parses as a JSON object with an id field (client.rpc re-validates the id;
// the per-client mutex guarantees one in-flight request).
func sseResponseData(body []byte) ([]byte, error) {
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var data strings.Builder
	inData := false
	flush := func() []byte {
		if !inData {
			return nil
		}
		payload := []byte(data.String())
		data.Reset()
		inData = false
		var probe struct {
			ID int `json:"id"`
		}
		if json.Unmarshal(payload, &probe) == nil && probe.ID != 0 {
			return payload
		}
		return nil
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "": // frame boundary
			if p := flush(); p != nil {
				return p, nil
			}
		case strings.HasPrefix(line, "data:"):
			if inData {
				data.WriteString("\n") // multi-line data joined per SSE spec
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			inData = true
		}
	}
	if p := flush(); p != nil {
		return p, nil
	}
	return nil, fmt.Errorf("mcp http: no json-rpc response in sse stream")
}

func (t *httpTransport) Stop() error {
	t.stopOnce.Do(func() { close(t.stopCh) })
	if t.resp != nil {
		_ = t.resp.Body.Close()
		t.resp = nil
	}
	t.client.CloseIdleConnections()
	return nil
}
