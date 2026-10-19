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
