package skill

import (
	"reflect"
	"testing"
)

func TestParseMetaAllFields(t *testing.T) {
	m := parseMeta("demo", "# demo\nwhen_to_use: fix build errors\nversion: 2\ntools: file_edit, http_fetch\ndescription: does X\n\nbody")
	if m.WhenToUse != "fix build errors" || m.Version != "2" || m.Description != "does X" {
		t.Fatalf("scalars wrong: %+v", m)
	}
	if !reflect.DeepEqual(m.Tools, []string{"file_edit", "http_fetch"}) {
		t.Fatalf("tools = %v", m.Tools)
	}
}

func TestParseMetaBlankLineStops(t *testing.T) {
	// 首个空行后的正文出现 description: 不得污染元数据
	m := parseMeta("demo", "# demo\ndescription: real\nversion: 1\n\nlater description: fake\nwhen_to_use: fake")
	if m.Description != "real" || m.WhenToUse != "" {
		t.Fatalf("blank line must stop parsing: %+v", m)
	}
}

func TestParseMetaMissingFieldsZero(t *testing.T) {
	m := parseMeta("demo", "# demo\ndescription: only desc\n")
	if m.Version != "" || m.WhenToUse != "" || len(m.Tools) != 0 {
		t.Fatalf("missing fields must be zero: %+v", m)
	}
}

func TestParseMetaToolsEmptyAndSpaces(t *testing.T) {
	m := parseMeta("demo", "tools:  a , ,b \n")
	if !reflect.DeepEqual(m.Tools, []string{"a", "b"}) {
		t.Fatalf("tools = %q", m.Tools)
	}
}
