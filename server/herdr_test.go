package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeHerdr is a herdr CLI server on a unix socket: newline-delimited JSON,
// one answer per request, events.subscribe streams what emit sends.
type fakeHerdr struct {
	sock string
	t    *testing.T

	mu      sync.Mutex
	calls   []herdrCallRec
	answer  map[string]func(params map[string]any) (any, *herdrError)
	subs    []*fakeSub
	subbed  chan []any // each events.subscribe's subscriptions
	refused bool       // answer nothing: the socket is there, the server isn't
}

type herdrCallRec struct {
	Method string
	Params map[string]any
}

type fakeSub struct {
	mu   sync.Mutex
	conn net.Conn
}

func newFakeHerdr(t *testing.T) *fakeHerdr {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "hrf") // short: unix socket paths are
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	f := &fakeHerdr{sock: filepath.Join(dir, "h.sock"), t: t, answer: map[string]func(map[string]any) (any, *herdrError){}, subbed: make(chan []any, 16)}
	ln, err := net.Listen("unix", f.sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeHerdr) on(method string, fn func(params map[string]any) (any, *herdrError)) {
	f.mu.Lock()
	f.answer[method] = fn
	f.mu.Unlock()
}

func (f *fakeHerdr) serve(c net.Conn) {
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var req struct {
			ID     string         `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			c.Close()
			return
		}
		f.mu.Lock()
		f.calls = append(f.calls, herdrCallRec{req.Method, req.Params})
		fn := f.answer[req.Method]
		f.mu.Unlock()
		if req.Method == "events.subscribe" {
			subs, _ := req.Params["subscriptions"].([]any)
			sub := &fakeSub{conn: c}
			sub.write(map[string]any{"id": req.ID, "result": map[string]any{"type": "subscription_started"}})
			f.mu.Lock()
			f.subs = append(f.subs, sub)
			f.mu.Unlock()
			select {
			case f.subbed <- subs:
			default:
			}
			continue // a second request would end it in herdr; never sent here
		}
		var reply map[string]any
		switch {
		case fn == nil:
			reply = map[string]any{"id": req.ID, "error": map[string]any{"code": "unknown_method", "message": "unknown method " + req.Method}}
		default:
			res, herr := fn(req.Params)
			if herr != nil {
				reply = map[string]any{"id": req.ID, "error": map[string]any{"code": herr.Code, "message": herr.Message}}
			} else {
				reply = map[string]any{"id": req.ID, "result": res}
			}
		}
		b, _ := json.Marshal(reply)
		c.Write(append(b, '\n'))
		c.Close() // herdr answers one request per connection here as there
		return
	}
	c.Close()
}

func (s *fakeSub) write(v any) error {
	b, _ := json.Marshal(v)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.conn.Write(append(b, '\n'))
	return err
}

// emit sends an event to every subscriber.
func (f *fakeHerdr) emit(event string, data any) {
	f.mu.Lock()
	subs := append([]*fakeSub(nil), f.subs...)
	f.mu.Unlock()
	for _, s := range subs {
		s.write(map[string]any{"event": event, "data": data})
	}
}

// dropSubs closes the event streams, as a herdr restart would.
func (f *fakeHerdr) dropSubs() {
	f.mu.Lock()
	subs := f.subs
	f.subs = nil
	f.mu.Unlock()
	for _, s := range subs {
		s.conn.Close()
	}
}

func (f *fakeHerdr) took(method string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c.Params)
		}
	}
	return out
}

func (f *fakeHerdr) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, c.Method)
	}
	return out
}

func (f *fakeHerdr) reset() {
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
}

// A session: workspace wF "proj" with tab wF:t2 "2" (panes wF:p2 claude,
// blocked; wF:p3 a shell), focused.
var fakeSnapshot = map[string]any{
	"focused_workspace_id": "wF", "focused_tab_id": "wF:t2", "focused_pane_id": "wF:p2",
	"workspaces": []any{map[string]any{"workspace_id": "wF", "label": "proj", "number": 1, "active_tab_id": "wF:t2", "tab_count": 1, "pane_count": 2, "agent_status": "blocked", "focused": true}},
	"tabs":       []any{map[string]any{"tab_id": "wF:t2", "workspace_id": "wF", "label": "2", "number": 2, "pane_count": 2, "agent_status": "blocked", "focused": true}},
	"panes": []any{
		map[string]any{"pane_id": "wF:p2", "tab_id": "wF:t2", "workspace_id": "wF", "cwd": "/tmp", "foreground_cwd": "/tmp/app", "agent": "claude", "agent_status": "blocked", "focused": true, "scroll": map[string]any{"viewport_rows": 4}},
		map[string]any{"pane_id": "wF:p3", "tab_id": "wF:t2", "workspace_id": "wF", "cwd": "/tmp", "agent_status": "unknown", "focused": false, "scroll": map[string]any{"viewport_rows": 4}},
	},
	"agents":  []any{map[string]any{"agent": "claude", "agent_status": "blocked", "pane_id": "wF:p2", "tab_id": "wF:t2", "workspace_id": "wF"}},
	"layouts": []any{},
}

func snapshotAnswer(snap map[string]any) func(map[string]any) (any, *herdrError) {
	return func(map[string]any) (any, *herdrError) {
		return map[string]any{"type": "session_snapshot", "snapshot": snap}, nil
	}
}

func paneAnswer(params map[string]any) (any, *herdrError) {
	id, _ := params["pane_id"].(string)
	for _, p := range fakeSnapshot["panes"].([]any) {
		if p.(map[string]any)["pane_id"] == id {
			return map[string]any{"type": "pane_info", "pane": p}, nil
		}
	}
	return nil, &herdrError{Code: "pane_not_found", Message: "pane " + id + " not found"}
}

func okAnswer(map[string]any) (any, *herdrError) { return map[string]any{"type": "ok"}, nil }

func TestHerdrAddress(t *testing.T) {
	for _, ok := range []string{"herdr:wF:p2", "herdr:w1:p1E", "herdr:wAb3:pZ9"} {
		if !validTerminal(ok) {
			t.Errorf("%q refused", ok)
		}
		if _, isHerdr := herdrPane(ok); !isHerdr {
			t.Errorf("%q not a herdr pane", ok)
		}
	}
	for _, bad := range []string{"herdr:", "herdr:wF", "herdr:wF:t2", "herdr:wF:p2:x", "herdr:wF:p-2", "herdr:../p2", "herdr:wF:p2 ", "Herdr:wF:p2", "herdr:wF/p2", "box/herdr:wF:p2"} {
		if validTerminal(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	if p, _ := herdrPane("herdr:wF:p1E"); p != "wF:p1E" {
		t.Errorf("pane %q", p)
	}
	// hc's addresses are still theirs.
	for _, hc := range []string{"t12", "box/t12"} {
		if !validTerminal(hc) {
			t.Errorf("%q refused", hc)
		}
		if _, isHerdr := herdrPane(hc); isHerdr {
			t.Errorf("%q taken for herdr", hc)
		}
	}
}

func TestHerdrState(t *testing.T) {
	f := newFakeHerdr(t)
	f.on("session.snapshot", snapshotAnswer(fakeSnapshot))
	appUp, _ := fakeTool(t, "herdr-app", `case "$1" in status) echo '{"window":true}';; ls) echo '{"spaces":[]}';; esac`)
	appDown, _ := fakeTool(t, "herdr-app", `echo "no window" >&2; exit 1`)
	hcDown, _ := fakeTool(t, "hc", `exit 1`)

	get := func(s *server) (int, map[string]json.RawMessage) {
		rec := httptest.NewRecorder()
		s.state(rec, httptest.NewRequest("GET", "/api/state", nil))
		var out map[string]json.RawMessage
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	// Both: everything.
	code, out := get(&server{herdr: appUp, hc: hcDown, herdrSock: f.sock})
	want, _ := json.Marshal(fakeSnapshot)
	var got, wantV any
	json.Unmarshal(out["herdr"], &got)
	json.Unmarshal(want, &wantV)
	if code != 200 || string(out["status"]) != `{"window":true}` || string(out["ls"]) != `{"spaces":[]}` || !reflect.DeepEqual(got, wantV) {
		t.Errorf("both: %d %v", code, out)
	}
	if m := f.took("session.snapshot"); len(m) != 1 || len(m[0]) != 0 {
		t.Errorf("snapshot params: %v", m)
	}

	// The Herdr app down, the herdr CLI up: still 200, without status and ls.
	code, out = get(&server{herdr: appDown, hc: hcDown, herdrSock: f.sock})
	_, hasStatus := out["status"]
	_, hasLs := out["ls"]
	if code != 200 || hasStatus || hasLs || out["herdr"] == nil {
		t.Errorf("app down: %d %v", code, out)
	}

	// The herdr CLI not running: as before.
	code, out = get(&server{herdr: appUp, hc: hcDown, herdrSock: filepath.Join(t.TempDir(), "none.sock")})
	if _, has := out["herdr"]; code != 200 || has || out["ls"] == nil {
		t.Errorf("herdr down: %d %v", code, out)
	}

	// Neither: 502, the app's error.
	rec := httptest.NewRecorder()
	(&server{herdr: appDown, hc: hcDown, herdrSock: filepath.Join(t.TempDir(), "none.sock")}).state(rec, httptest.NewRequest("GET", "/api/state", nil))
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), "herdr-app status") {
		t.Errorf("both down: %d %s", rec.Code, rec.Body)
	}

	// herdr hanging doesn't hold the state past 2 s.
	ln, _ := net.Listen("unix", f.sock+"2")
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // never answers
		}
	}()
	start := time.Now()
	code, out = get(&server{herdr: appUp, hc: hcDown, herdrSock: f.sock + "2"})
	if _, has := out["herdr"]; code != 200 || has || time.Since(start) > 4*time.Second {
		t.Errorf("hanging herdr: %d %v after %v", code, out, time.Since(start))
	}
}

func TestHerdrKey(t *testing.T) {
	cases := map[string]herdrInput{
		"enter": {key: "enter"}, "Enter": {key: "enter"}, "return": {key: "enter"},
		"esc": {key: "esc"}, "Escape": {key: "esc"},
		"tab": {key: "tab"}, "shifttab": {key: "shift+tab"}, "btab": {key: "shift+tab"},
		"backspace": {key: "backspace"}, "bs": {key: "backspace"}, "space": {key: "space"},
		"up": {key: "up"}, "Down": {key: "down"}, "left": {key: "left"}, "right": {key: "right"},
		"home": {text: "\x1b[H"}, "end": {text: "\x1b[F"}, "pgup": {text: "\x1b[5~"}, "pagedown": {text: "\x1b[6~"},
		"del": {text: "\x1b[3~"}, "delete": {text: "\x1b[3~"}, "insert": {text: "\x1b[2~"},
		"ctrl+c": {key: "ctrl+c"}, "C-c": {key: "ctrl+c"}, "ctrl-d": {key: "ctrl+d"}, "^z": {key: "ctrl+z"},
		"alt+x": {key: "alt+x"}, "M-b": {key: "alt+b"}, "f5": {key: "f5"}, "F12": {key: "f12"},
		"a": {key: "a"}, "Y": {key: "Y"}, "1": {key: "1"},
	}
	for in, want := range cases {
		if got := herdrKey(in); got != want {
			t.Errorf("%q: %+v, want %+v", in, got, want)
		}
	}
}

func TestHerdrReadSendKeys(t *testing.T) {
	f := newFakeHerdr(t)
	f.on("pane.get", paneAnswer)
	f.on("pane.read", func(p map[string]any) (any, *herdrError) {
		if _, err := paneAnswer(p); err != nil {
			return nil, err
		}
		return map[string]any{"type": "pane_read", "read": map[string]any{"pane_id": p["pane_id"], "source": p["source"], "format": "text", "text": "$ ls\nfile\n", "truncated": false, "revision": 0}}, nil
	})
	for _, m := range []string{"pane.send_text", "pane.send_keys", "pane.send_input"} {
		f.on(m, okAnswer)
	}
	f.on("agent.prompt", func(p map[string]any) (any, *herdrError) {
		if p["target"] == "wF:p2" {
			return map[string]any{"type": "ok"}, nil
		}
		return nil, &herdrError{Code: "agent_not_ready", Message: "no agent"}
	})
	s := &server{hc: "/nonexistent/hc", herdrSock: f.sock}

	// read: the screen (lines 0), else recent with the screen's rows more.
	code, out := call(s.read, "GET", "/api/read?terminal=herdr:wF:p3&lines=0", "")
	if code != 200 || out["text"] != "$ ls\nfile\n" || out["complete"] != true {
		t.Errorf("read: %d %v", code, out)
	}
	code, _ = call(s.read, "GET", "/api/read?terminal=herdr:wF:p3&lines=100", "")
	reads := f.took("pane.read")
	if code != 200 || len(reads) != 2 ||
		reads[0]["source"] != "visible" || reads[0]["format"] != "text" || reads[0]["lines"] != nil ||
		reads[1]["source"] != "recent" || reads[1]["lines"] != float64(104) || reads[1]["strip_ansi"] != true {
		t.Errorf("read params: %d %v", code, reads)
	}
	if code, _ := call(s.read, "GET", "/api/read?terminal=herdr:wF:p9", ""); code != 502 {
		t.Errorf("gone pane: %d", code)
	}

	// send: an agent through agent.prompt; a shell typed, Enter pressed.
	f.reset()
	if code, out := call(s.send, "POST", "/api/send", `{"terminal":"herdr:wF:p2","text":"fix it"}`); code != 200 || out["how"] != "prompt" {
		t.Errorf("send agent: %d %v", code, out)
	}
	if p := f.took("agent.prompt"); len(p) != 1 || p[0]["target"] != "wF:p2" || p[0]["text"] != "fix it" {
		t.Errorf("agent.prompt: %v", p)
	}
	f.reset()
	if code, out := call(s.send, "POST", "/api/send", `{"terminal":"herdr:wF:p3","text":"echo hi"}`); code != 200 || out["how"] != "typed" {
		t.Errorf("send shell: %d %v", code, out)
	}
	if p := f.took("pane.send_input"); len(p) != 1 || p[0]["text"] != "echo hi" || fmt.Sprint(p[0]["keys"]) != "[enter]" {
		t.Errorf("send_input: %v", p)
	}
	// force: typed, never through agent.prompt.
	f.reset()
	call(s.send, "POST", "/api/send", `{"terminal":"herdr:wF:p2","text":"no, use x","force":true}`)
	if m := f.methods(); !reflect.DeepEqual(m, []string{"pane.send_input"}) {
		t.Errorf("forced: %v", m)
	}

	// keys: hc's names in, herdr's out; the ones herdr has no name for as bytes, in order.
	f.reset()
	code, out = call(s.keys, "POST", "/api/keys", `{"terminal":"herdr:wF:p2","keys":["Escape","C-c","down","enter"]}`)
	if p := f.took("pane.send_keys"); code != 200 || len(p) != 1 || fmt.Sprint(p[0]["keys"]) != "[esc ctrl+c down enter]" || p[0]["pane_id"] != "wF:p2" {
		t.Errorf("keys: %d %v %v", code, out, p)
	}
	f.reset()
	call(s.keys, "POST", "/api/keys", `{"terminal":"herdr:wF:p2","keys":["home","x","end","pgup","enter"]}`)
	got := []string{}
	f.mu.Lock()
	for _, c := range f.calls {
		got = append(got, fmt.Sprintf("%s %q %v", c.Method, c.Params["text"], c.Params["keys"]))
	}
	f.mu.Unlock()
	want := []string{`pane.send_input "\x1b[H" [x]`, `pane.send_input "\x1b[F\x1b[5~" [enter]`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mixed keys:\n %q\nwant\n %q", got, want)
	}
	f.on("pane.send_keys", func(map[string]any) (any, *herdrError) {
		return nil, &herdrError{Code: "invalid_key", Message: "unsupported key f99"}
	})
	if code, _ := call(s.keys, "POST", "/api/keys", `{"terminal":"herdr:wF:p2","keys":["f99"]}`); code != 400 {
		t.Errorf("unknown key: %d", code)
	}
	f.on("pane.send_keys", okAnswer)

	// interrupt: Esc for an agent, Ctrl-C for anything else.
	f.reset()
	call(s.interrupt, "POST", "/api/interrupt", `{"terminal":"herdr:wF:p2"}`)
	call(s.interrupt, "POST", "/api/interrupt", `{"terminal":"herdr:wF:p3"}`)
	if p := f.took("pane.send_keys"); len(p) != 2 || fmt.Sprint(p[0]["keys"]) != "[esc]" || fmt.Sprint(p[1]["keys"]) != "[ctrl+c]" {
		t.Errorf("interrupt: %v", p)
	}
}

func TestHerdrInputOf(t *testing.T) {
	cases := []struct {
		in   string
		want []herdrInput
	}{
		{"ls -la\r", []herdrInput{{text: "ls -la"}, {key: "enter"}}},
		{"\x03", []herdrInput{{key: "ctrl+c"}}},
		{"\x1b", []herdrInput{{key: "esc"}}},
		{"\x1b[A\x1bOB\x1b[C\x1b[D", []herdrInput{{key: "up"}, {key: "down"}, {key: "right"}, {key: "left"}}},
		{"\x7f\t\x1b[Z", []herdrInput{{key: "backspace"}, {key: "tab"}, {key: "shift+tab"}}},
		{"\x1b[H\x1b[3~\x1b[5~", []herdrInput{{text: "\x1b[H\x1b[3~\x1b[5~"}}},
		{"\x1bb", []herdrInput{{text: "\x1bb"}}}, // Alt-b, as typed
		{"é✓", []herdrInput{{text: "é✓"}}},
		{"\x1b[<0;10;5M\x1b[<0;10;5m", nil}, // mouse: dropped
		{"\x1b[M !!a", []herdrInput{{text: "a"}}},
		{"\x1b\x1b", []herdrInput{{key: "esc"}, {key: "esc"}}},
	}
	for _, c := range cases {
		if got := herdrInputOf([]byte(c.in)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestHerdrPaint(t *testing.T) {
	lines := []string{"\x1b[1mhi\x1b[0m", "$ ls", ""}
	got := string(herdrPaint(lines, 4, 80))
	want := "\x1b[?25l\x1b[0m\x1b[H\x1b[2J\x1b[1mhi\x1b[0m\r\n$ ls\r\n\x1b[0m\x1b[2;5H"
	if got != want {
		t.Errorf("paint %q\nwant %q", got, want)
	}
	// More rows than the screen: the last ones.
	if got := string(herdrPaint([]string{"a", "b", "c"}, 2, 80)); !strings.Contains(got, "\x1b[2Jb\r\nc") {
		t.Errorf("cut %q", got)
	}
	// History: what recent has above the screen.
	recent := []string{"old1", "old2", "\x1b[1mhi\x1b[0m", "$ ls"}
	if h := herdrHistoryOf(recent, lines); !reflect.DeepEqual(h, []string{"old1", "old2"}) {
		t.Errorf("history %q", h)
	}
	if h := herdrHistoryOf([]string{"x", "y"}, []string{"full", "screen"}); h != nil {
		t.Errorf("alternate screen: %q", h)
	}
	if got := string(herdrHistory([]string{"old1", "old2"}, 3)); got != "\x1b[0m\x1b[H\x1b[2Jold1\x1b[0m\r\nold2\x1b[0m\r\n\n\n" {
		t.Errorf("history frame %q", got)
	}
	if w := textWidth(stripANSI("\x1b[31m日本\x1b[0m ok")); w != 7 {
		t.Errorf("width %d", w)
	}
}

func TestHerdrAttach(t *testing.T) {
	oldPoll, oldThrottle := herdrPoll, herdrThrottle
	herdrPoll, herdrThrottle = 40*time.Millisecond, 5*time.Millisecond
	defer func() { herdrPoll, herdrThrottle = oldPoll, oldThrottle }()

	f := newFakeHerdr(t)
	f.on("pane.get", paneAnswer)
	var smu sync.Mutex
	screen := "\x1b[32m$\x1b[0m ls\r\nfile"
	f.on("pane.read", func(p map[string]any) (any, *herdrError) {
		smu.Lock()
		defer smu.Unlock()
		text := screen
		if p["source"] == "recent" {
			text = "older\r\n" + screen
		}
		return map[string]any{"type": "pane_read", "read": map[string]any{"text": text, "truncated": false}}, nil
	})
	typed := make(chan herdrCallRec, 8)
	for _, m := range []string{"pane.send_text", "pane.send_keys", "pane.send_input"} {
		m := m
		f.on(m, func(p map[string]any) (any, *herdrError) {
			typed <- herdrCallRec{m, p}
			return map[string]any{"type": "ok"}, nil
		})
	}
	s := &server{herdrSock: f.sock}
	srv := httptest.NewServer(http.HandlerFunc(s.attach))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// A pane that isn't there: 404 before the upgrade.
	if resp, err := http.Get(srv.URL + "?terminal=herdr:wF:p9"); err != nil || resp.StatusCode != 404 {
		t.Fatalf("missing pane: %v %v", resp, err)
	}

	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"?terminal=herdr:wF:p3&cols=60&rows=20", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	next := func() (websocket.MessageType, string) {
		typ, b, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return typ, string(b)
	}

	// The size first, then history and screen in one frame.
	typ, msg := next()
	if typ != websocket.MessageText || msg != `{"cols":80,"rows":4,"snapshot":true,"type":"size"}` {
		t.Fatalf("size: %q", msg)
	}
	typ, msg = next()
	if typ != websocket.MessageBinary || !strings.HasPrefix(msg, "\x1b[0m\x1b[H\x1b[2Jolder\x1b[0m\r\n\n\n\n") ||
		!strings.HasSuffix(msg, "\x1b[?25l\x1b[0m\x1b[H\x1b[2J\x1b[32m$\x1b[0m ls\r\nfile\x1b[0m\x1b[2;5H") {
		t.Fatalf("first paint: %q", msg)
	}

	// The screen changes: one repaint, not more while it stays.
	smu.Lock()
	screen = "$ ls\r\nfile\r\n$ "
	smu.Unlock()
	typ, msg = next()
	if typ != websocket.MessageBinary || msg != "\x1b[?25l\x1b[0m\x1b[H\x1b[2J$ ls\r\nfile\r\n$ \x1b[0m\x1b[3;2H" {
		t.Fatalf("repaint: %q", msg)
	}

	// Typing: text, then Enter as herdr's key; Ctrl-C; an arrow.
	conn.Write(ctx, websocket.MessageBinary, []byte("echo hi\r"))
	conn.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":50,"rows":20}`)) // ignored
	conn.Write(ctx, websocket.MessageBinary, []byte("\x03"))
	conn.Write(ctx, websocket.MessageBinary, []byte("\x1b[A"))
	var got []string
	for i := 0; i < 3; i++ {
		select {
		case c := <-typed:
			got = append(got, fmt.Sprintf("%s %q %v", c.Method, c.Params["text"], c.Params["keys"]))
		case <-ctx.Done():
			t.Fatalf("typed only %q", got)
		}
	}
	want := []string{`pane.send_input "echo hi" [enter]`, `pane.send_keys %!q(<nil>) [ctrl+c]`, `pane.send_keys %!q(<nil>) [up]`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("typed %q\nwant %q", got, want)
	}

	// A wider row: the new size with a repaint, history and screen again.
	smu.Lock()
	screen = strings.Repeat("x", 100)
	smu.Unlock()
	typ, msg = next()
	if typ != websocket.MessageText || msg != `{"cols":100,"repaint":true,"rows":4,"snapshot":true,"type":"size"}` {
		t.Fatalf("resize: %q", msg)
	}
	if typ, msg = next(); typ != websocket.MessageBinary || !strings.HasSuffix(msg, strings.Repeat("x", 100)+"\x1b[0m\x1b[1;100H") {
		t.Fatalf("resized paint: %q", msg)
	}

	// The pane closes: exit.
	select {
	case subs := <-f.subbed:
		if len(subs) != 4 {
			t.Errorf("subscriptions: %v", subs)
		}
	case <-ctx.Done():
		t.Fatal("no subscription")
	}
	f.emit("pane_closed", map[string]any{"type": "pane_closed", "pane_id": "wF:p3", "workspace_id": "wF"})
	for {
		typ, msg = next()
		if typ == websocket.MessageText {
			break
		}
	}
	if msg != `{"reason":"the program in this terminal has ended","type":"exit"}` {
		t.Errorf("exit: %q", msg)
	}
}

func TestHerdrEvents(t *testing.T) {
	oldMin := herdrRetryMin
	herdrRetryMin = 20 * time.Millisecond
	defer func() { herdrRetryMin = oldMin }()

	f := newFakeHerdr(t)
	var smu sync.Mutex
	snap := fakeSnapshot
	f.on("session.snapshot", func(map[string]any) (any, *herdrError) {
		smu.Lock()
		defer smu.Unlock()
		return map[string]any{"type": "session_snapshot", "snapshot": snap}, nil
	})
	s := &server{hc: "/nonexistent/hc", herdrSock: f.sock, events: newEventHub("/nonexistent/hc")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.herdrEvents(ctx)

	var subs []any
	select {
	case subs = <-f.subbed:
	case <-time.After(5 * time.Second):
		t.Fatal("no subscription")
	}
	perPane := 0
	for _, x := range subs {
		m := x.(map[string]any)
		if m["type"] == "pane.agent_status_changed" {
			perPane++
		}
	}
	if perPane != 2 {
		t.Errorf("subscriptions: %v", subs)
	}

	// Seeded quietly: the blocked agent is current, no event.
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
			s.events.mu.Lock()
			ok := cond()
			s.events.mu.Unlock()
			if ok {
				return
			}
		}
		t.Fatalf("%s: current %v ring %v", what, s.events.current, s.events.ring)
	}
	waitFor("seed", func() bool {
		c := s.events.current["herdr:wF:p2"]
		return c.State == "blocked" && c.App == "claude" && c.Where == "proj / 2" && len(s.events.ring) == 0
	})

	// Status changes: events in hc's shape.
	f.emit("pane.agent_status_changed", map[string]any{"pane_id": "wF:p2", "workspace_id": "wF", "agent": "claude", "agent_status": "working", "title": "Fixing"})
	waitFor("working", func() bool {
		return len(s.events.ring) == 1 && s.events.ring[0].State == "working"
	})
	ev := s.events.ring[0]
	if ev.Terminal != "herdr:wF:p2" || ev.App != "claude" || ev.Where != "proj / 2" || ev.Title != "Fixing" || ev.Seq != 1 {
		t.Errorf("event %+v", ev)
	}
	f.emit("pane.agent_status_changed", map[string]any{"pane_id": "wF:p2", "agent": "claude", "agent_status": "blocked", "state_labels": map[string]string{"kind": "permission"}})
	waitFor("blocked", func() bool { return len(s.events.ring) == 2 })
	if ev := s.events.ring[1]; ev.State != "blocked" || ev.Kind != "permission" {
		t.Errorf("blocked %+v", ev)
	}
	f.emit("pane_agent_detected", map[string]any{"pane_id": "wF:p2", "agent": "claude", "released": true, "final_status": "unknown"})
	waitFor("cleared", func() bool {
		_, has := s.events.current["herdr:wF:p2"]
		return len(s.events.ring) == 3 && s.events.ring[2].State == "" && !has
	})

	// A new pane: subscribed again, its agent picked up from the snapshot.
	smu.Lock()
	snap = map[string]any{}
	for k, v := range fakeSnapshot {
		snap[k] = v
	}
	snap["panes"] = append(append([]any{}, fakeSnapshot["panes"].([]any)...), map[string]any{"pane_id": "wF:p4", "tab_id": "wF:t2", "workspace_id": "wF", "agent_status": "working"})
	snap["agents"] = []any{
		map[string]any{"agent": "claude", "agent_status": "unknown", "pane_id": "wF:p2"},
		map[string]any{"agent": "codex", "agent_status": "working", "pane_id": "wF:p4"},
	}
	smu.Unlock()
	f.emit("pane_created", map[string]any{"type": "pane_created", "pane": map[string]any{"pane_id": "wF:p4"}})
	select {
	case subs = <-f.subbed:
		if len(subs) != 12 {
			t.Errorf("again: %d subscriptions", len(subs))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("not subscribed again")
	}
	waitFor("new pane", func() bool {
		c := s.events.current["herdr:wF:p4"]
		return c.State == "working" && c.App == "codex" && len(s.events.ring) == 4
	})

	// A pane closes: ended.
	f.emit("pane_closed", map[string]any{"type": "pane_closed", "pane_id": "wF:p4", "workspace_id": "wF"})
	waitFor("ended", func() bool {
		_, has := s.events.current["herdr:wF:p4"]
		return !has && len(s.events.ring) == 5 && s.events.ring[4].State == "ended" && s.events.ring[4].Terminal == "herdr:wF:p4"
	})

	// A tab closes: herdr says tab_closed only; its panes end.
	smu.Lock()
	snap = map[string]any{}
	for k, v := range fakeSnapshot {
		snap[k] = v
	}
	snap["panes"] = fakeSnapshot["panes"].([]any)[:1]
	smu.Unlock()
	f.emit("tab_closed", map[string]any{"type": "tab_closed", "tab_id": "wF:t3", "workspace_id": "wF"})
	waitFor("tab closed", func() bool {
		for _, e := range s.events.ring[5:] {
			if e.State == "ended" && e.Terminal == "herdr:wF:p3" {
				return true
			}
		}
		return false
	})

	// herdr goes away: its terminals end; back: seeded again.
	smu.Lock()
	snap = fakeSnapshot
	smu.Unlock()
	f.on("session.snapshot", func(map[string]any) (any, *herdrError) {
		return nil, &herdrError{Code: "server_stopping", Message: "bye"}
	})
	f.dropSubs()
	waitFor("gone", func() bool {
		n := 0
		for _, e := range s.events.ring {
			if e.State == "ended" {
				n++
			}
		}
		return n >= 3 // p4 and p3 before, p2 now
	})
	f.on("session.snapshot", snapshotAnswer(fakeSnapshot))
	waitFor("back", func() bool { return s.events.current["herdr:wF:p2"].State == "blocked" })
}

func TestHerdrChat(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Claude Code's per-process file names its session: the pid comes from
	// pane.process_info, the folder from foreground_cwd.
	os.MkdirAll(filepath.Join(home, ".claude", "sessions"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude", "sessions", "4242.json"), []byte(`{"pid":4242,"sessionId":"abc-123","cwd":"/tmp/app"}`), 0o644)
	tr := filepath.Join(home, ".claude", "projects", "-tmp-app", "abc-123.jsonl")
	os.MkdirAll(filepath.Dir(tr), 0o755)
	os.WriteFile(tr, []byte(`{"type":"user","uuid":"1","parentUuid":null,"message":{"role":"user","content":"add a test"}}`+"\n"), 0o644)

	f := newFakeHerdr(t)
	f.on("pane.get", paneAnswer)
	f.on("pane.process_info", func(p map[string]any) (any, *herdrError) {
		return map[string]any{"type": "process_info", "process_info": map[string]any{
			"pane_id": p["pane_id"], "shell_pid": 100, "foreground_process_group_id": 4242,
			"foreground_processes": []any{
				map[string]any{"pid": 4242, "name": "node", "argv": []string{"node", "/opt/bin/claude"}, "cwd": "/tmp"},
			},
		}}, nil
	})
	f.on("pane.read", func(map[string]any) (any, *herdrError) {
		return map[string]any{"type": "pane_read", "read": map[string]any{"text": "> ", "truncated": false}}, nil
	})
	s := &server{hc: "/nonexistent/hc", herdrSock: f.sock}

	now, err := s.terminalNow(context.Background(), "herdr:wF:p2")
	if err != nil || now.fg.Pid != 4242 || now.fg.Cwd != "/tmp/app" || agentOf(now.fg) != "claude" || now.state != "blocked" {
		t.Fatalf("now: %+v %v", now, err)
	}
	code, out := call(s.chat, "GET", "/api/chat?terminal=herdr:wF:p2", "")
	if code != 200 || out["agent"] != "claude" || out["source"] != tr {
		t.Fatalf("chat: %d %v", code, out)
	}
	if items, _ := out["items"].([]any); len(items) != 1 {
		t.Errorf("items: %v", out["items"])
	}
	if p := f.took("pane.process_info"); len(p) == 0 || p[0]["pane_id"] != "wF:p2" {
		t.Errorf("process_info: %v", p)
	}
}

func TestHerdrForeground(t *testing.T) {
	cwd := "/x"
	procs := []herdrProcess{{Pid: 10, Name: "zsh", Argv: []string{"-zsh"}}, {Pid: 11, Name: "tego", Argv: []string{"tego"}, Cwd: &cwd}}
	if p, _ := herdrForeground(procs, 10); p.Pid != 11 {
		t.Errorf("the agent first: %+v", p)
	}
	if p, _ := herdrForeground(procs[:1], 10); p.Pid != 10 {
		t.Errorf("the leader: %+v", p)
	}
	if _, ok := herdrForeground(nil, 0); ok {
		t.Error("none")
	}
}

func TestHerdrControl(t *testing.T) {
	f := newFakeHerdr(t)
	f.on("tab.create", func(p map[string]any) (any, *herdrError) {
		return map[string]any{"type": "tab_created", "tab": map[string]any{"tab_id": "wF:t5"}, "root_pane": map[string]any{"pane_id": "wF:p9"}}, nil
	})
	f.on("tab.close", okAnswer)
	f.on("pane.close", okAnswer)
	f.on("tab.focus", func(p map[string]any) (any, *herdrError) {
		return nil, &herdrError{Code: "tab_not_found", Message: "tab wF:t9 not found"}
	})
	s := &server{herdrSock: f.sock}

	code, out := call(s.herdrControl, "POST", "/api/herdr", `{"method":"tab.create","params":{"workspace_id":"wF","cwd":"/tmp","label":"x","focus":false}}`)
	if r, _ := out["result"].(map[string]any); code != 200 || r["type"] != "tab_created" {
		t.Errorf("tab.create: %d %v", code, out)
	}
	if p := f.took("tab.create"); len(p) != 1 || !reflect.DeepEqual(p[0], map[string]any{"workspace_id": "wF", "cwd": "/tmp", "label": "x", "focus": false}) {
		t.Errorf("tab.create params: %v", p)
	}

	f.reset()
	bad := []string{
		`{"method":"pane.send_text","params":{"pane_id":"wF:p2","text":"rm -rf ~\r"}}`, // not allowed
		`{"method":"workspace.close","params":{"workspace_id":"wF"}}`,
		`{"method":"server.stop","params":{}}`,
		`{"method":"tab.create","params":{"command":"sh"}}`,  // a key the method doesn't take
		`{"method":"tab.close","params":{"tab_id":"wF:t2"}}`, // not confirmed
		`{"method":"pane.close","params":{"pane_id":"wF:p2"},"confirmed":false}`,
		`{"method":"tab.rename","params":{"tab_id":"wF:t2"}}`, // no label
		`{"method":"tab.focus","params":{"tab_id":"../x"}}`,
		`{"method":"tab.focus","params":{"tab_id":7}}`,
		`not json`,
	}
	for _, b := range bad {
		if code, out := call(s.herdrControl, "POST", "/api/herdr", b); code != 400 {
			t.Errorf("%s: %d %v", b, code, out)
		}
	}
	if m := f.methods(); len(m) != 0 {
		t.Errorf("herdr called: %v", m)
	}

	// Confirmed closes go through; a pane by its terminal address too.
	if code, out := call(s.herdrControl, "POST", "/api/herdr", `{"method":"tab.close","params":{"tab_id":"wF:t2"},"confirmed":true}`); code != 200 || out["result"] == nil {
		t.Errorf("tab.close: %d %v", code, out)
	}
	if code, _ := call(s.herdrControl, "POST", "/api/herdr", `{"method":"pane.close","params":{"pane_id":"herdr:wF:p2"},"confirmed":true}`); code != 200 {
		t.Errorf("pane.close: %d", code)
	}
	if p := f.took("pane.close"); len(p) != 1 || p[0]["pane_id"] != "wF:p2" {
		t.Errorf("pane.close params: %v", p)
	}
	// herdr's errors: its code, a fitting status.
	if code, out := call(s.herdrControl, "POST", "/api/herdr", `{"method":"tab.focus","params":{"tab_id":"wF:t9"}}`); code != 404 || out["code"] != "tab_not_found" {
		t.Errorf("not found: %d %v", code, out)
	}
	s.herdrSock = filepath.Join(t.TempDir(), "none.sock")
	if code, _ := call(s.herdrControl, "POST", "/api/herdr", `{"method":"tab.focus","params":{"tab_id":"wF:t2"}}`); code != 503 {
		t.Errorf("herdr off: %d", code)
	}
}

func TestHerdrTask(t *testing.T) {
	dir := t.TempDir()
	f := newFakeHerdr(t)
	f.on("session.snapshot", snapshotAnswer(fakeSnapshot))
	f.on("tab.create", func(p map[string]any) (any, *herdrError) {
		return map[string]any{"type": "tab_created", "tab": map[string]any{"tab_id": p["workspace_id"].(string) + ":t5", "workspace_id": p["workspace_id"]},
			"root_pane": map[string]any{"pane_id": p["workspace_id"].(string) + ":p9"}}, nil
	})
	f.on("pane.send_input", okAnswer)
	herdrApp, appCalls := fakeTool(t, "herdr-app", `echo '{"space":"sp1","tab":"tb9","terminal":"t7"}'`)
	s := &server{herdr: herdrApp, hc: "/bin/false", herdrSock: f.sock, programsFile: "/nonexistent", hasTool: func(n string) bool { return n == "claude" || n == "tego" }}
	post := func(body any) (int, map[string]string) {
		b, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		s.task(rec, httptest.NewRequest("POST", "/api/task", strings.NewReader(string(b))))
		var out map[string]string
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	prompt := `fix it; it's "quoted" $(x)`
	code, out := post(map[string]string{"cwd": dir, "agent": "claude", "prompt": prompt, "backend": "herdr"})
	if code != 200 || out["terminal"] != "herdr:wF:p9" || out["tab"] != "wF:t5" || out["space"] != "wF" || out["prompt"] != "argv" || out["agent"] != "claude" {
		t.Fatalf("task: %d %v", code, out)
	}
	if p := f.took("tab.create"); len(p) != 1 || !reflect.DeepEqual(p[0], map[string]any{"workspace_id": "wF", "cwd": dir, "focus": false}) {
		t.Errorf("tab.create: %v", p)
	}
	in := f.took("pane.send_input")
	if len(in) != 1 || in[0]["pane_id"] != "wF:p9" || in[0]["text"] != `claude 'fix it; it'\''s "quoted" $(x)'` || fmt.Sprint(in[0]["keys"]) != "[enter]" {
		t.Errorf("send_input: %v", in)
	}
	if c := readCalls(t, appCalls); c != "" {
		t.Errorf("herdr-app called: %s", c)
	}

	// Another workspace; the Herdr app by default.
	f.reset()
	if code, out := post(map[string]string{"cwd": dir, "agent": "tego", "backend": "herdr", "workspace": "w2"}); code != 200 || out["terminal"] != "herdr:w2:p9" || out["prompt"] != "none" {
		t.Errorf("workspace: %d %v", code, out)
	}
	if code, out := post(map[string]string{"cwd": dir, "agent": "tego"}); code != 200 || out["terminal"] != "t7" {
		t.Errorf("app: %d %v", code, out)
	}

	f.reset()
	for _, b := range []map[string]string{
		{"cwd": dir, "agent": "claude", "backend": "tmux"},
		{"cwd": dir, "agent": "claude", "backend": "herdr", "machine": "box"},
		{"cwd": dir, "agent": "claude", "backend": "herdr", "workspace": "wF:t2"},
		{"cwd": dir, "agent": "claude", "workspace": "wF"},
	} {
		if code, _ := post(b); code != 400 {
			t.Errorf("%v: %d", b, code)
		}
	}
	if m := f.methods(); len(m) != 0 {
		t.Errorf("herdr called: %v", m)
	}
}

func TestHerdrResume(t *testing.T) {
	h := newSessionHome(t)
	h.fill(t)
	f := newFakeHerdr(t)
	f.on("session.snapshot", snapshotAnswer(fakeSnapshot))
	f.on("tab.create", func(p map[string]any) (any, *herdrError) {
		return map[string]any{"type": "tab_created", "tab": map[string]any{"tab_id": p["workspace_id"].(string) + ":t5", "workspace_id": p["workspace_id"]},
			"root_pane": map[string]any{"pane_id": p["workspace_id"].(string) + ":p9"}}, nil
	})
	f.on("pane.send_input", okAnswer)
	herdrApp, appCalls := fakeTool(t, "herdr-app", `echo '{"space":"sp1","tab":"tb9","terminal":"t21"}'`)
	s := h.server(t, map[string]string{"t5": h.paths[tegoB]})
	s.herdr = herdrApp
	s.herdrSock = f.sock
	post := func(body any) (int, map[string]string) {
		b, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		s.resume(rec, httptest.NewRequest("POST", "/api/resume", strings.NewReader(string(b))))
		var out map[string]string
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	code, out := post(map[string]string{"agent": "tego", "id": tegoA, "backend": "herdr", "workspace": "w2"})
	if code != 200 || out["terminal"] != "herdr:w2:p9" || out["tab"] != "w2:t5" || out["cwd"] != h.api {
		t.Fatalf("resume: %d %v", code, out)
	}
	if p := f.took("tab.create"); len(p) != 1 || !reflect.DeepEqual(p[0], map[string]any{"workspace_id": "w2", "cwd": h.api, "focus": false}) {
		t.Errorf("tab.create: %v", p)
	}
	if in := f.took("pane.send_input"); len(in) != 1 || in[0]["text"] != "tego --resume "+tegoA || fmt.Sprint(in[0]["keys"]) != "[enter]" {
		t.Errorf("send_input: %v", in)
	}
	if c := readCalls(t, appCalls); c != "" {
		t.Errorf("herdr-app called: %s", c)
	}

	// Its focused workspace by default.
	f.reset()
	if code, out := post(map[string]string{"agent": "claude", "id": claudeA, "backend": "herdr"}); code != 200 || out["terminal"] != "herdr:wF:p9" {
		t.Errorf("focused: %d %v", code, out)
	}

	f.reset()
	for _, b := range []map[string]string{
		{"agent": "tego", "id": tegoA, "backend": "tmux"},
		{"agent": "tego", "id": tegoA, "workspace": "wF"},                      // a workspace without the CLI
		{"agent": "tego", "id": tegoA, "backend": "herdr", "workspace": "x y"}, // not a workspace id
	} {
		if code, _ := post(b); code != 400 {
			t.Errorf("%v: %d", b, code)
		}
	}
	if m := f.methods(); len(m) != 0 {
		t.Errorf("herdr called: %v", m)
	}
}

func TestShellJoin(t *testing.T) {
	if got := shellJoin([]string{"claude", "--model", "opus", "it's", "", "a b"}); got != `claude --model opus 'it'\''s' '' 'a b'` {
		t.Errorf("%s", got)
	}
}

func TestHerdrUpload(t *testing.T) {
	up := t.TempDir()
	s := &server{shotDir: t.TempDir(), uploadDir: up, hc: "/nonexistent/hc"}
	put := func() map[string]any {
		rec := httptest.NewRecorder()
		s.upload(rec, httptest.NewRequest("POST", "/api/upload?terminal=herdr:wF:p2&name=IMG%201.jpg", strings.NewReader("jpegdata")))
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != 200 {
			t.Fatalf("upload: %d %s", rec.Code, rec.Body)
		}
		return out
	}
	out := put()
	want := filepath.Join(up, "wF-p2", "IMG_1.jpg")
	if out["path"] != want || out["bytes"] != float64(8) {
		t.Errorf("upload: %v", out)
	}
	if b, _ := os.ReadFile(want); string(b) != "jpegdata" {
		t.Errorf("file: %q", b)
	}
	// The same name again: kept apart.
	if again := put(); again["path"] == want || filepath.Dir(again["path"].(string)) != filepath.Dir(want) {
		t.Errorf("again: %v", again)
	}
}
