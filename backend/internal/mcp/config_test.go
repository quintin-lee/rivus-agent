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
