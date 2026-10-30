package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// OAuth client-credentials：资源端点应收到 transport 注入的 Bearer 令牌；
// 401 触发一次刷新重试；持续 401 报错。
func TestHTTPOAuthTransport(t *testing.T) {
	var mu sync.Mutex
	tokenCalls := 0
	tokens := []string{"t1", "t2", "t3", "t4", "t5"}
	resourceCalls := 0
	authSeen := make([]string, 0)

	resourceHandler := func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n := resourceCalls
		resourceCalls++
		mu.Unlock()
		authSeen = append(authSeen, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") == "" {
			t.Errorf("resource call %d: missing Authorization", n)
		}
		if n == 0 {
			// 首次调用返回 401 → 触发刷新重试
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`))
	}
	resourceSrv := httptest.NewServer(http.HandlerFunc(resourceHandler))
	defer resourceSrv.Close()

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("token form: %v", err)
		}
		if r.FormValue("grant_type") != "client_credentials" || r.FormValue("client_id") != "cid" || r.FormValue("client_secret") != "sec" {
			t.Errorf("token form fields = %v", r.Form)
		}
		mu.Lock()
		tok := tokens[min(tokenCalls, len(tokens)-1)]
		tokenCalls++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{"access_token":%q,"expires_in":3600,"token_type":"Bearer"}`, tok)))
	}))
	defer tokenSrv.Close()

	spec := ServerSpec{Name: "oa", Type: "http", URL: resourceSrv.URL,
		OAuth: &OAuthConfig{TokenURL: tokenSrv.URL, ClientID: "cid", ClientSecret: "sec"}}
	tr, err := NewHTTPTransport(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Stop() }()

	// 初始令牌由后台 GET 流获取；WriteLine 后资源端点应见到某个 Bearer 令牌，
	// 401 后刷新重试，最终拿到响应。
	if err := tr.WriteLine([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp, err := tr.ReadLine()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var out struct {
		ID     int `json:"id"`
		Result struct {
			OK bool `json:"ok"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &out); err != nil || out.ID != 1 || !out.Result.OK {
		t.Fatalf("resp = %s err = %v", resp, err)
	}

	// 至少两次资源调用（401 + 重试），重试时令牌已刷新（不同于首次）
	mu.Lock()
	defer mu.Unlock()
	if len(authSeen) < 2 {
		t.Fatalf("resource calls = %d, want >= 2 (401 + retry)", len(authSeen))
	}
	// 刷新后的令牌与首个令牌不同（token 端点按序发 t1,t2,...）
	if authSeen[len(authSeen)-1] == authSeen[0] {
		t.Fatalf("retry reused same token %q", authSeen[0])
	}
}

// 缓存过期后下一次请求应重新取令牌（token 端点发新值）。
func TestHTTPOAuthTokenExpiryRefresh(t *testing.T) {
	var mu sync.Mutex
	tokenCalls := 0
	authSeen := make([]string, 0)

	resourceSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authSeen = append(authSeen, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer resourceSrv.Close()
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		tokenCalls++
		n := tokenCalls
		mu.Unlock()
		_, _ = w.Write([]byte(fmt.Sprintf(`{"access_token":"tk%d","expires_in":3600}`, n)))
	}))
	defer tokenSrv.Close()

	spec := ServerSpec{Name: "oa", Type: "http", URL: resourceSrv.URL,
		OAuth: &OAuthConfig{TokenURL: tokenSrv.URL, ClientID: "cid", ClientSecret: "sec"}}
	tr, err := NewHTTPTransport(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Stop() }()

	callOnce := func() {
		if err := tr.WriteLine([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := tr.ReadLine(); err != nil {
			t.Fatal(err)
		}
	}
	callOnce()
	// 手动把缓存拨到过期
	tr.oauthMu.Lock()
	tr.oauthExpires = time.Now().Add(-time.Minute)
	tr.oauthMu.Unlock()
	callOnce()

	mu.Lock()
	defer mu.Unlock()
	if tokenCalls < 2 {
		t.Fatalf("token calls = %d, want >= 2 after expiry", tokenCalls)
	}
	if len(authSeen) < 2 || authSeen[len(authSeen)-1] == authSeen[0] {
		t.Fatalf("auth seen = %v, want refreshed token", authSeen)
	}
}

// 持续 401：重试一次后仍失败 → WriteLine 报错（不无限循环）。
func TestHTTPOAuthPersistent401(t *testing.T) {
	resourceSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer resourceSrv.Close()
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"tk","expires_in":3600}`))
	}))
	defer tokenSrv.Close()

	spec := ServerSpec{Name: "oa", Type: "http", URL: resourceSrv.URL,
		OAuth: &OAuthConfig{TokenURL: tokenSrv.URL, ClientID: "cid", ClientSecret: "sec"}}
	tr, err := NewHTTPTransport(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Stop() }()

	if err := tr.WriteLine([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err == nil {
		t.Fatal("persistent 401 must fail")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// mapStore：内存 TokenStore 假实现，记录 Set 调用。
type mapStore struct {
	mu      sync.Mutex
	kv      map[string]string
	sets    int
	deletes int
}

func (m *mapStore) Get(_ context.Context, key string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.kv[key]
	return v, ok, nil
}

func (m *mapStore) Set(_ context.Context, key, val string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.kv == nil {
		m.kv = map[string]string{}
	}
	if val == "" {
		m.deletes++
		delete(m.kv, key)
		return nil
	}
	m.sets++
	m.kv[key] = val
	return nil
}

// 授权码模式：transport 在 RedirectPort 起本地回调，stderr 打授权链接；
// code 回调后换 token，请求携带 Bearer。
func TestHTTPOAuthAuthCodeFlow(t *testing.T) {
	var mu sync.Mutex
	tokenGrants := make([]string, 0)
	tokenCodes := make([]string, 0)
	resourceAuth := ""

	// 占位：先起服务器再改 handler（与既有测试同模式）
	resourceHandler := func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		resourceAuth = r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`))
	}
	// 同一服务器：/token 换 code，其他路径视为资源
	oauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_ = r.ParseForm()
			mu.Lock()
			tokenGrants = append(tokenGrants, r.FormValue("grant_type"))
			tokenCodes = append(tokenCodes, r.FormValue("code"))
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"ac-token","expires_in":3600}`))
			return
		}
		resourceHandler(w, r)
	}))
	defer oauthSrv.Close()

	// 找一个空闲高端口作为回调端口（探测-重绑存在 TIME_WAIT 竞态，重试几次）
	var tr *httpTransport
	var port int
	var done chan error
	for attempt := 0; attempt < 5; attempt++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port = ln.Addr().(*net.TCPAddr).Port
		_ = ln.Close()

		tr, err = NewHTTPTransport(nil, ServerSpec{
			Name: "authcode", Type: "http", URL: oauthSrv.URL,
			OAuth: &OAuthConfig{
				TokenURL:     oauthSrv.URL + "/token",
				ClientID:     "cid",
				ClientSecret: "sec",
				AuthorizeURL: oauthSrv.URL + "/auth",
				RedirectPort: port,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		done = make(chan error, 1)
		go func() { done <- tr.WriteLine([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`)) }()
		// 短暂等待：若端口被占，WriteLine 会立即报错
		select {
		case err := <-done:
			_ = tr.Stop()
			if err != nil && strings.Contains(err.Error(), "address already in use") {
				continue
			}
			t.Fatalf("WriteLine returned early: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
		break
	}

	// 轮询本地回调端口就绪后模拟浏览器重定向
	deadline := time.Now().Add(3 * time.Second)
	var cbErr error
	for {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?code=the-code", port))
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		cbErr = err
		if time.Now().After(deadline) {
			t.Fatalf("callback never ready: %v", cbErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := <-done; err != nil {
		t.Fatalf("WriteLine: %v", err)
	}
	resp, err := tr.ReadLine()
	if err != nil || !strings.Contains(string(resp), `"id":1`) {
		t.Fatalf("call resp = %s, err = %v", resp, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(tokenGrants) == 0 || tokenGrants[0] != "authorization_code" {
		t.Fatalf("token grants = %v, want authorization_code", tokenGrants)
	}
	if len(tokenCodes) == 0 || tokenCodes[0] != "the-code" {
		t.Fatalf("token codes = %v, want the-code", tokenCodes)
	}
	if resourceAuth != "Bearer ac-token" {
		t.Fatalf("resource auth = %q", resourceAuth)
	}
}

// 持久化：store 已有 token → 不访问 token 端点；无 token 换取后写回 store。
func TestHTTPOAuthTokenStorePersist(t *testing.T) {
	var mu sync.Mutex
	tokenCalls := 0
	oauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			mu.Lock()
			tokenCalls++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"fresh","expires_in":3600}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`))
	}))
	defer oauthSrv.Close()

	// 1) 预置持久化 token：零 token 端点调用
	st := &mapStore{kv: map[string]string{
		"mcp:oauth:persisted": `{"access_token":"stored","expires_at":` + fmt.Sprint(time.Now().Add(time.Hour).UnixMilli()) + `}`,
	}}
	tr, err := NewHTTPTransport(nil, ServerSpec{
		Name: "persisted", Type: "http", URL: oauthSrv.URL,
		OAuth: &OAuthConfig{TokenURL: oauthSrv.URL + "/token", ClientID: "cid", ClientSecret: "sec"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tr.tokenStore = st
	defer func() { _ = tr.Stop() }()
	if err := tr.WriteLine([]byte(`{"jsonrpc":"2.0","id":1,"method":"m"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.ReadLine(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if tokenCalls != 0 {
		t.Fatalf("tokenCalls = %d, want 0 (stored token should be used)", tokenCalls)
	}
	mu.Unlock()

	// 2) 空 store：走 client_credentials 并写回
	tr2, err := NewHTTPTransport(nil, ServerSpec{
		Name: "fetch", Type: "http", URL: oauthSrv.URL,
		OAuth: &OAuthConfig{TokenURL: oauthSrv.URL + "/token", ClientID: "cid", ClientSecret: "sec"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tr2.tokenStore = st
	defer func() { _ = tr2.Stop() }()
	if err := tr2.WriteLine([]byte(`{"jsonrpc":"2.0","id":1,"method":"m"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := tr2.ReadLine(); err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	stored := st.kv["mcp:oauth:fetch"]
	sets := st.sets
	st.mu.Unlock()
	if sets == 0 || stored == "" || !strings.Contains(stored, "fresh") {
		t.Fatalf("store not updated: sets=%d stored=%q", sets, stored)
	}
}
