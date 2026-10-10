package integration

import (
	"bufio"
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

func openStream(t *testing.T, h *testutil.Harness, runID string) (context.CancelFunc, *http.Response, chan string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, "GET", h.Server.URL+"/api/v1/runs/"+runID+"/events", nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := h.Client.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		cancel()
		t.Fatalf("sse status %d", resp.StatusCode)
	}
	lines := make(chan string, 128)
	go func() {
		defer close(lines)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	return cancel, resp, lines
}

func waitLine(t *testing.T, lines chan string, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("stream closed before seeing %q", want)
			}
			if strings.Contains(l, want) {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q", want)
		}
	}
}

func TestSSEFollowPushHeartbeatClose(t *testing.T) {
	h := testutil.New(t, blockToolDef())
	h.Model.EnqueueToolCall("tc1", "block_tool", `{}`)
	ses := h.CreateSession("s")
	id, code, _ := h.CreateRun(ses, map[string]any{"goal": "slow task"}, "follow-2")
	if code != 201 {
		t.Fatalf("create run: %d", code)
	}
	cancelStream, _, lines := openStream(t, h, id)
	defer cancelStream()

	waitLine(t, lines, "event: run.created", 5*time.Second)

	if _, err := h.Events.Append(context.Background(), id, domain.EventType("test.live"), `{}`, ""); err != nil {
		t.Fatal(err)
	}
	waitLine(t, lines, "event: test.live", 5*time.Second)

	waitLine(t, lines, ": ping", 25*time.Second)

	if code, _ := h.Do("POST", "/api/v1/runs/"+id+"/cancel", nil, nil); code != 200 {
		t.Fatalf("cancel: %d", code)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case _, ok := <-lines:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("stream did not close after terminal state")
		}
	}
}

func TestSSEAfterMid(t *testing.T) {
	h := testutil.New(t)
	h.Model.EnqueueText("done")
	ses := h.CreateSession("s")
	id, _, _ := h.CreateRun(ses, map[string]any{"goal": "hi"}, "after-mid-1")
	h.WaitTerminal(id, 30*time.Second)

	all, err := h.Events.ListAfter(context.Background(), id, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 2 {
		t.Fatalf("need >=2 events for mid-filter test, got %d", len(all))
	}
	mid := all[0].Seq
	code, body := h.Do("GET", "/api/v1/runs/"+id+"/events?after="+strconv.FormatInt(mid, 10), nil, nil)
	if code != 200 {
		t.Fatalf("sse status %d", code)
	}
	var got []int64
	for _, l := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(l, "id: ") {
			n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(l, "id: ")), 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, n)
		}
	}
	if len(got) != len(all)-1 {
		t.Fatalf("want %d events after seq %d, got %v", len(all)-1, mid, got)
	}
	for _, n := range got {
		if n <= mid {
			t.Fatalf("stale event id %d in filtered replay", n)
		}
	}
}
