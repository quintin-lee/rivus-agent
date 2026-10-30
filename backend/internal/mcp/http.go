package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
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
type HTTPTransportFactory struct {
	client     *http.Client
	TokenStore TokenStore
}

func NewHTTPTransportFactory() *HTTPTransportFactory {
	return &HTTPTransportFactory{client: &http.Client{Timeout: httpCallTimeout}}
}

func (f *HTTPTransportFactory) New(_ context.Context, spec ServerSpec) (Transport, error) {
	tt, err := newHTTPTransport(f.client, spec)
	if err != nil {
		return nil, err
	}
	tt.tokenStore = f.TokenStore
	return tt, nil
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

	oauthMu      sync.Mutex
	oauthToken   string
	oauthExpires time.Time
	tokenStore   TokenStore
}

// TokenStore 持久化 OAuth 令牌（生产实现为 *store.SettingsRepo，测试用 map 假实现）。
type TokenStore interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, val string) error
}

type oauthStored struct {
	AccessToken string `json:"access_token"`
	ExpiresAt   int64  `json:"expires_at"`
}

const oauthStorePrefix = "mcp:oauth:"

func (t *httpTransport) oauthKey() string { return oauthStorePrefix + t.spec.Name }

const httpNoteQueueCap = 64
const httpStreamRetry = 5 * time.Second

// oauthRefreshSkew: refresh the token when less than this remains.
const oauthRefreshSkew = 30 * time.Second

// ensureToken returns a valid bearer token: in-memory cache first, then the
// persisted store, then the OAuth flow (authorization-code when AuthorizeURL
// is set, client-credentials otherwise).
func (t *httpTransport) ensureToken(ctx context.Context) (string, error) {
	t.oauthMu.Lock()
	if t.oauthToken != "" && time.Until(t.oauthExpires) > oauthRefreshSkew {
		tok := t.oauthToken
		t.oauthMu.Unlock()
		return tok, nil
	}
	t.oauthMu.Unlock()

	if tok, exp, ok := t.loadStoredToken(ctx); ok {
		t.oauthMu.Lock()
		t.oauthToken = tok
		t.oauthExpires = exp
		t.oauthMu.Unlock()
		return tok, nil
	}

	cfg := t.spec.OAuth
	var (
		tok string
		exp time.Time
		err error
	)
	if cfg.AuthorizeURL != "" {
		tok, exp, err = t.authCodeToken(ctx, cfg)
	} else {
		form := url.Values{
			"grant_type":    {"client_credentials"},
			"client_id":     {cfg.ClientID},
			"client_secret": {cfg.ClientSecret},
		}
		if len(cfg.Scopes) > 0 {
			form.Set("scope", strings.Join(cfg.Scopes, " "))
		}
		tok, exp, err = t.fetchToken(ctx, form)
	}
	if err != nil {
		return "", err
	}
	t.oauthMu.Lock()
	t.oauthToken = tok
	t.oauthExpires = exp
	t.oauthMu.Unlock()
	t.persistToken(ctx, tok, exp)
	return tok, nil
}

func (t *httpTransport) loadStoredToken(ctx context.Context) (string, time.Time, bool) {
	if t.tokenStore == nil {
		return "", time.Time{}, false
	}
	raw, ok, err := t.tokenStore.Get(ctx, t.oauthKey())
	if err != nil || !ok {
		return "", time.Time{}, false
	}
	var st oauthStored
	if json.Unmarshal([]byte(raw), &st) != nil || st.AccessToken == "" {
		return "", time.Time{}, false
	}
	exp := time.UnixMilli(st.ExpiresAt)
	if time.Until(exp) <= oauthRefreshSkew {
		return "", time.Time{}, false
	}
	return st.AccessToken, exp, true
}

func (t *httpTransport) persistToken(ctx context.Context, tok string, exp time.Time) {
	if t.tokenStore == nil {
		return
	}
	b, _ := json.Marshal(oauthStored{AccessToken: tok, ExpiresAt: exp.UnixMilli()})
	_ = t.tokenStore.Set(ctx, t.oauthKey(), string(b))
}

func (t *httpTransport) clearStoredToken(ctx context.Context) {
	if t.tokenStore == nil {
		return
	}
	_ = t.tokenStore.Set(ctx, t.oauthKey(), "")
}

// fetchToken POSTs an OAuth token request and parses the access_token.
func (t *httpTransport) fetchToken(ctx context.Context, form url.Values) (string, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.spec.OAuth.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("mcp http %s: oauth token request: %w", t.spec.Name, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range t.spec.Headers {
		req.Header.Set(k, v)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("mcp http %s: oauth token fetch: %w", t.spec.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("mcp http %s: oauth token body: %w", t.spec.Name, err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("mcp http %s: oauth token status %d: %s", t.spec.Name, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil || tr.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("mcp http %s: oauth token response invalid: %w", t.spec.Name, err)
	}
	expires := time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	if tr.ExpiresIn <= 0 {
		expires = time.Now().Add(time.Hour)
	}
	return tr.AccessToken, expires, nil
}

// authCodeToken runs the interactive authorization-code flow: local callback
// listener, authorize URL on stderr, code exchange at the token endpoint.
func (t *httpTransport) authCodeToken(ctx context.Context, cfg *OAuthConfig) (string, time.Time, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.RedirectPort))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("mcp http %s: oauth callback listen: %w", t.spec.Name, err)
	}
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if e := r.URL.Query().Get("error"); e != "" {
			w.WriteHeader(http.StatusBadRequest)
			errCh <- fmt.Errorf("oauth authorize error: %s", e)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			w.WriteHeader(http.StatusBadRequest)
			errCh <- fmt.Errorf("oauth callback missing code")
			return
		}
		_, _ = fmt.Fprint(w, "已收到授权码，可关闭此页面。")
		codeCh <- code
	})}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", cfg.RedirectPort)
	q := url.Values{
		"response_type": {"code"},
		"client_id":     {cfg.ClientID},
		"redirect_uri":  {redirectURI},
	}
	if len(cfg.Scopes) > 0 {
		q.Set("scope", strings.Join(cfg.Scopes, " "))
	}
	fmt.Fprintf(os.Stderr, "mcp %s: 请在浏览器中打开以下链接完成授权：\n%s\n", t.spec.Name, cfg.AuthorizeURL+"?"+q.Encode())

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return "", time.Time{}, fmt.Errorf("mcp http %s: %w", t.spec.Name, err)
	case <-ctx.Done():
		return "", time.Time{}, fmt.Errorf("mcp http %s: oauth authorize canceled: %w", t.spec.Name, ctx.Err())
	case <-time.After(5 * time.Minute):
		return "", time.Time{}, fmt.Errorf("mcp http %s: oauth authorize timed out", t.spec.Name)
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
	}
	return t.fetchToken(ctx, form)
}

// applyAuth copies spec.Headers and overrides Authorization with the OAuth
// bearer token when configured (explicit token beats static header).
func (t *httpTransport) applyAuth(ctx context.Context, h map[string]string) error {
	for k, v := range t.spec.Headers {
		h[k] = v
	}
	if t.spec.OAuth == nil {
		return nil
	}
	tok, err := t.ensureToken(ctx)
	if err != nil {
		return err
	}
	h["Authorization"] = "Bearer " + tok
	return nil
}

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
	headers := map[string]string{}
	if err := t.applyAuth(ctx, headers); err != nil {
		return true // 取令牌失败：按可重试错误处理，5s 后再试
	}
	for k, v := range headers {
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
	// OAuth：401 时失效缓存重取令牌并重试一次；仍 401 返回原错误。
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequest(http.MethodPost, t.spec.URL, bytes.NewReader(p))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		headers := map[string]string{}
		if err := t.applyAuth(req.Context(), headers); err != nil {
			return err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := t.client.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusUnauthorized && t.spec.OAuth != nil && attempt == 0 {
			t.oauthMu.Lock()
			t.oauthToken = ""
			t.oauthMu.Unlock()
			t.clearStoredToken(req.Context())
			_ = resp.Body.Close()
			continue
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
