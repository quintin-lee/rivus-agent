package integration

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"rivus-agent-backend/tests/testutil"
)

func TestCursorPaginationAndNext(t *testing.T) {
	h := testutil.New(t)
	for _, name := range []string{"c-page-1", "c-page-2", "c-page-3"} {
		h.CreateSession(name)
		time.Sleep(2 * time.Millisecond) // updated_at ms 精度：游标键严格递减
	}

	// 第 1 页：limit=2 → 两个最新会话 + next_cursor
	code, body := h.Do("GET", "/api/v1/sessions?limit=2", nil, nil)
	if code != 200 {
		t.Fatalf("page1 code=%d body=%s", code, body)
	}
	var page1 struct {
		Sessions   []struct{ Title string } `json:"sessions"`
		NextCursor string                   `json:"next_cursor"`
	}
	if err := json.Unmarshal(body, &page1); err != nil {
		t.Fatal(err)
	}
	if len(page1.Sessions) != 2 || page1.NextCursor == "" {
		t.Fatalf("page1 = %+v", page1)
	}
	if page1.Sessions[0].Title != "c-page-3" {
		t.Fatalf("page1 newest = %s", page1.Sessions[0].Title)
	}

	// 第 2 页：cursor 翻页 → 无重叠
	code, body = h.Do("GET", "/api/v1/sessions?limit=2&cursor="+page1.NextCursor, nil, nil)
	if code != 200 {
		t.Fatalf("page2 code=%d body=%s", code, body)
	}
	var page2 struct {
		Sessions   []struct{ Title string } `json:"sessions"`
		NextCursor string                   `json:"next_cursor"`
	}
	if err := json.Unmarshal(body, &page2); err != nil {
		t.Fatal(err)
	}
	if len(page2.Sessions) != 1 || page2.Sessions[0].Title != "c-page-1" {
		t.Fatalf("page2 = %+v", page2)
	}

	// 无效 cursor → 400
	code, body = h.Do("GET", "/api/v1/sessions?cursor=!!!bad", nil, nil)
	if code != 400 || !strings.Contains(string(body), "bad_request") {
		t.Fatalf("invalid cursor = %d %s", code, body)
	}
}
