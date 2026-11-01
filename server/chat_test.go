package main

import (
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
	items := parseTree("tego", lines(`
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
	items := parseTree("claude", lines(`
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
	if got := tegoSlug("/nonexistent/a/b"); got != "nonexistent-a-b" {
		t.Errorf("tego: %q", got)
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
