package httpapi_test

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aluo96078/shinsou_sync_service/internal/syncsvc"
)

func readSeq(t *testing.T, ctx context.Context, c *websocket.Conn) string {
	t.Helper()
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, msg, err := c.Read(rctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(msg)
}

func TestStreamWebSocket(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice")
	b := e.login("alice", "correct horse", "tablet")
	other := e.register("bob")
	ctx := context.Background()

	// Authorization header.
	c, _, err := websocket.Dial(ctx, wsURL(e, "/api/v1/sync/stream"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + a.AccessToken}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if got := readSeq(t, ctx, c); got != `{"seq":0}` {
		t.Fatalf("initial message = %s", got)
	}

	// Sec-WebSocket-Protocol for browsers; the token is echoed back as the chosen subprotocol.
	c2, resp, err := websocket.Dial(ctx, wsURL(e, "/api/v1/sync/stream"), &websocket.DialOptions{
		Subprotocols: []string{"bearer." + b.AccessToken}})
	if err != nil {
		t.Fatal(err)
	}
	defer c2.CloseNow()
	if resp.Header.Get("Sec-WebSocket-Protocol") != "bearer."+b.AccessToken {
		t.Fatalf("subprotocol not negotiated: %q", resp.Header.Get("Sec-WebSocket-Protocol"))
	}
	readSeq(t, ctx, c2)

	// Another account's connection must stay quiet.
	c3, _, err := websocket.Dial(ctx, wsURL(e, "/api/v1/sync/stream"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + other.AccessToken}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c3.CloseNow()
	readSeq(t, ctx, c3)

	cx := clock(nowMs(), 0, a.Device.SyncDeviceID)
	push(e, a.AccessToken, 0, nil, rec("setting", "x", "value", 1, cx))
	if got := readSeq(t, ctx, c); got != `{"seq":1}` {
		t.Fatalf("device A notification = %s", got)
	}
	if got := readSeq(t, ctx, c2); got != `{"seq":1}` {
		t.Fatalf("device B notification = %s", got)
	}
	// An unchanged push does not notify; the next change carries the new seq.
	push(e, a.AccessToken, 0, nil, rec("setting", "x", "value", 1, cx))
	push(e, b.AccessToken, 0, nil, rec("setting", "y", "value", 1, clock(nowMs(), 0, b.Device.SyncDeviceID)))
	if got := readSeq(t, ctx, c); got != `{"seq":2}` {
		t.Fatalf("second notification = %s", got)
	}
	quiet, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if _, msg, err := c3.Read(quiet); err == nil {
		t.Fatalf("other account was notified: %s", msg)
	}

	// A late subscriber is told the current seq immediately.
	c4, _, err := websocket.Dial(ctx, wsURL(e, "/api/v1/sync/stream"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + a.AccessToken}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c4.CloseNow()
	if got := readSeq(t, ctx, c4); got != `{"seq":2}` {
		t.Fatalf("late subscriber = %s", got)
	}
}

func TestStreamRejectsBadCredentials(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice")
	if _, resp, err := websocket.Dial(context.Background(), wsURL(e, "/api/v1/sync/stream"), nil); err == nil || resp == nil || resp.StatusCode != 401 {
		t.Fatalf("anonymous dial: err=%v resp=%v", err, resp)
	}
	if _, resp, err := websocket.Dial(context.Background(), wsURL(e, "/api/v1/sync/stream"), &websocket.DialOptions{
		Subprotocols: []string{"bearer.garbage"}}); err == nil || resp == nil || resp.StatusCode != 401 {
		t.Fatalf("bad token dial: err=%v resp=%v", err, resp)
	}
	e.expectErr(401, "unauthorized", "GET", "/api/v1/sync/stream", "", nil, "Accept", "text/event-stream")
	e.expectErr(426, "unsupported_api_version", "GET", "/api/v1/sync/stream", a.AccessToken, nil, "Shinsou-Api-Version", "3")
}

func TestStreamSSE(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/api/v1/sync/stream", nil)
	req.Header.Set("Authorization", "Bearer "+a.AccessToken)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status %d content-type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	lines := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "data: ") {
				lines <- strings.TrimPrefix(sc.Text(), "data: ")
			}
		}
		close(lines)
	}()
	next := func() string {
		select {
		case l := <-lines:
			return l
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for an event")
			return ""
		}
	}
	if got := next(); got != `{"seq":0}` {
		t.Fatalf("initial event = %s", got)
	}
	push(e, a.AccessToken, 0, nil,
		rec("setting", "x", "value", 1, clock(nowMs(), 0, a.Device.SyncDeviceID)),
		syncsvc.PushRecord{Kind: "setting", Key: "y", Fields: rec("setting", "y", "value", 2, clock(nowMs(), 1, "d")).Fields,
			Clocks: map[string]string{"value": clock(nowMs(), 1, "d")}})
	if got := next(); got != `{"seq":2}` {
		t.Fatalf("event after push = %s", got)
	}
}
