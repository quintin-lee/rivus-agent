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
type httpTransport struct {
	spec   ServerSpec
	client *http.Client
	resp   *http.Response
	read   bool
}

func newHTTPTransport(client *http.Client, spec ServerSpec) (*httpTransport, error) {
	return &httpTransport{spec: spec, client: client}, nil
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
	if t.resp != nil {
		_ = t.resp.Body.Close()
		t.resp = nil
	}
	t.client.CloseIdleConnections()
	return nil
}
