package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// sessionHome: a home with tego and Claude Code sessions in two projects.
type sessionHome struct {
	home, api, web, gone string
	paths                map[string]string // by session id
}

func newSessionHome(t *testing.T) *sessionHome {
	t.Helper()
	old := tempRoots
	tempRoots = []string{"/tmp", "/private/tmp"} // the test's home is in the temp folder
	t.Cleanup(func() { tempRoots = old })

	h := &sessionHome{home: t.TempDir(), paths: map[string]string{}}
	h.api = filepath.Join(h.home, "Code", "api")
	h.web = filepath.Join(h.home, "Code", "web")
	h.gone = filepath.Join(h.home, "Code", "gone")
	os.MkdirAll(h.api, 0o755)
	os.MkdirAll(h.web, 0o755)
	return h
}

func jsonl(lines ...any) string {
	var b strings.Builder
	for _, l := range lines {
		if s, ok := l.(string); ok {
			b.WriteString(s)
		} else {
			j, _ := json.Marshal(l)
			b.Write(j)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func (h *sessionHome) write(t *testing.T, agent, cwd, id, body string, age time.Duration) {
	t.Helper()
	// Both name the folder by its real path (the temp folder is behind a symlink on macOS).
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	dir := filepath.Join(h.home, ".tego", "sessions", tegoSlug(cwd))
	if agent == "claude" {
		dir = filepath.Join(h.home, ".claude", "projects", claudeSlug(cwd))
	}
	os.MkdirAll(dir, 0o700)
	p := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-age)
	os.Chtimes(p, at, at)
	h.paths[id] = p
}

// The transcripts' messages, with their keys in the agents' order.
type msgBody struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type textBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func tegoMsg(role, text string) any {
	return struct {
		Type    string  `json:"type"`
		ID      string  `json:"id"`
		Message msgBody `json:"message"`
	}{"message", "m1", msgBody{role, []textBlock{{"text", text}}}}
}

func tegoHeader(id, cwd string) map[string]any {
	return map[string]any{"type": "header", "id": id, "cwd": cwd}
}

func claudeUser(cwd string, content any, meta bool) any {
	return struct {
		Parent      *string `json:"parentUuid"`
		IsSidechain bool    `json:"isSidechain"`
		Type        string  `json:"type"`
		Message     msgBody `json:"message"`
		IsMeta      bool    `json:"isMeta"`
		Cwd         string  `json:"cwd"`
	}{nil, false, "user", msgBody{"user", content}, meta, cwd}
}

const (
	tegoA     = "20261101-083706-ee31c7"
	tegoB     = "20261031-120000-0a0b0c"
	tegoNil   = "20261031-130000-ffffff"
	tegoBig   = "20261030-090000-123456"
	tegoOld   = "20261001-090000-abcdef"
	tegoSkill = "20261031-100000-5c5c5c"
	claudeA   = "78759a30-f50b-498a-9f99-8c319953813a"
	claudeE   = "f0d71145-8209-4082-96b6-c9647e912c20"
)

func (h *sessionHome) fill(t *testing.T) {
	// tego, with a title after the first message.
	h.write(t, "tego", h.api, tegoA, jsonl(
		tegoHeader(tegoA, h.api),
		tegoMsg("user", "fix the  login\nbug"),
		tegoMsg("user", "<system-reminder>Environment: today</system-reminder>"),
		`{"type":"title","title":"Login bug fix"}`,
		tegoMsg("assistant", "Done."),
		tegoMsg("user", "now run the tests"),
	), time.Minute)
	// No title yet; typed by hc from another terminal.
	h.write(t, "tego", h.web, tegoB, jsonl(
		tegoHeader(tegoB, h.web),
		tegoMsg("user", `<hc from="t7">check the flaky test</hc>`),
	), time.Hour)
	// A skill invoked, then what you asked: titled by what you asked.
	h.write(t, "tego", h.web, tegoSkill, jsonl(
		tegoHeader(tegoSkill, h.web),
		tegoMsg("user", "<skill name=\"frontend-design\" path=\"/x/SKILL.md\">\n# Frontend\nlots\n</skill>\n\nredesign the billing page"),
	), 90*time.Minute)
	// Opened and left: nothing said.
	h.write(t, "tego", h.api, tegoNil, jsonl(tegoHeader(tegoNil, h.api)), 30*time.Second)
	// Long: renamed at the end, past what is read of the start.
	big := []any{tegoHeader(tegoBig, h.web), tegoMsg("user", "write the parser"), `{"type":"title","title":"Parser"}`}
	for i := 0; i < 400; i++ {
		big = append(big, tegoMsg("assistant", strings.Repeat("x", 1000)))
	}
	big = append(big, `{"type":"title","title":"Parser, renamed"}`)
	h.write(t, "tego", h.web, tegoBig, jsonl(big...), 2*time.Hour)
	// In a folder deleted since.
	h.write(t, "tego", h.gone, tegoOld, jsonl(tegoHeader(tegoOld, h.gone), tegoMsg("user", "old work")), 3*time.Hour)

	// Claude Code: a /clear, then what you asked; its title, then yours.
	h.write(t, "claude", h.api, claudeA, jsonl(
		map[string]any{"type": "mode", "mode": "normal", "sessionId": claudeA},
		claudeUser(h.api, "<command-name>/clear</command-name>", false),
		claudeUser(h.api, "Caveat: The messages below were generated", true),
		claudeUser(h.api, []map[string]any{{"type": "text", "text": "add a dark mode"}}, false),
		map[string]any{"type": "ai-title", "aiTitle": "Dark mode", "sessionId": claudeA},
		claudeUser(h.api, []map[string]any{{"type": "tool_result", "tool_use_id": "x", "content": "ok"}}, false),
		map[string]any{"type": "custom-title", "customTitle": "Dark mode for settings", "sessionId": claudeA},
	), 10*time.Minute)
	// Claude Code opened and left.
	h.write(t, "claude", h.api, claudeE, jsonl(
		map[string]any{"type": "mode", "mode": "normal", "sessionId": claudeE},
		map[string]any{"type": "last-prompt", "sessionId": claudeE},
	), 20*time.Second)
	// Not sessions: a stray file, a subagent's transcript.
	h.write(t, "claude", h.api, "agent-1234", jsonl(claudeUser(h.api, "x y", false)), 0)
	h.write(t, "tego", h.api, "notes", "x\n", 0)
}

func (h *sessionHome) server(t *testing.T, running map[string]string) *server {
	hc, _ := fakeTool(t, "hc", `echo '{"terminals":[{"address":"t5","state":"live"},{"address":"t6","state":"finished"},{"address":"t8","state":"live"}]}'`)
	return &server{
		hc: hc, herdr: "/bin/false", home: h.home, programsFile: "/nonexistent",
		hasTool: func(n string) bool { return n == "tego" || n == "claude" },
		transcriptOf: func(ctx context.Context, terminal string) string {
			if terminal == "t6" {
				t.Error("asked a finished terminal for its transcript")
			}
			return running[terminal]
		},
	}
}

func getSessions(t *testing.T, s *server, query string) (int, []Session) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.sessions(rec, httptest.NewRequest("GET", "/api/sessions"+query, nil))
	var out struct{ Sessions []Session }
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out.Sessions
}

func sessionIDs(list []Session) []string {
	var out []string
	for _, s := range list {
		out = append(out, s.ID)
	}
	return out
}

func TestSessions(t *testing.T) {
	h := newSessionHome(t)
	h.fill(t)
	s := h.server(t, map[string]string{"t5": h.paths[claudeA], "t8": "/elsewhere/x.jsonl"})

	code, list := getSessions(t, s, "")
	if code != 200 {
		t.Fatal(code)
	}
	// Newest first; no empty sessions, none in a folder gone since, no strays.
	if want := []string{tegoA, claudeA, tegoB, tegoSkill, tegoBig}; !reflect.DeepEqual(sessionIDs(list), want) {
		t.Fatalf("all:\n got %q\nwant %q", sessionIDs(list), want)
	}
	byID := map[string]Session{}
	for _, x := range list {
		byID[x.ID] = x
	}
	titles := map[string]string{
		tegoA:     "Login bug fix",
		claudeA:   "Dark mode for settings", // yours over its own
		tegoB:     "check the flaky test",   // the first message, without hc's tag
		tegoBig:   "Parser, renamed",        // the rename at the end
		tegoSkill: "redesign the billing page",
	}
	for id, want := range titles {
		if got := byID[id].Title; got != want {
			t.Errorf("%s: title %q, want %q", id, got, want)
		}
	}
	a := byID[tegoA]
	if a.Agent != "tego" || a.Cwd != h.api || a.Messages != 2 || a.Estimate || a.Size == 0 || a.Running != "" {
		t.Errorf("tego session: %+v", a)
	}
	if time.Since(time.UnixMilli(a.Time)) > 2*time.Minute {
		t.Errorf("time %d", a.Time)
	}
	if c := byID[claudeA]; c.Agent != "claude" || c.Messages != 1 || c.Running != "t5" {
		t.Errorf("claude session: %+v", c)
	}
	if b := byID[tegoBig]; b.Estimate || b.Messages != 1 { // counted to the end
		t.Errorf("long session: %+v", b)
	}

	// One folder, by another spelling too.
	if _, list := getSessions(t, s, "?cwd="+h.api+"/"); !reflect.DeepEqual(sessionIDs(list), []string{tegoA, claudeA}) {
		t.Errorf("cwd: %q", sessionIDs(list))
	}
	link := filepath.Join(h.home, "api-link")
	os.Symlink(h.api, link)
	if _, list := getSessions(t, s, "?cwd="+link); !reflect.DeepEqual(sessionIDs(list), []string{tegoA, claudeA}) {
		t.Errorf("cwd through a symlink: %q", sessionIDs(list))
	}
	if _, list := getSessions(t, s, "?cwd="+h.api+"&agent=claude"); !reflect.DeepEqual(sessionIDs(list), []string{claudeA}) {
		t.Errorf("cwd and agent: %q", sessionIDs(list))
	}
	// Asked for by folder, a gone or temporary folder's sessions are there.
	if _, list := getSessions(t, s, "?cwd="+h.gone); !reflect.DeepEqual(sessionIDs(list), []string{tegoOld}) {
		t.Errorf("gone folder: %q", sessionIDs(list))
	}
	if _, list := getSessions(t, s, "?limit=2"); len(list) != 2 {
		t.Errorf("limit: %q", sessionIDs(list))
	}
	for _, q := range []string{"?cwd=relative/x", "?agent=codex", "?agent=../x"} {
		if code, _ := getSessions(t, s, q); code != 400 {
			t.Errorf("%s: %d, want 400", q, code)
		}
	}

	// Claude Code not installed: its sessions aren't offered.
	s.hasTool = func(n string) bool { return n == "tego" }
	if _, list := getSessions(t, s, "?cwd="+h.api); !reflect.DeepEqual(sessionIDs(list), []string{tegoA}) {
		t.Errorf("without claude: %q", sessionIDs(list))
	}
}

func TestSessionsTimeBound(t *testing.T) {
	h := newSessionHome(t)
	h.fill(t)
	s := h.server(t, nil)
	old := sessionScan
	sessionScan = 0
	t.Cleanup(func() { sessionScan = old })
	rec := httptest.NewRecorder()
	s.sessions(rec, httptest.NewRequest("GET", "/api/sessions", nil))
	var out struct {
		Sessions []Session
		Partial  bool
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if !out.Partial || len(out.Sessions) != 0 {
		t.Errorf("out of time: %s", rec.Body.String())
	}
}

func TestResume(t *testing.T) {
	h := newSessionHome(t)
	h.fill(t)
	os.WriteFile(filepath.Join(h.home, ".tego", "config.json"), []byte(`{"model":"anthropic/claude-opus-5-5"}`), 0o600)
	herdr, calls := fakeTool(t, "herdr-app", `echo '{"space":"sp1","tab":"tb9","terminal":"t21"}'`)
	s := h.server(t, map[string]string{"t5": h.paths[tegoB]})
	s.herdr = herdr

	post := func(body any) (int, map[string]string) {
		b, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		s.resume(rec, httptest.NewRequest("POST", "/api/resume", strings.NewReader(string(b))))
		var out map[string]string
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	tabNew := func(folder string, argv ...string) string {
		b, _ := json.Marshal(map[string]any{"folder": folder, "argv": argv})
		return "do tab.new " + string(b)
	}

	cases := []struct {
		body map[string]string
		want string
	}{
		{map[string]string{"agent": "tego", "id": tegoA}, tabNew(h.api, "tego", "--resume", tegoA)},
		{map[string]string{"agent": "claude", "id": claudeA}, tabNew(h.api, "claude", "--resume", claudeA)},
		{map[string]string{"agent": "tego", "id": tegoBig, "model": "anthropic/claude-opus-5-5", "thinking": "high"},
			tabNew(h.web, "tego", "--model", "anthropic/claude-opus-5-5", "--thinking", "high", "--resume", tegoBig)},
		{map[string]string{"agent": "claude", "id": claudeA, "thinking": "max"}, tabNew(h.api, "claude", "--effort", "max", "--resume", claudeA)},
	}
	for _, c := range cases {
		os.Remove(calls)
		code, out := post(c.body)
		if code != 200 || out["terminal"] != "t21" || out["tab"] != "tb9" || out["agent"] != c.body["agent"] {
			t.Errorf("%v: %d %v", c.body, code, out)
		}
		if got := readCalls(t, calls); got != c.want {
			t.Errorf("%v: herdr-app called with\n %s\nwant\n %s", c.body, got, c.want)
		}
	}
	if _, out := post(map[string]string{"agent": "tego", "id": tegoA}); out["cwd"] != h.api {
		t.Errorf("cwd: %v", out)
	}

	bad := []struct {
		body map[string]string
		code int
	}{
		{map[string]string{"agent": "tego", "id": "20991231-235959-000000"}, 404}, // no such session
		{map[string]string{"agent": "claude", "id": "00000000-0000-0000-0000-000000000000"}, 404},
		{map[string]string{"agent": "tego", "id": "../../../etc/passwd"}, 400},
		{map[string]string{"agent": "tego", "id": "Users-akbar/" + tegoA}, 400},
		{map[string]string{"agent": "tego", "id": "*"}, 400},
		{map[string]string{"agent": "tego", "id": tegoA + ".jsonl"}, 400},
		{map[string]string{"agent": "claude", "id": "../" + claudeA}, 400},
		{map[string]string{"agent": "tego", "id": ""}, 400},
		{map[string]string{"agent": "tego", "id": claudeA}, 400}, // another agent's id
		{map[string]string{"agent": "codex", "id": claudeA}, 400},
		{map[string]string{"agent": "sh -c ls", "id": tegoA}, 400},
		{map[string]string{"agent": "tego", "id": tegoNil}, 400},                      // nothing said
		{map[string]string{"agent": "claude", "id": claudeE}, 400},                    // nothing said
		{map[string]string{"agent": "tego", "id": tegoOld}, 409},                      // its folder is gone
		{map[string]string{"agent": "tego", "id": tegoA, "model": "gpt-9"}, 400},      // not offered
		{map[string]string{"agent": "tego", "id": tegoA, "thinking": "a lot"}, 400},   // not a level
		{map[string]string{"agent": "claude", "id": claudeA, "thinking": "off"}, 400}, // not Claude's
	}
	for _, b := range bad {
		os.Remove(calls)
		if code, out := post(b.body); code != b.code {
			t.Errorf("%v: %d %v, want %d", b.body, code, out, b.code)
		}
		if got := readCalls(t, calls); got != "" {
			t.Errorf("%v: herdr-app called with %q", b.body, got)
		}
	}

	// Running now: open that terminal instead.
	os.Remove(calls)
	if code, out := post(map[string]string{"agent": "tego", "id": tegoB}); code != 409 || out["terminal"] != "t5" {
		t.Errorf("running: %d %v", code, out)
	}
	if got := readCalls(t, calls); got != "" {
		t.Errorf("running: herdr-app called with %q", got)
	}

	// Not installed.
	s.hasTool = func(n string) bool { return n == "tego" }
	if code, _ := post(map[string]string{"agent": "claude", "id": claudeA}); code != 400 {
		t.Errorf("claude not installed: %d", code)
	}

	// programs.json's flags for tego come along.
	progs := filepath.Join(t.TempDir(), "programs.json")
	os.WriteFile(progs, []byte(`{"programs":{"tego":{"name":"tego","run":["tego","--permissions","edits"]}}}`), 0o644)
	s.programsFile = progs
	os.Remove(calls)
	if code, _ := post(map[string]string{"agent": "tego", "id": tegoA}); code != 200 {
		t.Errorf("with programs.json: %d", code)
	}
	if got, want := readCalls(t, calls), tabNew(h.api, "tego", "--permissions", "edits", "--resume", tegoA); got != want {
		t.Errorf("with programs.json:\n %s\nwant\n %s", got, want)
	}
}

func TestSessionsCountLater(t *testing.T) {
	h := newSessionHome(t)
	h.fill(t)
	s := h.server(t, nil)
	old := sessionCount
	sessionCount = 0
	t.Cleanup(func() { sessionCount = old })
	_, list := getSessions(t, s, "?cwd="+h.web)
	for _, x := range list {
		if x.ID == tegoBig && (!x.Estimate || x.Messages != 1) { // seen in the start: at least 1
			t.Errorf("no time to count: %+v", x)
		}
	}
}

func TestCountPrompts(t *testing.T) {
	old := countChunk
	countChunk = 300
	t.Cleanup(func() { countChunk = old })
	long := strings.Repeat("y", 1000) // longer than a chunk
	lines := []any{
		tegoHeader(tegoA, "/x"),
		tegoMsg("user", "one"),
		tegoMsg("assistant", long),
		tegoMsg("user", "<system-reminder>not yours</system-reminder>"),
		tegoMsg("user", "two "+long), // long and yours
		map[string]any{"type": "message", "message": map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "text": long}}}},
		tegoMsg("user", "three"),
		`{"type":"title","title":"Late title"}`,
	}
	p := filepath.Join(t.TempDir(), tegoA+".jsonl")
	body := jsonl(lines...)
	os.WriteFile(p, []byte(body), 0o600)
	file := func() sessionFile {
		info, _ := os.Stat(p)
		return sessionFile{agent: "tego", id: tegoA, path: p, size: info.Size()}
	}
	if n, title, done := countPrompts(file(), time.Now().Add(time.Minute)); n != 3 || !done || title != "Late title" {
		t.Errorf("count %d %q %v, want 3, the late title", n, title, done)
	}
	// It grows, with a line half written: counted on from where it stopped.
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(jsonl(tegoMsg("assistant", long), tegoMsg("user", "four")) + `{"type":"message","message":{"role":"user","content":[{"type":"text","text":"fi`)
	f.Close()
	if n, _, done := countPrompts(file(), time.Now().Add(time.Minute)); n != 4 || !done {
		t.Errorf("grown: %d %v, want 4", n, done)
	}
	f, _ = os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("ve\"}]}}\n")
	f.Close()
	if n, _, _ := countPrompts(file(), time.Now().Add(time.Minute)); n != 5 {
		t.Errorf("finished line: %d, want 5", n)
	}
	// Out of time: what it got so far.
	os.WriteFile(p, []byte(body[:len(body)/2]), 0o600) // rewritten shorter: counted again
	if n, _, done := countPrompts(file(), time.Now().Add(-time.Second)); done || n != 0 {
		t.Errorf("no time: %d %v", n, done)
	}
}

func TestCleanTitle(t *testing.T) {
	cases := map[string]string{
		"  fix\n\tthe   bug ":                  "fix the bug",
		`<hc from="t7">run the tests</hc>`:     "run the tests",
		`<hc from="t7"> two\nlines </hc>`:      `two\nlines`,
		strings.Repeat("é", sessionTitleMax+5): strings.Repeat("é", sessionTitleMax-1) + "…",
	}
	for in, want := range cases {
		if got := cleanTitle(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}
