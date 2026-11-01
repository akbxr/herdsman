package main

// Checks against a real herdr CLI server, skipped unless asked.
//
// Read-only, on any session (the default one is fine): every request goes
// through a guard that lets only reading methods reach herdr and fails the
// test on anything else.
//
//	HERDR_REMOTE_LIVE_HERDR=~/.config/herdr/herdr.sock go test -run TestHerdrLiveRead -v
//
// Changing things, on a throwaway session only (never the default one):
//
//	herdr --session hrcli-test server &
//	HERDR_REMOTE_LIVE_HERDR_SCRATCH=~/.config/herdr/sessions/hrcli-test/herdr.sock go test -run TestHerdrLiveScratch -v
//	herdr session stop hrcli-test && herdr session delete hrcli-test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

var herdrReadOnly = map[string]bool{
	"session.snapshot": true, "pane.get": true, "pane.list": true, "pane.read": true, "pane.process_info": true,
	"tab.get": true, "tab.list": true, "workspace.get": true, "workspace.list": true, "agent.list": true,
	"agent.get": true, "agent.read": true, "events.subscribe": true,
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, rest)
	}
	return p
}

// readOnlyGuard: a socket that passes reading requests on to real and
// refuses (and reports) the rest.
func readOnlyGuard(t *testing.T, real string) (string, func() []string) {
	dir, _ := os.MkdirTemp("/tmp", "hrg")
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "g.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var seen = make(chan string, 1024)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				rd := bufio.NewReader(c)
				line, err := rd.ReadBytes('\n')
				if err != nil {
					return
				}
				var req struct{ ID, Method string }
				json.Unmarshal(line, &req)
				seen <- req.Method
				if !herdrReadOnly[req.Method] {
					t.Errorf("REFUSED non-read-only %s", req.Method)
					b, _ := json.Marshal(map[string]any{"id": req.ID, "error": map[string]any{"code": "refused", "message": "read-only guard"}})
					c.Write(append(b, '\n'))
					return
				}
				up, err := net.Dial("unix", real)
				if err != nil {
					return
				}
				defer up.Close()
				up.Write(line)
				go func() { buf := make([]byte, 4096); rd.Read(buf); c.Close(); up.Close() }() // our side hung up
				buf := make([]byte, 64<<10)
				for {
					n, err := up.Read(buf)
					if n > 0 {
						if _, werr := c.Write(buf[:n]); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}(c)
		}
	}()
	return sock, func() []string {
		var out []string
		for {
			select {
			case m := <-seen:
				out = append(out, m)
			default:
				return out
			}
		}
	}
}

func TestHerdrLiveRead(t *testing.T) {
	real := expandHome(os.Getenv("HERDR_REMOTE_LIVE_HERDR"))
	if real == "" {
		t.Skip("set HERDR_REMOTE_LIVE_HERDR to a herdr socket")
	}
	guard, methods := readOnlyGuard(t, real)
	appDown, _ := fakeTool(t, "herdr-app", `exit 1`)
	s := &server{herdr: appDown, hc: "/nonexistent/hc", herdrSock: guard, events: newEventHub("/nonexistent/hc")}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// /api/state with the Herdr app down: the snapshot alone.
	rec := httptest.NewRecorder()
	s.state(rec, httptest.NewRequest("GET", "/api/state", nil))
	var st map[string]json.RawMessage
	json.Unmarshal(rec.Body.Bytes(), &st)
	var snap herdrSnapshot
	if rec.Code != 200 || json.Unmarshal(st["herdr"], &snap) != nil || len(snap.Panes) == 0 {
		t.Fatalf("state: %d %.300s", rec.Code, rec.Body)
	}
	t.Logf("state: %d workspaces, %d tabs, %d panes, %d agents; keys %v", len(snap.Workspaces), len(snap.Tabs), len(snap.Panes), len(snap.Agents), keysOf(st))

	// A pane with an agent if there is one, else the first.
	pane := snap.Panes[0].PaneID
	for _, a := range snap.Agents {
		pane = a.PaneID
		if str(a.Agent) == "tego" || str(a.Agent) == "claude" {
			break
		}
	}
	term := herdrAddr(pane)
	code, out := call(s.read, "GET", "/api/read?terminal="+term+"&lines=0", "")
	text, _ := out["text"].(string)
	if code != 200 || text == "" {
		t.Errorf("read: %d %v", code, out)
	}
	t.Logf("read %s: %d lines on screen", term, strings.Count(text, "\n")+1)
	code, out = call(s.read, "GET", "/api/read?terminal="+term+"&lines=50", "")
	t.Logf("read with 50 lines of history: %d, %d lines", code, strings.Count(out["text"].(string), "\n")+1)

	now, err := s.terminalNow(ctx, term)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("now: name %q pid %d cwd %q state %q agent %q", now.fg.Name, now.fg.Pid, now.fg.Cwd, now.state, agentOf(now.fg))
	code, out = call(s.chat, "GET", "/api/chat?terminal="+term+"&limit=3", "")
	items, _ := out["items"].([]any)
	t.Logf("chat: %d agent %v source %v, %d items, model %v %v", code, out["agent"], out["source"], len(items), out["modelLabel"], out["thinking"])
	if agentOf(now.fg) != "" && (code != 200 || out["source"] == "") {
		t.Errorf("chat: %d %v", code, out["error"])
	}
	code, out = call(s.promptHandler, "GET", "/api/prompt?terminal="+term, "")
	t.Logf("prompt: %d %v", code, out)

	// The repaint stream: the size and the first paint; nothing typed.
	srv := httptest.NewServer(http.HandlerFunc(s.attach))
	defer srv.Close()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"?terminal="+term, nil)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetReadLimit(4 << 20)
	_, size, _ := conn.Read(ctx)
	_, paint, _ := conn.Read(ctx)
	t.Logf("attach: %s, first frame %d bytes", size, len(paint))
	if !strings.Contains(string(size), `"type":"size"`) || !strings.Contains(string(paint), "\x1b[2J") {
		t.Errorf("attach frames: %s / %.100q", size, paint)
	}
	conn.Close(websocket.StatusNormalClosure, "")

	// Events for a few seconds: seeded from the snapshot.
	ectx, ecancel := context.WithTimeout(ctx, 4*time.Second)
	s.herdrEvents(ectx)
	ecancel()
	s.events.mu.Lock()
	for term, ev := range s.events.current {
		t.Logf("current %s: %s %s (%s)", term, ev.App, ev.State, ev.Where)
	}
	t.Logf("events in 4 s: %d", len(s.events.ring))
	s.events.mu.Unlock()

	used := map[string]int{}
	for _, m := range methods() {
		used[m]++
	}
	t.Logf("methods sent to herdr: %v", used)
}

func keysOf(m map[string]json.RawMessage) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestHerdrLiveScratch(t *testing.T) {
	sock := expandHome(os.Getenv("HERDR_REMOTE_LIVE_HERDR_SCRATCH"))
	if sock == "" {
		t.Skip("set HERDR_REMOTE_LIVE_HERDR_SCRATCH to a throwaway session's socket")
	}
	if filepath.Clean(sock) == filepath.Clean(defaultHerdrSock()) {
		t.Fatal("not the default session: start a throwaway one")
	}
	dir := t.TempDir()
	progs := filepath.Join(dir, "programs.json")
	os.WriteFile(progs, []byte(`{"programs":{"echoer":{"name":"Echoer","run":["sh","-c","echo task-started; exec cat"]}}}`), 0o644)
	s := &server{herdr: "/nonexistent/herdr-app", hc: "/nonexistent/hc", herdrSock: sock, uploadDir: dir,
		programsFile: progs, hasTool: func(n string) bool { return n == "sh" }, events: newEventHub("/nonexistent/hc")}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	go s.herdrEvents(ctx)

	snap, err := s.herdrSnapshot(ctx)
	if err != nil || len(snap.Workspaces) == 0 {
		t.Fatalf("snapshot: %v %+v", err, snap)
	}
	ws := snap.Workspaces[0].WorkspaceID

	// A tab with a shell, through /api/herdr.
	code, out := call(s.herdrControl, "POST", "/api/herdr", `{"method":"tab.create","params":{"workspace_id":"`+ws+`","cwd":"/tmp","label":"hrcli","focus":false}}`)
	res, _ := out["result"].(map[string]any)
	root, _ := res["root_pane"].(map[string]any)
	tab, _ := res["tab"].(map[string]any)
	pane, _ := root["pane_id"].(string)
	if code != 200 || pane == "" {
		t.Fatalf("tab.create: %d %v", code, out)
	}
	t.Logf("/api/herdr tab.create → %v", out)
	term := herdrAddr(pane)
	waitScreen := func(what, want string) string {
		t.Helper()
		var text string
		for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
			_, out := call(s.read, "GET", "/api/read?terminal="+term+"&lines=20", "")
			text, _ = out["text"].(string)
			if strings.Contains(text, want) {
				return text
			}
		}
		t.Fatalf("%s: %q not on screen:\n%s", what, want, text)
		return ""
	}
	time.Sleep(time.Second) // the shell starts

	// send: typed (a shell isn't an agent), Enter pressed.
	if code, out := call(s.send, "POST", "/api/send", `{"terminal":"`+term+`","text":"echo h''i-from-send"}`); code != 200 || out["how"] != "typed" {
		t.Fatalf("send: %d %v", code, out)
	}
	waitScreen("send", "hi-from-send")
	// keys: text keys and named ones.
	if code, out := call(s.keys, "POST", "/api/keys", `{"terminal":"`+term+`","keys":["e","c","h","o","space","k","left","x","enter"]}`); code != 200 {
		t.Fatalf("keys: %d %v", code, out)
	}
	waitScreen("keys", "\nxk") // left went back over the k
	// interrupt: Ctrl-C at a shell.
	call(s.send, "POST", "/api/send", `{"terminal":"`+term+`","text":"sleep 30"}`)
	time.Sleep(500 * time.Millisecond)
	if code, out := call(s.interrupt, "POST", "/api/interrupt", `{"terminal":"`+term+`"}`); code != 200 || out["key"] != "ctrl+c" {
		t.Errorf("interrupt: %d %v", code, out)
	}
	call(s.send, "POST", "/api/send", `{"terminal":"`+term+`","text":"echo after-interrupt"}`)
	waitScreen("interrupt", "\nafter-interrupt")

	now, err := s.terminalNow(ctx, term)
	t.Logf("now: %+v %v", now, err)
	if err != nil || filepath.Base(now.fg.Name) == "" || !strings.HasSuffix(now.fg.Cwd, "tmp") {
		t.Errorf("now: %+v %v", now, err)
	}

	// The repaint stream: typing goes in, the screen comes back.
	srv := httptest.NewServer(http.HandlerFunc(s.attach))
	defer srv.Close()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"?terminal="+term, nil)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetReadLimit(4 << 20)
	_, size, _ := conn.Read(ctx)
	_, first, _ := conn.Read(ctx)
	t.Logf("attach: %s; first frame %d bytes", size, len(first))
	conn.Write(ctx, websocket.MessageBinary, []byte("echo attach-"))
	conn.Write(ctx, websocket.MessageBinary, []byte("ok\r"))
	var frames int
	got := false
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end) && !got; {
		rctx, rc := context.WithTimeout(ctx, 3*time.Second)
		typ, b, err := conn.Read(rctx)
		rc()
		if err != nil {
			break
		}
		if typ == websocket.MessageBinary {
			frames++
			got = strings.Contains(stripANSI(string(b)), "\r\nattach-ok")
		} else {
			t.Logf("attach text frame: %s", b)
		}
	}
	if !got {
		t.Errorf("attach: no repaint with the output after %d frames", frames)
	}
	t.Logf("attach: %d repaint frames until the output showed", frames)
	conn.Close(websocket.StatusNormalClosure, "")

	// A task in the throwaway session: a tab, the program typed, the prompt typed once it settles.
	code, tout := postJSON(s.task, `{"cwd":"/tmp","agent":"echoer","prompt":"hello task","backend":"herdr","workspace":"`+ws+`"}`)
	t.Logf("task → %d %v", code, tout)
	if code != 200 || !strings.HasPrefix(tout["terminal"].(string), "herdr:") {
		t.Fatalf("task: %d %v", code, tout)
	}
	taskTerm := tout["terminal"].(string)
	taskPane, _ := herdrPane(taskTerm)
	for end := time.Now().Add(30 * time.Second); time.Now().Before(end); time.Sleep(300 * time.Millisecond) {
		text, _, _ := s.herdrRead(ctx, taskPane, "visible", "text", 0)
		if strings.Count(text, "hello task") >= 2 { // typed, and cat's echo
			t.Logf("task screen:\n%s", strings.TrimSpace(text))
			break
		}
		if time.Now().Add(300 * time.Millisecond).After(end) {
			t.Errorf("task: prompt never arrived:\n%s", text)
		}
	}

	// Agent states, as an agent's herdr integration reports them: events in hc's shape.
	for _, st := range []string{"working", "blocked"} {
		if err := s.herdrDo(ctx, "pane.report_agent", map[string]any{"pane_id": pane, "source": "hrcli-test", "agent": "claude", "state": st}, nil); err != nil {
			t.Fatalf("report_agent: %v", err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	s.events.mu.Lock()
	cur := s.events.current[term]
	s.events.mu.Unlock()
	t.Logf("current %s: %+v", term, cur)
	if cur.State != "blocked" || cur.App != "claude" || !strings.HasSuffix(cur.Where, " / hrcli") {
		t.Errorf("events: %+v", cur)
	}
	s.herdrDo(ctx, "pane.release_agent", map[string]any{"pane_id": pane, "source": "hrcli-test", "agent": "claude"}, nil)

	// Upload: under the uploads folder.
	rec := httptest.NewRecorder()
	s.upload(rec, httptest.NewRequest("POST", "/api/upload?terminal="+term+"&name=a.txt", strings.NewReader("hi")))
	t.Logf("upload: %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))

	// Events: the task's tab and this one were seen.
	s.events.mu.Lock()
	t.Logf("events: %d, current %v", len(s.events.ring), s.events.current)
	s.events.mu.Unlock()

	// Close both tabs: not without confirmed, then with it.
	if code, _ := call(s.herdrControl, "POST", "/api/herdr", `{"method":"tab.close","params":{"tab_id":"`+tab["tab_id"].(string)+`"}}`); code != 400 {
		t.Errorf("unconfirmed close: %d", code)
	}
	for _, id := range []string{tab["tab_id"].(string), tout["tab"].(string)} {
		code, out := call(s.herdrControl, "POST", "/api/herdr", `{"method":"tab.close","params":{"tab_id":"`+id+`"},"confirmed":true}`)
		t.Logf("/api/herdr tab.close %s → %d %v", id, code, out)
		if code != 200 {
			t.Errorf("close %s: %d %v", id, code, out)
		}
	}
	time.Sleep(500 * time.Millisecond)
	s.events.mu.Lock()
	for _, e := range s.events.ring {
		t.Logf("event %+v", e)
	}
	s.events.mu.Unlock()
}

func postJSON(h http.HandlerFunc, body string) (int, map[string]any) {
	return call(h, "POST", "/", body)
}
