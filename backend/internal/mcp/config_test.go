package mcp

import (
	"encoding/json"
	"testing"
)

func TestToolConfigCodec(t *testing.T) {
	in := ToolConfig{
		SkillsEnabled: true,
		MCPEnabled:    true,
		Servers: []ServerSpec{
			{Name: "fs", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"}},
		},
	}
	b, _ := json.Marshal(in.Servers)
	var got []ServerSpec
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "fs" {
		t.Fatalf("codec round-trip wrong: %+v", got)
	}
}

func TestServerNameValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"fs", true}, {"my_srv-1", true}, {"Bad Name", false}, {"a/b", false}, {"", false},
	} {
		if got := ValidServerName(tc.name); got != tc.want {
			t.Fatalf("ValidServerName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestServersFromJSON(t *testing.T) {
	for _, tc := range []struct {
		in      string
		wantN   int
		wantErr bool
	}{
		{in: "", wantN: 0},     // empty → no servers, no error
		{in: "null", wantN: 0}, // null → nil slice, no error
		{in: `[]`, wantN: 0},   // empty array
		{in: `[{"name":"fs","command":"npx","args":["-y"]}]`, wantN: 1},
		{in: `[{"name":"Bad Name","command":"npx"}]`, wantErr: true}, // invalid name
		{in: `[{"name":"fs","command":""}]`, wantErr: true},          // empty command
		{in: `not json`, wantErr: true},                              // malformed
	} {
		got, err := ServersFromJSON(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ServersFromJSON(%q) = no error, want error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ServersFromJSON(%q) error = %v, want nil", tc.in, err)
			continue
		}
		if len(got) != tc.wantN {
			t.Errorf("ServersFromJSON(%q) len = %d, want %d", tc.in, len(got), tc.wantN)
		}
	}
}

func TestServersFromJSONHTTPType(t *testing.T) {
	specs, err := ServersFromJSON(`[{"name":"remote","type":"http","url":"https://mcp.example.com/v1","headers":{"Authorization":"Bearer x"}}]`)
	if err != nil || len(specs) != 1 {
		t.Fatalf("err=%v specs=%v", err, specs)
	}
	if specs[0].Type != "http" || specs[0].URL != "https://mcp.example.com/v1" || specs[0].Headers["Authorization"] != "Bearer x" {
		t.Fatalf("spec=%+v", specs[0])
	}
	if _, err := ServersFromJSON(`[{"name":"bad","type":"http"}]`); err == nil {
		t.Fatal("http without url must fail")
	}
	if _, err := ServersFromJSON(`[{"name":"bad","type":"http","url":"ws://x"}]`); err == nil {
		t.Fatal("ws scheme must fail")
	}
	if _, err := ServersFromJSON(`[{"name":"s"}]`); err == nil {
		t.Fatal("stdio without command must fail")
	}
}

func TestValidateSpecs(t *testing.T) {
	ok := []ServerSpec{{Name: "a", Command: "x"}, {Name: "b", Type: "http", URL: "http://127.0.0.1:1"}}
	if err := ValidateSpecs(ok); err != nil {
		t.Fatalf("ok: %v", err)
	}
	bad := []ServerSpec{{Name: "c", Type: "http"}}
	if err := ValidateSpecs(bad); err == nil {
		t.Fatal("http spec without url must fail")
	}
}

func TestValidateSpecsOAuth(t *testing.T) {
	full := OAuthConfig{TokenURL: "https://idp/token", ClientID: "cid", ClientSecret: "sec"}
	if err := ValidateSpecs([]ServerSpec{{Name: "a", Type: "http", URL: "http://127.0.0.1:1", OAuth: &full}}); err != nil {
		t.Fatalf("valid oauth spec: %v", err)
	}
	cases := map[string]ServerSpec{
		"missing token_url": {Name: "a", Type: "http", URL: "http://127.0.0.1:1",
			OAuth: &OAuthConfig{ClientID: "cid", ClientSecret: "sec"}},
		"missing client_id": {Name: "a", Type: "http", URL: "http://127.0.0.1:1",
			OAuth: &OAuthConfig{TokenURL: "https://idp/token", ClientSecret: "sec"}},
		"missing client_secret": {Name: "a", Type: "http", URL: "http://127.0.0.1:1",
			OAuth: &OAuthConfig{TokenURL: "https://idp/token", ClientID: "cid"}},
		"stdio with oauth": {Name: "a", Command: "x",
			OAuth: &OAuthConfig{TokenURL: "https://idp/token", ClientID: "cid", ClientSecret: "sec"}},
	}
	for name, spec := range cases {
		if err := ValidateSpecs([]ServerSpec{spec}); err == nil {
			t.Fatalf("%s must fail", name)
		}
	}
}
