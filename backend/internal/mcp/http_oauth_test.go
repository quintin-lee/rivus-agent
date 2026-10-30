package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
