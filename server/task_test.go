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

func TestAgents(t *testing.T) {
	progs := filepath.Join(t.TempDir(), "programs.json")
	os.WriteFile(progs, []byte(`{"programs":{
		"tego":{"name":"tego","run":["tego","--fast"]},
		"lazygit":{"name":"Lazygit","run":["lazygit"]},
		"ghost":{"name":"Ghost","run":["ghost"]},
		"empty":{"name":"Empty","run":[]}}}`), 0o644)
	installed := map[string]bool{"tego": true, "claude": true, "lazygit": true}
	s := &server{programsFile: progs, hasTool: func(n string) bool { return installed[n] }}

	got := s.agents()
	want := []AgentInfo{
		{ID: "tego", Name: "tego", Argv: []string{"tego", "--fast"}, Positional: true}, // programs.json's flags
		{ID: "claude", Name: "Claude Code", Argv: []string{"claude"}, Positional: true},
		{ID: "lazygit", Name: "Lazygit", Argv: []string{"lazygit"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("agents:\n got %+v\nwant %+v", got, want)
	}
}

func TestTaskLaunch(t *testing.T) {
	claude := AgentInfo{ID: "claude", Argv: []string{"claude"}, Positional: true}
	other := AgentInfo{ID: "aider", Argv: []string{"aider"}}
	cases := []struct {
		agent    AgentInfo
		prompt   string
		argv     []string
		delivery string
	}{
		{claude, "fix the login bug", []string{"claude", "fix the login bug"}, "argv"},
		{claude, "two\nlines", []string{"claude", "two\nlines"}, "argv"},
		{claude, "-v is broken, look", []string{"claude"}, "typed"}, // a flag to it
		{claude, "update", []string{"claude"}, "typed"},             // one of its commands
		{claude, "", []string{"claude"}, "none"},
		{other, "fix the login bug", []string{"aider"}, "typed"},
	}
	for _, c := range cases {
		argv, d := taskLaunch(c.agent, c.prompt)
		if !reflect.DeepEqual(argv, c.argv) || d != c.delivery {
			t.Errorf("%s %q: got %q %s, want %q %s", c.agent.ID, c.prompt, argv, d, c.argv, c.delivery)
		}
	}
	if argv, _ := taskLaunch(claude, "x y"); &argv[0] == &claude.Argv[0] {
		t.Error("taskLaunch shares the agent's argv")
	}
}

func TestTask(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a-file")
	os.WriteFile(file, nil, 0o644)
	herdr, calls := fakeTool(t, "herdr-app", `echo '{"space":"sp1","tab":"tb9","terminal":"t7"}'`)
	s := &server{herdr: herdr, hc: "/bin/false", programsFile: "/nonexistent", hasTool: func(n string) bool { return n == "claude" || n == "tego" }}

	post := func(body any) (int, map[string]string) {
		b, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		s.task(rec, httptest.NewRequest("POST", "/api/task", strings.NewReader(string(b))))
		var out map[string]string
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	// Words a shell would act on are one argument, never parsed.
	prompt := `fix it; rm -rf ~ $(whoami) "quoted"`
	code, out := post(map[string]string{"cwd": dir + "/", "agent": "claude", "prompt": "  " + prompt + "\n"})
	if code != 200 || out["terminal"] != "t7" || out["tab"] != "tb9" || out["prompt"] != "argv" {
		t.Fatalf("start: %d %v", code, out)
	}
	args, _ := json.Marshal(map[string]any{"folder": dir, "argv": []string{"claude", prompt}})
	if got, want := readCalls(t, calls), "do tab.new "+string(args); got != want {
		t.Errorf("herdr-app called with\n %s\nwant\n %s", got, want)
	}

	bad := []map[string]string{
		{"cwd": "relative/path", "agent": "claude", "prompt": "x y"},
		{"cwd": "", "agent": "claude", "prompt": "x y"},
		{"cwd": filepath.Join(dir, "missing"), "agent": "claude", "prompt": "x y"},
		{"cwd": file, "agent": "claude", "prompt": "x y"},
		{"cwd": dir, "agent": "codex", "prompt": "x y"}, // not installed
		{"cwd": dir, "agent": "sh -c ls", "prompt": "x y"},
		{"cwd": dir, "agent": "claude", "prompt": strings.Repeat("x ", taskMaxPrompt)},
	}
	for _, b := range bad {
		os.Remove(calls)
		if code, _ := post(b); code != 400 {
			t.Errorf("%v: code %d, want 400", b, code)
		}
		if got := readCalls(t, calls); got != "" {
			t.Errorf("%v: herdr-app called with %q", b, got)
		}
	}

	// The window is closed, or answers without a terminal.
	s.herdr, _ = fakeTool(t, "herdr-app", `echo "no window" >&2; exit 1`)
	if code, out := post(map[string]string{"cwd": dir, "agent": "tego", "prompt": "x y"}); code != 502 || !strings.Contains(out["error"], "no window") {
		t.Errorf("no window: %d %v", code, out)
	}
	s.herdr, _ = fakeTool(t, "herdr-app", `echo '{"tab":"tb9"}'`)
	if code, _ := post(map[string]string{"cwd": dir, "agent": "tego", "prompt": "x y"}); code != 502 {
		t.Errorf("no terminal: %d, want 502", code)
	}
}

// fast makes the typed-task waits short for a test.
func fast(t *testing.T) {
	old := []time.Duration{taskReady, taskPoll, taskSettle, taskWarmup}
	taskReady, taskPoll, taskSettle, taskWarmup = 5*time.Second, 20*time.Millisecond, 100*time.Millisecond, 150*time.Millisecond
	t.Cleanup(func() { taskReady, taskPoll, taskSettle, taskWarmup = old[0], old[1], old[2], old[3] })
}

// hcScript: a fake hc whose `get` answers with the shell for the first
// [shellFor] calls, then with fg (a JSON object, or the shell again).
func hcScript(t *testing.T, shellFor int, then string) (string, string) {
	dir := t.TempDir()
	n := filepath.Join(dir, "n")
	return fakeTool(t, "hc", `case "$1" in
get)
  c=$(cat `+n+` 2>/dev/null || echo 0); c=$((c+1)); echo $c > `+n+`
  if [ $c -le `+itoa(int64(shellFor))+` ]; then echo '{"state":"live","foreground":{"name":"zsh"}}'
  else echo '`+then+`'; fi ;;
read) echo '{"text":"ready"}' ;;
send) echo '{"ok":true}' ;;
esac`)
}

func sends(t *testing.T, calls string) []string {
	var out []string
	for _, l := range strings.Split(readCalls(t, calls), "\n") {
		if strings.HasPrefix(l, "send ") {
			out = append(out, l)
		}
	}
	return out
}

func TestTypeWhenReady(t *testing.T) {
	fast(t)
	ctx := context.Background()

	// An agent that reports: typed once it says idle.
	hc, calls := hcScript(t, 3, `{"state":"live","foreground":{"name":"aider"},"status":{"state":"idle"}}`)
	s := &server{hc: hc}
	if err := s.typeWhenReady(ctx, "t7", "fix the login bug"); err != nil {
		t.Fatal(err)
	}
	if got := sends(t, calls); len(got) != 1 || got[0] != "send --json t7 fix the login bug" {
		t.Errorf("reporting agent: sends %q", got)
	}

	// One that says nothing: typed once its screen settles.
	hc, calls = hcScript(t, 2, `{"state":"live","foreground":{"name":"lazygit"},"status":null}`)
	s = &server{hc: hc}
	start := time.Now()
	if err := s.typeWhenReady(ctx, "t7", "hello"); err != nil {
		t.Fatal(err)
	}
	if got := sends(t, calls); len(got) != 1 {
		t.Errorf("quiet program: sends %q", got)
	}
	if time.Since(start) < taskWarmup {
		t.Error("typed before the program had run a moment")
	}

	// Still working, or waiting on a question: it waits, it doesn't type.
	hc, calls = hcScript(t, 0, `{"state":"live","foreground":{"name":"claude"},"status":{"state":"blocked"}}`)
	s = &server{hc: hc}
	taskReady = 300 * time.Millisecond
	if err := s.typeWhenReady(ctx, "t7", "hello"); err == nil {
		t.Error("blocked: no error")
	}
	if got := sends(t, calls); len(got) != 0 {
		t.Errorf("blocked: sends %q", got)
	}
	taskReady = 5 * time.Second

	// The program never starts and the terminal ends.
	hc, calls = hcScript(t, 2, `{"state":"finished","foreground":null}`)
	s = &server{hc: hc}
	if err := s.typeWhenReady(ctx, "t7", "hello"); err == nil || len(sends(t, calls)) != 0 {
		t.Errorf("ended: err %v, sends %q", err, sends(t, calls))
	}
}

func TestTypeWhenReadyQuit(t *testing.T) {
	fast(t)
	// Starts, then the shell is back before it settled: it quit.
	dir := t.TempDir()
	n := filepath.Join(dir, "n")
	hc, calls := fakeTool(t, "hc", `case "$1" in
get)
  c=$(cat `+n+` 2>/dev/null || echo 0); c=$((c+1)); echo $c > `+n+`
  if [ $c -le 2 ]; then echo '{"state":"live","foreground":{"name":"aider"},"status":{"state":"working"}}'
  else echo '{"state":"live","foreground":{"name":"-zsh"}}'; fi ;;
send) echo '{"ok":true}' ;;
esac`)
	s := &server{hc: hc}
	if err := s.typeWhenReady(context.Background(), "t7", "hello"); err == nil || !strings.Contains(err.Error(), "quit") {
		t.Errorf("quit: err %v", err)
	}
	if got := sends(t, calls); len(got) != 0 {
		t.Errorf("quit: sends %q", got)
	}
}

func TestFolders(t *testing.T) {
	home := t.TempDir()
	old := tempRoots
	tempRoots = []string{"/tmp", "/private/tmp"} // the test's home is in the temp folder
	t.Cleanup(func() { tempRoots = old })
	mk := func(p ...string) string {
		d := filepath.Join(append([]string{home}, p...)...)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		return d
	}
	write := func(path, body string, age time.Duration) {
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(body), 0o644)
		at := time.Now().Add(-age)
		os.Chtimes(path, at, at)
	}
	gitAt := func(dir string, age time.Duration) {
		write(filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main\n", age)
		at := time.Now().Add(-age)
		os.Chtimes(filepath.Join(dir, ".git"), at, at)
	}

	api := mk("Code", "api")             // a repo; tego worked there an hour ago
	web := mk("Code", "projects", "web") // a repo, live terminal there now
	stale := mk("Code", "old")           // a repo nobody worked in lately
	deep := mk("Code", "a", "b", "deep") // three levels down: found
	mk("Code", "a", "b", "c", "toodeep") // four: not
	notes := mk("notes")                 // not a repo; Claude worked there yesterday
	mk("Code", "node_modules", "pkg")
	for _, d := range []string{api, web, deep, filepath.Join(home, "Code", "a", "b", "c", "toodeep"), filepath.Join(home, "Code", "node_modules", "pkg")} {
		gitAt(d, 48*time.Hour)
	}
	gitAt(stale, 30*24*time.Hour)
	mk("Code", "api", "sub")
	gitAt(filepath.Join(api, "sub"), time.Hour) // inside a repo: not a project of its own
	os.WriteFile(filepath.Join(home, "Code", "worktree-file"), nil, 0o644)
	wt := mk("Code", "wt")
	write(filepath.Join(wt, ".git"), "gitdir: /elsewhere\n", 72*time.Hour) // a worktree

	write(filepath.Join(home, ".tego", "sessions", "x", "1.jsonl"), `{"type":"header","cwd":"`+api+`"}`+"\n", time.Hour)
	write(filepath.Join(home, ".tego", "sessions", "y", "1.jsonl"), `{"type":"header","cwd":"/tmp/scratch"}`+"\n", time.Minute)
	write(filepath.Join(home, ".claude", "projects", "-notes", "s.jsonl"), `{"type":"user","cwd":"`+notes+`","x":1}`+"\n", 24*time.Hour)
	write(filepath.Join(home, ".claude", "history.jsonl"), `{"project":"`+filepath.Join(home, "gone")+`","timestamp":`+itoa(time.Now().UnixMilli())+`}`+"\n", 0)

	hc, _ := fakeTool(t, "hc", `echo '{"terminals":[{"address":"t1","cwd":"`+web+`","state":"live"},{"address":"t2","cwd":"`+stale+`","state":"finished"}]}'`)
	s := &server{hc: hc, home: home}
	rec := httptest.NewRecorder()
	s.folders(rec, httptest.NewRequest("GET", "/api/folders", nil))
	var out struct {
		Home     string
		Recent   []Folder
		Projects []Folder
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err, rec.Body.String())
	}
	paths := func(fs []Folder) []string {
		var p []string
		for _, f := range fs {
			p = append(p, f.Path)
		}
		return p
	}
	if want := []string{web, api, notes}; !reflect.DeepEqual(paths(out.Recent), want) {
		t.Errorf("recent:\n got %q\nwant %q", paths(out.Recent), want)
	}
	if out.Recent[0].Name != "web" {
		t.Errorf("name %q", out.Recent[0].Name)
	}
	if want := []string{deep, wt, stale}; !reflect.DeepEqual(paths(out.Projects), want) {
		t.Errorf("projects:\n got %q\nwant %q", paths(out.Projects), want)
	}
}
