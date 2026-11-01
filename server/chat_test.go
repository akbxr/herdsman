package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func lines(s string) [][]byte {
	var out [][]byte
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		out = append(out, []byte(strings.TrimSpace(l)))
	}
	return out
}

func TestParseTego(t *testing.T) {
	// A conversation rewound once: the old answer (a2) is on a dead branch.
	items, _ := parseTree("tego", lines(`
		{"type":"session","id":"s"}
		{"type":"message","id":"u1","parent":null,"message":{"role":"user","content":[{"type":"text","text":"fix the bug"}]}}
		{"type":"message","id":"a1","parent":"u1","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"Looking."},{"type":"tool_use","id":"c1","name":"bash","input":{"command":"go test ./..."}},{"type":"tool_use","id":"c2","name":"read","input":{"path":"main.go"}}]}}
		{"type":"message","id":"r1","parent":"a1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":"FAIL x\nline2\nline3\nline4","is_error":true},{"type":"tool_result","tool_use_id":"c2","content":[{"type":"text","text":"package main"}]}]}}
		{"type":"message","id":"a2","parent":"r1","message":{"role":"assistant","content":[{"type":"text","text":"old answer"}]}}
		{"type":"compaction","id":"k1","parent":"r1"}
		{"type":"message","id":"a3","parent":"k1","message":{"role":"assistant","content":[{"type":"text","text":"Fixed it."}]}}
		{"type":"message","id":"u2","parent":"a3","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>x</system-reminder>"},{"type":"text","text":"thanks"},{"type":"image"}]}}
	`))
	want := []ChatItem{
		{Kind: "user", Text: "fix the bug"},
		{Kind: "assistant", Text: "Looking."},
		{Kind: "tools", Tools: []ChatTool{
			{Name: "bash", Detail: "go test ./...", Result: "FAIL x\nline2\nline3\n…", Error: true},
			{Name: "read", Detail: "main.go", Result: "package main"},
		}},
		{Kind: "note", Text: "Earlier messages were summarised to free context."},
		{Kind: "assistant", Text: "Fixed it."},
		{Kind: "user", Text: "thanks\n\n[image]"},
	}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("got  %+v\nwant %+v", items, want)
	}
}

func TestParseClaude(t *testing.T) {
	items, _ := parseTree("claude", lines(`
		{"type":"user","uuid":"1","parentUuid":null,"message":{"role":"user","content":"add a test"}}
		{"type":"user","uuid":"m","parentUuid":"1","isMeta":true,"message":{"role":"user","content":"<command-name>/x</command-name>"}}
		{"type":"assistant","uuid":"2","parentUuid":"m","message":{"id":"msg1","role":"assistant","content":[{"type":"text","text":"Sure."}]}}
		{"type":"assistant","uuid":"3","parentUuid":"2","message":{"id":"msg1","role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/a/b.go","old_string":"x"}}]}}
		{"type":"assistant","uuid":"s1","parentUuid":"3","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"subagent"}]}}
		{"type":"system","uuid":"4","parentUuid":"3"}
		{"type":"user","uuid":"5","parentUuid":"4","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}
		{"type":"assistant","uuid":"6","parentUuid":"5","message":{"id":"msg2","role":"assistant","content":[{"type":"text","text":"Done."}]}}
	`))
	want := []ChatItem{
		{Kind: "user", Text: "add a test"},
		{Kind: "assistant", Text: "Sure."},
		{Kind: "tools", Tools: []ChatTool{{Name: "Edit", Detail: "/a/b.go", Result: "ok"}}},
		{Kind: "assistant", Text: "Done."},
	}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("got  %+v\nwant %+v", items, want)
	}
}

// tego's rewind (/tree, or editing an earlier message) writes a "leaf" entry
// whose id is the node the conversation goes on from, then the new branch.
// That id must not replace the node: the chat before the rewind stays.
func TestParseTegoRewind(t *testing.T) {
	before := `
		{"type":"header","id":"20260101-000000-abcdef","cwd":"/p"}
		{"type":"message","id":"u1","message":{"role":"user","content":[{"type":"text","text":"first question"}]}}
		{"type":"message","id":"a1","parent":"u1","message":{"role":"assistant","content":[{"type":"text","text":"first answer"}]}}
		{"type":"title","title":"t","ts":1}
		{"type":"message","id":"u2","parent":"a1","message":{"role":"user","content":[{"type":"text","text":"rewound away"}]}}
		{"type":"message","id":"a2","parent":"u2","message":{"role":"assistant","content":[{"type":"text","text":"dead answer"}]}}
		{"type":"leaf","id":"a1"}`

	// Right after the rewind: the conversation as it is at the rewind point.
	items, cut := parseTree("tego", lines(before))
	want := []ChatItem{{Kind: "user", Text: "first question"}, {Kind: "assistant", Text: "first answer"}}
	if !reflect.DeepEqual(items, want) || cut {
		t.Errorf("at the rewind point: got %+v (cut %v)\nwant %+v", items, cut, want)
	}

	// Then the new branch, from the rewind point.
	items, cut = parseTree("tego", lines(before+`
		{"type":"message","id":"u3","parent":"a1","message":{"role":"user","content":[{"type":"text","text":"second try"}]}}
		{"type":"message","id":"a3","parent":"u3","message":{"role":"assistant","content":[{"type":"text","text":"new answer"}]}}
	`))
	want = append(want, ChatItem{Kind: "user", Text: "second try"}, ChatItem{Kind: "assistant", Text: "new answer"})
	if !reflect.DeepEqual(items, want) || cut {
		t.Errorf("after the rewind: got %+v (cut %v)\nwant %+v", items, cut, want)
	}

	// A rewind to the very start: nothing in play.
	items, _ = parseTree("tego", lines(before+`
		{"type":"leaf"}
	`))
	if len(items) != 0 {
		t.Errorf("start over: got %+v", items)
	}
}

// Claude Code's rewind leaves the old messages in the file and writes the new
// prompt as a second child of the point it went back to.
func TestParseClaudeRewind(t *testing.T) {
	items, _ := parseTree("claude", lines(`
		{"type":"user","uuid":"u1","parentUuid":null,"message":{"role":"user","content":"first question"}}
		{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"first answer"}]}}
		{"type":"user","uuid":"u2","parentUuid":"a1","message":{"role":"user","content":"rewound away"}}
		{"type":"assistant","uuid":"a2","parentUuid":"u2","message":{"id":"m2","role":"assistant","content":[{"type":"text","text":"dead answer"}]}}
		{"type":"last-prompt","leafUuid":"a2"}
		{"type":"user","uuid":"u3","parentUuid":"a1","message":{"role":"user","content":"second try"}}
		{"type":"assistant","uuid":"a3","parentUuid":"u3","message":{"id":"m3","role":"assistant","content":[{"type":"text","text":"new answer"}]}}
	`))
	want := []ChatItem{
		{Kind: "user", Text: "first question"},
		{Kind: "assistant", Text: "first answer"},
		{Kind: "user", Text: "second try"},
		{Kind: "assistant", Text: "new answer"},
	}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("got  %+v\nwant %+v", items, want)
	}
}

// Parallel calls: Claude Code chains the calls of one answer and hangs each
// result off its own call; the conversation goes on from one of them.
func TestParseClaudeParallelCalls(t *testing.T) {
	items, _ := parseTree("claude", lines(`
		{"type":"user","uuid":"u1","parentUuid":null,"message":{"role":"user","content":"search"}}
		{"type":"assistant","uuid":"c1","parentUuid":"u1","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"t1","name":"WebSearch","input":{"query":"one"}}]}}
		{"type":"assistant","uuid":"c2","parentUuid":"c1","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"t2","name":"WebSearch","input":{"query":"two"}}]}}
		{"type":"assistant","uuid":"c3","parentUuid":"c2","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"t3","name":"WebSearch","input":{"query":"three"}}]}}
		{"type":"user","uuid":"r1","parentUuid":"c1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"r one"}]}}
		{"type":"user","uuid":"r3","parentUuid":"c3","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t3","content":"r three"}]}}
		{"type":"user","uuid":"r2","parentUuid":"c2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"r two"}]}}
		{"type":"attachment","uuid":"x","parentUuid":"r2"}
		{"type":"assistant","uuid":"a","parentUuid":"x","message":{"id":"m2","role":"assistant","content":[{"type":"text","text":"Found it."}]}}
	`))
	want := []ChatItem{
		{Kind: "user", Text: "search"},
		{Kind: "tools", Tools: []ChatTool{
			{Name: "WebSearch", Detail: "one", Result: "r one"},
			{Name: "WebSearch", Detail: "two", Result: "r two"},
			{Name: "WebSearch", Detail: "three", Result: "r three"},
		}},
		{Kind: "assistant", Text: "Found it."},
	}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("got  %+v\nwant %+v", items, want)
	}
}

// A rewind to a point older than the tail: the file is read further back
// until the branch in play is whole, and a long conversation that simply
// starts before the tail does not make it read more.
func TestReadChatRewindPastTheTail(t *testing.T) {
	msg := func(id, parent, role, text string) string {
		p := "null"
		if parent != "" {
			p = `"` + parent + `"`
		}
		return `{"type":"message","id":"` + id + `","parent":` + p + `,"message":{"role":"` + role + `","content":[{"type":"text","text":"` + text + `"}]}}` + "\n"
	}
	var sb strings.Builder
	sb.WriteString(`{"type":"header","id":"s"}` + "\n")
	sb.WriteString(msg("u1", "", "user", "first question"))
	sb.WriteString(msg("a1", "u1", "assistant", "first answer"))
	sb.WriteString(msg("u2", "a1", "user", "rewound away"))
	// A long dead branch: more than the tail.
	pad := strings.Repeat("x", 64<<10)
	prev := "u2"
	for i := 0; int64(i*len(pad)) < chatTail+(1<<20); i++ {
		id := fmt.Sprintf("d%d", i)
		sb.WriteString(msg(id, prev, "assistant", pad))
		prev = id
	}
	sb.WriteString(`{"type":"leaf","id":"a1"}` + "\n")
	sb.WriteString(msg("u3", "a1", "user", "second try"))
	sb.WriteString(msg("a3", "u3", "assistant", "new answer"))

	p := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(p, []byte(sb.String()), 0o644)
	st, _ := os.Stat(p)
	items, err := readChat("tego", p, st)
	if err != nil {
		t.Fatal(err)
	}
	want := []ChatItem{
		{Kind: "user", Text: "first question"},
		{Kind: "assistant", Text: "first answer"},
		{Kind: "user", Text: "second try"},
		{Kind: "assistant", Text: "new answer"},
	}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("got %d items, first %+v\nwant %+v", len(items), items[:min(len(items), 2)], want)
	}

	// The same file without the rewind: the walk stops at the start of the
	// tail, with nothing left behind, so no more is read.
	linear := strings.Split(strings.TrimSpace(sb.String()), "\n")
	linear = linear[:len(linear)-3]
	var ls [][]byte
	for _, l := range linear[len(linear)-40:] {
		ls = append(ls, []byte(l))
	}
	if items, cut := parseTree("tego", ls); cut || len(items) != 1 {
		t.Errorf("a conversation longer than the lines read: cut %v, %d items", cut, len(items))
	}
}

func TestParseCodex(t *testing.T) {
	items := parseCodex(lines(`
		{"type":"session_meta","payload":{"cwd":"/p"}}
		{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>x</environment_context>"}]}}
		{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"list files"}]}}
		{"type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"{\"command\":[\"bash\",\"-lc\",\"ls\"]}","call_id":"c1"}}
		{"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"{\"output\":\"a\\nb\"}"}}
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Two files."}]}
	`))
	want := []ChatItem{
		{Kind: "user", Text: "list files"},
		{Kind: "tools", Tools: []ChatTool{{Name: "shell", Detail: "bash -lc ls", Result: "a\nb"}}},
		{Kind: "assistant", Text: "Two files."},
	}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("got  %+v\nwant %+v", items, want)
	}
}

func TestTailLinesDropsTheCutLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	os.WriteFile(p, []byte("first-long-line\nsecond\nthird\n"), 0o644)
	got, _ := tailLines(p, 16) // starts inside "first-long-line": that line is dropped
	if len(got) != 2 || string(got[0]) != "second" || string(got[1]) != "third" {
		t.Errorf("got %q", got)
	}
	all, _ := tailLines(p, 1<<20)
	if len(all) != 3 {
		t.Errorf("whole file: got %q", all)
	}
}

func TestSlugs(t *testing.T) {
	if got := claudeSlug("/Users/a/my.proj_x"); got != "-Users-a-my-proj-x" {
		t.Errorf("claude: %q", got)
	}
}

func TestAgentOf(t *testing.T) {
	for in, want := range map[string]string{"tego": "tego", "claude": "claude", "/opt/bin/codex": "codex", "zsh": ""} {
		if got := agentOf(foregroundInfo{Name: in}); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
	// Claude Code runs as node with claude in its arguments.
	if got := agentOf(foregroundInfo{Name: "node", Argv: []string{"node", "/usr/local/bin/claude"}}); got != "claude" {
		t.Errorf("node claude: %q", got)
	}
}

func TestMatchFiles(t *testing.T) {
	files := []string{"server/attach.go", "android/app/Attachments.kt", "docs/how-to-attach.md", "a/t/t/a/c/h.txt", "main.go"}
	got := matchFiles(files, "attach", 10)
	want := []string{"server/attach.go", "android/app/Attachments.kt", "docs/how-to-attach.md", "a/t/t/a/c/h.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := matchFiles(files, "zzz", 10); len(got) != 0 {
		t.Errorf("no match: %q", got)
	}
}

func TestCommandsFor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	proj := filepath.Join(home, "p")
	os.MkdirAll(filepath.Join(home, ".claude", "commands"), 0o755)
	os.MkdirAll(filepath.Join(proj, ".tego", "skills", "deploy"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude", "commands", "ship.md"), []byte("---\ndescription: Ship it\n---\nbody"), 0o644)
	os.WriteFile(filepath.Join(proj, ".tego", "skills", "deploy", "SKILL.md"), []byte("---\nname: deploy-app\n---\n# Deploys the app\n"), 0o644)

	names := func(cs []command) map[string]string {
		m := map[string]string{}
		for _, c := range cs {
			m[c.Name] = c.Help
		}
		return m
	}
	tego := names(commandsFor("tego", proj))
	if tego["/compact"] == "" || tego["/ship"] != "Ship it" || tego["/skill:deploy-app"] != "Deploys the app" {
		t.Errorf("tego: %v", tego)
	}
	claude := names(commandsFor("claude", proj))
	if claude["/ship"] != "Ship it" || claude["/clear"] == "" {
		t.Errorf("claude: %v", claude)
	}
	if _, ok := claude["/deploy-app"]; ok {
		t.Error("claude got a .tego skill")
	}
	if got := commandsFor("", proj); len(got) != 0 {
		t.Errorf("a shell: %v", got)
	}
}

func TestClaudeSession(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".claude", "sessions"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude", "sessions", "4242.json"), []byte(`{"pid":4242,"sessionId":"abc-123","cwd":"/Users/x/p"}`), 0o644)
	want := filepath.Join(home, ".claude", "projects", "-Users-x-p", "abc-123.jsonl")
	if got := claudeSession(home, 4242); got != want {
		t.Errorf("got %q, want %q (even before the file exists: a new session)", got, want)
	}
	if got := claudeSession(home, 1); got != "" {
		t.Errorf("no session file: %q", got)
	}
	os.WriteFile(filepath.Join(home, ".claude", "sessions", "7.json"), []byte(`{"sessionId":"../../etc/x","cwd":"/p"}`), 0o644)
	if got := claudeSession(home, 7); got != "" {
		t.Errorf("a path for a session id: %q", got)
	}
}
