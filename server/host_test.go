package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeRemote writes an hc whose `exec` runs the command here, as hc runs it
// on a machine (argv as given, no shell; --cwd, --env; the result as JSON
// with base64 output), and that records every call. Anything else runs body.
func fakeRemote(t *testing.T, body string) (bin, calls string) {
	t.Helper()
	return fakeTool(t, "hc", `if [ "$1" = exec ]; then
  shift; cwd=$HOME
  while [ $# -gt 0 ]; do
    case "$1" in
    --json) shift;;
    --on|--timeout) shift 2;;
    --cwd) cwd=$2; shift 2;;
    --env) export "$2"; shift 2;;
    --) shift; break;;
    *) break;;
    esac
  done
  d=$(mktemp -d)
  ( cd "$cwd" || exit 1; exec "$@" ) </dev/null >"$d/o" 2>"$d/e"; c=$?
  printf '{"code":%d,"stdout":"%s","stderr":"%s","timed_out":false,"truncated":false}\n' "$c" "$(base64 < "$d/o" | tr -d '\n')" "$(base64 < "$d/e" | tr -d '\n')"
  rm -rf "$d"
  exit 0
fi
`+body)
}

// remoteHost: a machine named name whose commands run here (fakeRemote).
func remoteHost(t *testing.T, name string) (host, string) {
	t.Helper()
	hc, calls := fakeRemote(t, "exit 1")
	homes.Lock()
	delete(homes.m, name)
	homes.Unlock()
	return host{machine: name, hc: hc}, calls
}

func TestSplitTerminal(t *testing.T) {
	for addr, want := range map[string][2]string{"t7": {"", "t7"}, "box/t7": {"box", "t7"}, "my.box-1/t12": {"my.box-1", "t12"}} {
		if m, l := splitTerminal(addr); m != want[0] || l != want[1] {
			t.Errorf("%s: %q %q", addr, m, l)
		}
	}
	s := &server{hc: "/x/hc", home: "/h"}
	if h := s.hostOf("t1"); h.remote() || h.home != "/h" {
		t.Errorf("local: %+v", h)
	}
	if h := s.hostOf("box/t1"); !h.remote() || h.machine != "box" || h.hc != "/x/hc" {
		t.Errorf("remote: %+v", h)
	}
	for _, m := range []string{"box", "a.b_c-d", ""} {
		if !validMachine(m) {
			t.Errorf("%q refused", m)
		}
	}
	for _, m := range []string{"-x", "a b", "a/b", "a;b", strings.Repeat("a", 65)} {
		if validMachine(m) {
			t.Errorf("%q accepted", m)
		}
	}
}

func TestExecArgs(t *testing.T) {
	h := host{machine: "box", hc: "hc"}
	got := h.execArgs(30*time.Second, "/w d", []string{"A=1"}, []string{"git", "-C", "/a b", "--", "-x", "$(rm -rf ~)", ""})
	want := []string{"exec", "--json", "--on", "box", "--timeout", "28", "--cwd", "/w d", "--env", "A=1",
		"--", "git", "-C", "/a b", "--", "-x", "$(rm -rf ~)", ""}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	// One word that hc would hand a shell as a line goes through env.
	if got := h.execArgs(time.Second, "", nil, []string{"/opt/my tools/x"}); !reflect.DeepEqual(got[len(got)-3:], []string{"--", "env", "/opt/my tools/x"}) {
		t.Errorf("a lone program with a space: %q", got)
	}
	if got := h.execArgs(time.Hour, "", nil, []string{"pwd"}); !reflect.DeepEqual(got[4:], []string{"--timeout", "600", "--", "pwd"}) {
		t.Errorf("a plain program: %q", got)
	}
}

func TestHostRemote(t *testing.T) {
	h, calls := remoteHost(t, "rbox")
	ctx := context.Background()
	dir := t.TempDir()
	tricky := []string{"a b", `it's "quoted"`, "$HOME", "-n", "", "*", "x\ny", `back\slash`}

	// Words reach the program as they are.
	out, err := h.run(ctx, dir, append([]string{"printf", "[%s]"}, tricky...)...)
	if err != nil {
		t.Fatal(err)
	}
	if want := "[" + strings.Join(tricky, "][") + "]"; string(out) != want {
		t.Errorf("run: %q, want %q", out, want)
	}
	if !strings.Contains(readCalls(t, calls), "--on rbox") {
		t.Errorf("calls: %s", readCalls(t, calls))
	}
	if out, _ := h.run(ctx, dir, "pwd", "-P"); strings.TrimSpace(string(out)) != mustEval(t, dir) {
		t.Errorf("--cwd: %q", out)
	}
	// A nonzero exit is an error with what it said.
	if _, err := h.run(ctx, "", "sh", "-c", "echo oops >&2; exit 3"); err == nil || err.Error() != "oops" {
		t.Errorf("failure: %v", err)
	}

	// A batch: each command's own output and exit, binary output intact.
	res, err := h.batch(ctx, dir, [][]string{
		append([]string{"printf", "%s\\0"}, tricky...),
		{"sh", "-c", "printf 'no newline'; exit 1"},
		{"no-such-program-here"},
		{"printf", "\\n\\nlast\\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Join(tricky, "\x00") + "\x00"; string(res[0].out) != want || !res[0].ok {
		t.Errorf("batch 0: %q %v", res[0].out, res[0].ok)
	}
	if string(res[1].out) != "no newline" || res[1].ok {
		t.Errorf("batch 1: %q %v", res[1].out, res[1].ok)
	}
	if res[2].ok {
		t.Errorf("batch 2: %+v", res[2])
	}
	if string(res[3].out) != "\n\nlast\n" || !res[3].ok {
		t.Errorf("batch 3: %q", res[3].out)
	}

	// Files: kinds, sizes, pieces from an offset.
	f := filepath.Join(dir, "f.txt")
	os.WriteFile(f, []byte("0123456789"), 0o644)
	os.Symlink(f, filepath.Join(dir, "link"))
	sts, err := h.stat(ctx, f, dir, filepath.Join(dir, "missing"), filepath.Join(dir, "link"), filepath.Join(dir, "with space"))
	if err != nil {
		t.Fatal(err)
	}
	if sts[0].kind != "f" || sts[0].size != 10 || sts[0].link || time.Since(sts[0].mtime) > time.Minute {
		t.Errorf("file: %+v", sts[0])
	}
	if sts[1].kind != "d" || sts[2].kind != "" || sts[3].kind != "f" || !sts[3].link || sts[4].kind != "" {
		t.Errorf("stat: %+v", sts)
	}
	for _, c := range []struct {
		from, n int64
		want    string
	}{{0, 100, "0123456789"}, {3, 4, "3456"}, {9, 5, "9"}, {10, 5, ""}, {-3, 3, "789"}, {-30, 30, "0123456789"}} {
		b, size, err := h.readFrom(ctx, f, c.from, c.n)
		lb, lsize, _ := host{}.readFrom(ctx, f, c.from, c.n)
		if err != nil || string(b) != c.want || size != 10 || string(lb) != c.want || lsize != 10 {
			t.Errorf("readFrom %d %d: %q %d %v (here: %q)", c.from, c.n, b, size, err, lb)
		}
	}
	if _, _, err := h.readFrom(ctx, filepath.Join(dir, "missing"), 0, 5); err == nil {
		t.Error("reading a missing file")
	}
	if b, _, _ := h.readAll(ctx, f, 2, 5); string(b) != "23456" {
		t.Errorf("readAll: %q", b)
	}
	if !h.isDir(ctx, dir) || h.isDir(ctx, f) {
		t.Error("isDir")
	}
	home, err := h.homeDir(ctx)
	if err != nil || home != os.Getenv("HOME") {
		t.Errorf("home %q %v", home, err)
	}
	ents, err := h.listDir(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, fmt.Sprintf("%s:%v", e.name, e.dir))
	}
	sort.Strings(names)
	if strings.Join(names, " ") != "f.txt:false link:false" {
		t.Errorf("listDir: %v", names)
	}
}

func mustEval(t *testing.T, p string) string {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestHostRemoteErrors(t *testing.T) {
	hc, _ := fakeTool(t, "hc", `echo '{"error":{"hint":"hc machine ls","kind":"machine_unknown","message":"no machine gone"}}'; exit 1`)
	h := host{machine: "gone", hc: hc}
	_, err := h.run(context.Background(), "", "true")
	var he hcErr
	if !errors.As(err, &he) || he.kind != "machine_unknown" || !strings.Contains(err.Error(), "no machine gone (hc machine ls)") {
		t.Errorf("got %v", err)
	}
	// Not a repository vs. not reachable.
	if _, err := repoRootOn(context.Background(), h, "/x"); !errors.As(err, &he) {
		t.Errorf("repoRoot on an unreachable machine: %v", err)
	}
}

// changesShape: what the phone sees of changes, without the version.
func changesShape(c changesResult) string {
	c.Version = ""
	b, _ := json.Marshal(c)
	return string(b)
}

func TestRemoteChangesMatchLocal(t *testing.T) {
	dir, g, write := commitRepo(t)
	h, calls := remoteHost(t, "cbox")
	ctx := context.Background()
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	write("a.txt", "a changed\n")
	os.Remove(filepath.Join(dir, "gone.txt"))
	write("new file.txt", "hello\nworld\n")
	write("sub/deep.txt", "deep\n")
	os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{0, 1, 2, 0, 3}, 0o644)
	g("add", "bin.dat")
	write("big.txt", strings.Repeat("x", maxNewFileBytes+10))

	for _, cwd := range []string{dir, filepath.Join(dir, "sub")} {
		local := gitChanges(ctx, cwd)
		remote, err := gitChangesOn(ctx, h, cwd)
		if err != nil {
			t.Fatal(err)
		}
		if changesShape(local) != changesShape(remote) {
			t.Errorf("from %s:\nlocal  %s\nremote %s", cwd, changesShape(local), changesShape(remote))
		}
		if len(remote.Files) < 6 || remote.Version == "" {
			t.Errorf("remote: %+v", remote)
		}
	}
	// Few round trips: the root once (kept), then two batches a poll.
	before := strings.Count(readCalls(t, calls), "exec --json")
	gitChangesOn(ctx, h, dir)
	if n := strings.Count(readCalls(t, calls), "exec --json") - before; n != 2 {
		t.Errorf("%d hc calls for a poll, want 2", n)
	}
	// Outside a repository: nothing, no error.
	if res, err := gitChangesOn(ctx, h, t.TempDir()); err != nil || res.Repo {
		t.Errorf("not a repository: %+v %v", res, err)
	}
}

func TestRemoteCommitAndDiscard(t *testing.T) {
	dir, g, write := commitRepo(t)
	h, _ := remoteHost(t, "gbox")
	ctx := context.Background()
	write("a.txt", "a changed\n")
	write("new.txt", "new\n")
	write("b.txt", "b changed\n")
	res, err := gitCommitOn(ctx, h, dir, "remote commit", []string{"a.txt", "new.txt"})
	if err != nil || res.Subject != "remote commit" {
		t.Fatalf("commit: %+v %v", res, err)
	}
	if p := porcelain(g); p != " M b.txt" {
		t.Errorf("after commit: %q", p)
	}
	if err := gitDiscardOn(ctx, h, dir, "b.txt"); err != nil {
		t.Fatal(err)
	}
	// New files: a plain one goes; links out and folders stay.
	write("fresh.txt", "x")
	outside := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(outside, []byte("keep"), 0o644)
	os.Symlink(outside, filepath.Join(dir, "escape"))
	os.MkdirAll(filepath.Join(dir, "folder"), 0o755)
	os.WriteFile(filepath.Join(dir, "folder", "x"), []byte("x"), 0o644)
	if err := gitDiscardOn(ctx, h, dir, "fresh.txt"); err != nil {
		t.Errorf("a new file: %v", err)
	}
	for _, p := range []string{"escape", "../secret.txt", "folder/x/.."} {
		var bad errBadRequest
		if err := gitDiscardOn(ctx, h, dir, p); !errors.As(err, &bad) {
			t.Errorf("%s: %v, want a refusal", p, err)
		}
	}
	for _, c := range []struct{ p, want string }{{"escape", "links outside"}, {"folder", "a folder"}} {
		if err := h.newFileInside(ctx, dir, c.p); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.p, err)
		}
	}
	if b, _ := os.ReadFile(outside); string(b) != "keep" {
		t.Error("the file outside changed")
	}
	if p := porcelain(g); p != "?? escape\n?? folder/x" {
		t.Errorf("after discards: %q", p)
	}
}

func TestRemoteFilesAndCommands(t *testing.T) {
	dir, _, write := commitRepo(t)
	h, _ := remoteHost(t, "fbox")
	ctx := context.Background()
	write("untracked.go", "x")
	files, err := listFilesOn(ctx, h, dir)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	if strings.Join(files, " ") != "a.txt b.txt gone.txt keep.txt untracked.go" {
		t.Errorf("files: %v", files)
	}
	plain := t.TempDir()
	os.MkdirAll(filepath.Join(plain, "a", "b"), 0o755)
	os.MkdirAll(filepath.Join(plain, ".hidden"), 0o755)
	os.MkdirAll(filepath.Join(plain, "node_modules"), 0o755)
	for _, f := range []string{"top.txt", "a/b/c.txt", ".hidden/x", "node_modules/y"} {
		os.WriteFile(filepath.Join(plain, f), nil, 0o644)
	}
	files, _ = listFilesOn(ctx, h, plain)
	sort.Strings(files)
	if strings.Join(files, " ") != "a/b/c.txt top.txt" {
		t.Errorf("outside a repository: %v", files)
	}

	// Commands: the same as here, from the same home.
	home := t.TempDir()
	t.Setenv("HOME", home)
	proj := filepath.Join(home, "Code", "app")
	for f, body := range map[string]string{
		".claude/commands/ship.md":         "---\ndescription: Ship it\n---\nbody",
		".claude/skills/pdf/SKILL.md":      "---\nname: pdf-tools\ndescription: PDFs\n---\n",
		"Code/app/.claude/commands/fix.md": "# Fix the thing\n",
		"Code/app/.tego/skills/x/SKILL.md": "no front matter\n",
	} {
		p := filepath.Join(home, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	for _, agent := range []string{"tego", "claude", "codex", "zsh"} {
		h, _ := remoteHost(t, "cmdbox-"+agent)
		got, err := commandsOn(ctx, h, agent, proj)
		if err != nil {
			t.Fatal(err)
		}
		if want := commandsFor(agent, proj); !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\nremote %v\nlocal  %v", agent, got, want)
		}
	}
}

func TestRemoteChat(t *testing.T) {
	h, _ := remoteHost(t, "chatbox")
	ctx := context.Background()
	dir := t.TempDir()
	pic := testPNG(t, 8, 8)
	picPath := filepath.Join(dir, "shot.png")
	os.WriteFile(picPath, pic, 0o644)
	msg := func(id, parent, role, text string) string {
		p := "null"
		if parent != "" {
			p = `"` + parent + `"`
		}
		return `{"type":"message","id":"` + id + `","parent":` + p + `,"message":{"role":"` + role + `","content":[{"type":"text","text":"` + text + `"}]}}` + "\n"
	}
	p := filepath.Join(dir, "s.jsonl")
	os.WriteFile(p, []byte(`{"type":"header","id":"s"}`+"\n"+msg("u1", "", "user", "look at "+picPath)+msg("a1", "u1", "assistant", "a picture")), 0o644)
	read := func(h host) []ChatItem {
		t.Helper()
		size, mtime, err := h.statFile(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		r, err := readChatOn(ctx, h, "tego", p, size, mtime, chatMaxItems)
		if err != nil {
			t.Fatal(err)
		}
		return r.items
	}
	remote := read(h)
	if len(remote) != 2 || remote[0].Text != "look at" || len(remote[0].Images) != 1 || remote[1].Text != "a picture" {
		t.Fatalf("remote: %+v", remote)
	}
	if rec := getImage(t, "id="+remote[0].Images[0]); rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), pic) || rec.Header().Get("Content-Type") != "image/png" {
		t.Errorf("a picture on the machine: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	// Written to since: read on from where it was.
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(msg("u2", "a1", "user", "more") + msg("a2", "u2", "assistant", "done"))
	f.Close()
	if got := noIDs(read(h)); len(got) != 4 || got[3].Text != "done" {
		t.Errorf("after more: %+v", got)
	}
	if !reflect.DeepEqual(noIDs(read(h))[2:], noIDs(read(host{}))[2:]) {
		t.Error("remote and local differ")
	}
}

func TestLocateTranscript(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		if _, err := os.Stat("/usr/sbin/lsof"); err != nil {
			t.Skip("no lsof")
		}
	}
	h, _ := remoteHost(t, "lbox")
	home := mustEval(t, t.TempDir()) // lsof names files by their real path
	tr := filepath.Join(home, ".tego", "sessions", "--x--", "s.jsonl")
	os.MkdirAll(filepath.Dir(tr), 0o755)
	os.WriteFile(tr, []byte("{}\n"), 0o644)
	// A process holding the transcript open, under a parent like an agent's.
	cmd := exec.Command("sh", "-c", `sh -c 'exec 3<"$1"; sleep 30' sh "$0"; :`, tr)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); cmd.Wait() }()
	ctx := context.Background()
	var got string
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		if got, _ = locateTranscript(ctx, h, "tego", home, cmd.Process.Pid); got != "" {
			break
		}
	}
	if real, _ := filepath.EvalSymlinks(tr); got != tr && got != real {
		t.Errorf("tego: %q, want %q", got, tr)
	}
	// Claude Code: its per-process file names the session.
	os.MkdirAll(filepath.Join(home, ".claude", "sessions"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude", "sessions", fmt.Sprintf("%d.json", cmd.Process.Pid)),
		[]byte(`{"sessionId":"abc-123","cwd":"/Users/me/app"}`), 0o644)
	want := claudeSessionIn(home, []byte(`{"sessionId":"abc-123","cwd":"/Users/me/app"}`))
	if got, err := locateTranscript(ctx, h, "claude", home, cmd.Process.Pid); err != nil || got != want || want == "" {
		t.Errorf("claude: %q %v, want %q", got, err, want)
	}
	if got, _ := locateTranscript(ctx, h, "codex", home, cmd.Process.Pid); got != "" {
		t.Errorf("codex: %q", got)
	}
}
