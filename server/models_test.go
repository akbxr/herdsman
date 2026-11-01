package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// modelHome: a home with tego's model cache and config.
func modelHome(t *testing.T) string {
	home := t.TempDir()
	dir := filepath.Join(home, ".tego")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{
		"anthropic": {"fetched": "2026-10-30T00:00:00Z", "models": ["claude-opus-5-5", "claude-sonnet-5-5", "claude-opus-9-1"]},
		"openai-codex": {"fetched": "2026-10-30T00:00:00Z", "models": ["gpt-6"]},
		"evil": {"models": ["x; rm -rf ~", "two\nlines", "ok-1"]}
	}`), 0o644)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"model": "openai-codex/gpt-6", "thinking": "high"}`), 0o644)
	return home
}

func ids(ms []ModelInfo) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

func TestModelChoices(t *testing.T) {
	s := &server{home: modelHome(t)}

	tego := s.modelChoices("tego")
	// The default model's provider first, then by name; odd ids left out.
	want := []string{"openai-codex/gpt-6", "anthropic/claude-opus-5-5", "anthropic/claude-sonnet-5-5", "anthropic/claude-opus-9-1", "evil/ok-1"}
	if got := ids(tego.Models); !reflect.DeepEqual(got, want) {
		t.Errorf("tego models:\n got %q\nwant %q", got, want)
	}
	if m := tego.Models[1]; m.Label != "Opus 5.5" || m.Group != "anthropic" {
		t.Errorf("tego model: %+v", m)
	}
	if !tego.Live || tego.ThinkingName != "Thinking" || len(tego.Thinking) != 6 || tego.Thinking[0].ID != "off" {
		t.Errorf("tego: %+v", tego)
	}

	claude := s.modelChoices("claude")
	got := ids(claude.Models)
	if got[0] != "default" || got[1] != "claude-opus-5-5" || got[len(got)-1] != "claude-opus-9-1" {
		t.Errorf("claude models: %q", got) // the picker's, then newer ones tego knows of
	}
	if strings.Count(strings.Join(got, " "), "claude-opus-5-5") != 1 {
		t.Errorf("claude models twice: %q", got)
	}
	if claude.ThinkingName != "Effort" || claude.hasLevel("off") || !claude.hasLevel("xhigh") {
		t.Errorf("claude: %+v", claude.Thinking)
	}

	codex := s.modelChoices("codex")
	if codex.Live || !codex.hasModel("gpt-5") || !codex.hasLevel("minimal") {
		t.Errorf("codex: %+v", codex)
	}
	if s.modelChoices("aider") != nil {
		t.Error("an unknown agent has choices")
	}

	rec := httptest.NewRecorder()
	s.modelsHandler(rec, httptest.NewRequest("GET", "/api/models?agent=tego", nil))
	var out ModelChoices
	if json.Unmarshal(rec.Body.Bytes(), &out); rec.Code != 200 || len(out.Models) != 5 {
		t.Errorf("GET tego: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	s.modelsHandler(rec, httptest.NewRequest("GET", "/api/models?agent=sh", nil))
	if rec.Code != 404 {
		t.Errorf("GET sh: %d", rec.Code)
	}
}

func TestModelLabel(t *testing.T) {
	for id, want := range map[string]string{
		"claude-opus-5-5":           "Opus 5.5",
		"claude-sonnet-5":           "Sonnet 5",
		"claude-haiku-4-5-20251001": "Haiku 4.5",
		"gpt-6":                     "gpt-6",
		"default":                   "Default",
	} {
		if got := modelLabel(id); got != want {
			t.Errorf("%s: %q, want %q", id, got, want)
		}
	}
}

func TestCheckChoice(t *testing.T) {
	s := &server{home: modelHome(t)}
	c := s.modelChoices("tego")
	for _, ok := range [][2]string{{"anthropic/claude-opus-5-5", ""}, {"", "xhigh"}, {"openai-codex/gpt-6", "off"}} {
		if err := c.check(ok[0], ok[1]); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range [][2]string{
		{"anthropic/claude-opus-0", ""}, // not offered
		{"", "huge"},                    // no such level
		{"evil/x; rm -rf ~", ""},        // never a command
		{"", "low\n/quit"},              // never a second line
		{"anthropic/claude-opus-5-5 x", ""},
		{"", "high:max"},
	} {
		if err := c.check(bad[0], bad[1]); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestTegoStatus(t *testing.T) {
	screen := "● done\n\n──────\n❯ \n──────\n ◆ Opus 5.5 · ~/Code/x · master · ◔ 32.8%/1.0M · ✦ high · ↑1.2M ↓30k\n"
	if m, th, ok := tegoStatus(screen); !ok || m != "Opus 5.5" || th != "high" {
		t.Errorf("status: %q %q %v", m, th, ok)
	}
	// Thinking off: no ✦.
	if m, th, ok := tegoStatus(" ⏸ plan mode · ◆ gpt-6 · ~ · ◔ 1%/400k\n"); !ok || m != "gpt-6" || th != "off" {
		t.Errorf("off: %q %q %v", m, th, ok)
	}
	if _, _, ok := tegoStatus("$ ls\nfoo bar\n"); ok {
		t.Error("a shell has a tego status line")
	}
}

func TestClaudeCurrent(t *testing.T) {
	screen := " ▐▛███▜▌   Claude Code v2.1.316\n▝▜█████▛▘  Opus 5.5 with high effort · Claude Team\n\n❯ \n                                                   ● high · /effort\n"
	if got := claudeEffort(screen); got != "high" {
		t.Errorf("effort %q", got)
	}
	if m, e := claudeHeader(screen); m != "Opus 5.5" || e != "high" {
		t.Errorf("header %q %q", m, e)
	}
	if got := claudeEffort("❯ \n  ◐ medium · /effort\n"); got != "medium" {
		t.Errorf("effort %q", got)
	}

	home := modelHome(t)
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"modelSettings":{"claude-opus-5-5":{"effortLevel":"xhigh"}}}`), 0o644)
	s := &server{home: home}
	quiet := "❯ \n  ⏵⏵ auto mode on\n" // no banner, no effort shown

	// Nothing written yet: its flags, else its settings.
	if c := s.current("claude", []string{"claude", "--model", "claude-sonnet-5-5", "--effort", "low"}, quiet, ""); c.Model != "claude-sonnet-5-5" || c.ModelLabel != "Sonnet 5.5" || c.Thinking != "low" {
		t.Errorf("flags: %+v", c)
	}
	if c := s.current("claude", []string{"claude"}, quiet, ""); c.Model != "" || c.Thinking != "" {
		t.Errorf("nothing known: %+v", c)
	}
	if c := s.current("claude", []string{"claude"}, screen, ""); c.Model != "claude-opus-5-5" || c.Thinking != "high" {
		t.Errorf("banner: %+v", c)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","message":{"role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"hello"}]}}`,
	}
	write := func(more ...string) {
		lines = append(lines, more...)
		os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
		at := time.Now().Add(time.Duration(len(lines)) * time.Second) // a new mtime each time
		os.Chtimes(path, at, at)
	}
	stdout := func(s string) string {
		b, _ := json.Marshal("<local-command-stdout>" + s + "</local-command-stdout>")
		return `{"type":"user","message":{"role":"user","content":` + string(b) + `}}`
	}
	write()
	// The answer's model; the effort its settings keep for it.
	if c := s.current("claude", []string{"claude"}, quiet, path); c.Model != "claude-opus-5-5" || c.ModelLabel != "Opus 5.5" || c.Thinking != "xhigh" {
		t.Errorf("answer: %+v", c)
	}
	write(stdout("Set model to `Sonnet 5.5`"), stdout("Set effort level to low (saved as your default for new sessions): Quick"))
	if c := s.current("claude", []string{"claude", "--effort", "max"}, quiet, path); c.Model != "claude-sonnet-5-5" || c.ModelLabel != "Sonnet 5.5" || c.Thinking != "low" {
		t.Errorf("after /model and /effort: %+v", c)
	}
	// What the screen shows after a change is the freshest.
	if c := s.current("claude", nil, "● medium · /effort", path); c.Thinking != "medium" {
		t.Errorf("screen effort: %+v", c)
	}
	write(`{"type":"system","content":` + strings.TrimPrefix(strings.TrimSuffix(stdout("Set model to `Opus 5.5 (default)`"), "}}"), `{"type":"user","message":{"role":"user","content":`) + `}`)
	if c := s.current("claude", nil, quiet, path); c.Model != "default" || c.ModelLabel != "Opus 5.5" || c.Thinking != "low" {
		t.Errorf("back to default: %+v", c)
	}
	// A subagent's answer isn't the session's model.
	write(`{"type":"assistant","isSidechain":true,"message":{"role":"assistant","model":"claude-haiku-4-5","content":[]}}`)
	if c := s.current("claude", nil, quiet, path); c.Model != "default" {
		t.Errorf("sidechain: %+v", c)
	}
}

func TestCurrentTego(t *testing.T) {
	home := modelHome(t)
	// The same label from two providers: the one tego's config names wins.
	os.WriteFile(filepath.Join(home, ".tego", "models.json"), []byte(`{"anthropic":{"models":["claude-opus-5-5"]},"google-antigravity":{"models":["claude-opus-5-5"]}}`), 0o644)
	os.WriteFile(filepath.Join(home, ".tego", "config.json"), []byte(`{"model":"google-antigravity/claude-opus-5-5"}`), 0o644)
	s := &server{home: home}
	c := s.current("tego", nil, " ◆ Opus 5.5 · ~ · ✦ max\n", "")
	if c.Model != "google-antigravity/claude-opus-5-5" || c.ModelLabel != "Opus 5.5" || c.Thinking != "max" {
		t.Errorf("current: %+v", c)
	}
}

// sessionHC: a fake hc whose get and read answer from files the test
// rewrites; each call is one line of its arguments, split by |.
type sessionHC struct {
	t                     *testing.T
	bin, calls, get, read string
}

func newSessionHC(t *testing.T) *sessionHC {
	dir := t.TempDir()
	h := &sessionHC{t: t, calls: filepath.Join(dir, "calls"), get: filepath.Join(dir, "get"), read: filepath.Join(dir, "read")}
	h.bin = filepath.Join(dir, "hc")
	script := `#!/bin/sh
for a in "$@"; do printf '%s|' "$a" >> ` + h.calls + `; done; echo >> ` + h.calls + `
case "$1" in
get) cat ` + h.get + ` ;;
read) cat ` + h.read + ` ;;
*) echo '{"ok":true}' ;;
esac
`
	os.WriteFile(h.bin, []byte(script), 0o755)
	return h
}

func (h *sessionHC) set(app, state, screen string) {
	g, _ := json.Marshal(map[string]any{"cwd": "/tmp/x", "foreground": map[string]any{"name": app, "argv": []string{app}}, "status": map[string]any{"app": app, "state": state}})
	os.WriteFile(h.get, g, 0o644)
	r, _ := json.Marshal(map[string]string{"text": screen})
	os.WriteFile(h.read, r, 0o644)
	os.Remove(h.calls)
}

// typed: the calls that typed or pressed something.
func (h *sessionHC) typed() []string {
	b, _ := os.ReadFile(h.calls)
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.HasPrefix(l, "send|") || strings.HasPrefix(l, "keys|") {
			out = append(out, l)
		}
	}
	return out
}

func TestSession(t *testing.T) {
	old := []time.Duration{switchConfirmWait, switchPoll, switchSettle}
	switchConfirmWait, switchPoll, switchSettle = 300*time.Millisecond, 20*time.Millisecond, 0
	t.Cleanup(func() { switchConfirmWait, switchPoll, switchSettle = old[0], old[1], old[2] })

	h := newSessionHC(t)
	s := &server{hc: h.bin, home: modelHome(t)}
	post := func(body map[string]string) (int, string) {
		b, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		s.session(rec, httptest.NewRequest("POST", "/api/session", strings.NewReader(string(b))))
		return rec.Code, rec.Body.String()
	}
	tegoScreen := "❯ \n ◆ gpt-6 · ~ · ✦ high · ↑1k\n"

	// tego, idle: its own commands, one word each, as one argument.
	h.set("tego", "idle", tegoScreen)
	if code, body := post(map[string]string{"terminal": "t7", "agent": "tego", "thinking": "low"}); code != 200 || !strings.Contains(body, `"thinking":"high"`) {
		t.Errorf("thinking: %d %s", code, body) // "current" is what the screen says after
	}
	if got, want := h.typed(), []string{"send|--json|t7|/thinking low|"}; !reflect.DeepEqual(got, want) {
		t.Errorf("thinking sent %q, want %q", got, want)
	}
	// A model alone keeps the level on screen (/model spec alone opens tego's picker).
	h.set("tego", "done", tegoScreen)
	if code, body := post(map[string]string{"terminal": "t7", "model": "anthropic/claude-sonnet-5-5"}); code != 200 {
		t.Errorf("model: %d %s", code, body)
	}
	if got, want := h.typed(), []string{"send|--json|t7|/model anthropic/claude-sonnet-5-5:high|"}; !reflect.DeepEqual(got, want) {
		t.Errorf("model sent %q, want %q", got, want)
	}
	h.set("tego", "idle", tegoScreen)
	post(map[string]string{"terminal": "t7", "model": "anthropic/claude-opus-5-5", "thinking": "off"})
	if got, want := h.typed(), []string{"send|--json|t7|/model anthropic/claude-opus-5-5:off|"}; !reflect.DeepEqual(got, want) {
		t.Errorf("model and thinking sent %q, want %q", got, want)
	}
	// No status line to read the level from: it asks for one.
	h.set("tego", "idle", "loading…")
	if code, _ := post(map[string]string{"terminal": "t7", "model": "anthropic/claude-opus-5-5"}); code != 409 || len(h.typed()) != 0 {
		t.Errorf("no level: %d %q", code, h.typed())
	}

	// Refused, nothing typed.
	refuse := []struct {
		app, state string
		body       map[string]string
		code       int
		msg        string
	}{
		{"tego", "working", map[string]string{"terminal": "t7", "thinking": "low"}, 409, "tego is working; change the model when it's done"},
		{"tego", "blocked", map[string]string{"terminal": "t7", "thinking": "low"}, 409, "waiting for you"},
		{"claude", "idle", map[string]string{"terminal": "t7", "agent": "tego", "thinking": "low"}, 409, "no longer runs tego"},
		{"codex", "idle", map[string]string{"terminal": "t7", "thinking": "low"}, 409, "new task"},
		{"zsh", "", map[string]string{"terminal": "t7", "thinking": "low"}, 409, "isn't running an agent"},
		{"tego", "idle", map[string]string{"terminal": "t7", "model": "anthropic/claude-opus-0"}, 400, "doesn't offer"},
		{"tego", "idle", map[string]string{"terminal": "t7", "thinking": "low\n/quit"}, 400, "no thinking level"},
		{"tego", "idle", map[string]string{"terminal": "t7", "model": "evil/x; rm -rf ~"}, 400, "doesn't offer"},
		{"tego", "idle", map[string]string{"terminal": "t7"}, 400, "need"},
		{"tego", "idle", map[string]string{"terminal": "t7; ls", "thinking": "low"}, 400, "need"},
		{"claude", "idle", map[string]string{"terminal": "t7", "thinking": "off"}, 400, "no effort level"},
	}
	for _, c := range refuse {
		h.set(c.app, c.state, tegoScreen)
		code, body := post(c.body)
		if code != c.code || !strings.Contains(body, c.msg) || len(h.typed()) != 0 {
			t.Errorf("%s %s %v: %d %s, typed %q", c.app, c.state, c.body, code, body, h.typed())
		}
	}

	// Claude Code: /model, then Enter on its "Switch model?" (and nothing
	// else), then /effort.
	h.set("claude", "idle", ` Switch model?
 This conversation is cached for Opus 5.5.

 ❯ 1. Yes, switch to Sonnet 5.5
   2. No, go back

 Enter to confirm · Esc to cancel
`)
	if code, body := post(map[string]string{"terminal": "t7", "agent": "claude", "model": "claude-sonnet-5-5", "thinking": "low"}); code != 200 {
		t.Errorf("claude: %d %s", code, body)
	}
	want := []string{"send|--json|t7|/model claude-sonnet-5-5|", "keys|--json|t7|enter|", "send|--json|t7|/effort low|"}
	if got := h.typed(); !reflect.DeepEqual(got, want) {
		t.Errorf("claude typed\n %q\nwant\n %q", got, want)
	}
	// Some other menu on screen: left alone.
	h.set("claude", "idle", " Do you want to proceed?\n ❯ 1. Yes\n   2. No\n")
	post(map[string]string{"terminal": "t7", "model": "default"})
	if got, want := h.typed(), []string{"send|--json|t7|/model default|"}; !reflect.DeepEqual(got, want) {
		t.Errorf("claude other menu: %q", got)
	}
}

func TestStartFlags(t *testing.T) {
	cases := []struct {
		agent, model, thinking string
		want                   []string
	}{
		{"tego", "anthropic/claude-opus-5-5", "high", []string{"--model", "anthropic/claude-opus-5-5", "--thinking", "high"}},
		{"tego", "", "off", []string{"--thinking", "off"}},
		{"claude", "claude-sonnet-5-5", "max", []string{"--model", "claude-sonnet-5-5", "--effort", "max"}},
		{"claude", "default", "", nil}, // its default: no flag
		{"codex", "gpt-5", "minimal", []string{"-m", "gpt-5", "-c", "model_reasoning_effort=minimal"}},
		{"aider", "x", "y", nil},
	}
	for _, c := range cases {
		if got := startFlags(c.agent, c.model, c.thinking); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %s %s: %q, want %q", c.agent, c.model, c.thinking, got, c.want)
		}
	}
}

func TestTaskModel(t *testing.T) {
	dir := t.TempDir()
	herdr, calls := fakeTool(t, "herdr-app", `echo '{"space":"sp1","tab":"tb9","terminal":"t7"}'`)
	s := &server{herdr: herdr, hc: "/bin/false", home: modelHome(t), programsFile: "/nonexistent",
		hasTool: func(n string) bool { return n == "claude" || n == "tego" || n == "codex" || n == "aider" }}
	post := func(body map[string]string) (int, string) {
		b, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		s.task(rec, httptest.NewRequest("POST", "/api/task", strings.NewReader(string(b))))
		return rec.Code, rec.Body.String()
	}
	argvOf := func() []string {
		var call struct{ Argv []string }
		json.Unmarshal([]byte(strings.TrimPrefix(readCalls(t, calls), "do tab.new ")), &call)
		return call.Argv
	}

	ok := []struct {
		body map[string]string
		argv []string
	}{
		{map[string]string{"agent": "tego", "model": "anthropic/claude-opus-5-5", "thinking": "xhigh", "prompt": "fix the bug"},
			[]string{"tego", "--model", "anthropic/claude-opus-5-5", "--thinking", "xhigh", "fix the bug"}},
		{map[string]string{"agent": "claude", "model": "claude-sonnet-5-5", "thinking": "low", "prompt": "fix the bug"},
			[]string{"claude", "--model", "claude-sonnet-5-5", "--effort", "low", "fix the bug"}},
		{map[string]string{"agent": "codex", "model": "gpt-5", "thinking": "high", "prompt": "fix the bug"},
			[]string{"codex", "-m", "gpt-5", "-c", "model_reasoning_effort=high", "fix the bug"}},
		{map[string]string{"agent": "claude", "thinking": "max", "prompt": ""}, []string{"claude", "--effort", "max"}},
		{map[string]string{"agent": "claude", "prompt": "fix the bug"}, []string{"claude", "fix the bug"}},
	}
	for _, c := range ok {
		os.Remove(calls)
		c.body["cwd"] = dir
		if code, body := post(c.body); code != 200 {
			t.Errorf("%v: %d %s", c.body, code, body)
			continue
		}
		if got := argvOf(); !reflect.DeepEqual(got, c.argv) {
			t.Errorf("%v:\n got %q\nwant %q", c.body, got, c.argv)
		}
	}

	bad := []map[string]string{
		{"agent": "tego", "model": "anthropic/claude-opus-0"},
		{"agent": "tego", "thinking": "turbo"},
		{"agent": "claude", "thinking": "off"},             // Claude has no "off" effort
		{"agent": "codex", "model": "gpt-5; rm -rf ~"},     // not a model, never a word in argv
		{"agent": "tego", "model": "--dangerously-skip-x"}, // not a flag either
		{"agent": "claude", "model": "claude-sonnet-5-5\n--x"},
		{"agent": "aider", "model": "gpt-5"}, // takes no model from here
	}
	for _, b := range bad {
		os.Remove(calls)
		b["cwd"], b["prompt"] = dir, "fix the bug"
		if code, _ := post(b); code != 400 {
			t.Errorf("%v: code %d, want 400", b, code)
		}
		if got := readCalls(t, calls); got != "" {
			t.Errorf("%v: herdr-app called with %q", b, got)
		}
	}
}
