package main

// A check against a real machine of hc's, read-only, skipped unless asked:
//
//	HERDSMAN_LIVE=box HERDSMAN_LIVE_TERMINAL=box/t1 go test -run TestLive -v
//
// (run outside a Herdr terminal, or with HC_TERMINAL_ID and HC_TERMINAL_SOCKET
// unset). It reads that terminal's folder through the machine and compares
// it with this Mac's reading when the machine shares this Mac's disk (one
// added with --via 'sh -c'); for the transcript it starts one throwaway
// terminal there holding a file open in a temporary folder, and kills it.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestLive(t *testing.T) {
	machine, terminal := os.Getenv("HERDSMAN_LIVE"), os.Getenv("HERDSMAN_LIVE_TERMINAL")
	if machine == "" || terminal == "" {
		t.Skip("set HERDSMAN_LIVE and HERDSMAN_LIVE_TERMINAL")
	}
	s := &server{hc: findTool("hc")}
	h := s.on(machine)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	home, err := h.homeDir(ctx)
	if err != nil {
		t.Fatal(err)
	}
	localHome, _ := os.UserHomeDir()
	shared := home == localHome && h.isDir(ctx, localHome)
	t.Logf("%s: home %s (shares this Mac's disk: %v)", machine, home, shared)

	now, err := s.terminalNow(ctx, terminal)
	if err != nil {
		t.Fatal(err)
	}
	cwd := now.fg.Cwd
	t.Logf("%s: cwd %s, foreground %s pid %d", terminal, cwd, now.fg.Name, now.fg.Pid)

	start := time.Now()
	remote, err := gitChangesOn(ctx, h, cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("changes: repo %v, branch %q, %d files, %s", remote.Repo, remote.Branch, len(remote.Files), time.Since(start).Round(time.Millisecond))
	start = time.Now()
	gitChangesOn(ctx, h, cwd)
	t.Logf("changes again (root kept): %s", time.Since(start).Round(time.Millisecond))
	files, err := listFilesOn(ctx, h, cwd)
	if err != nil {
		t.Fatal(err)
	}
	cmds, err := commandsOn(ctx, h, "claude", cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("files: %d, claude commands: %d", len(files), len(cmds))
	if shared {
		if local := gitChanges(ctx, cwd); changesShape(local) != changesShape(remote) {
			t.Errorf("changes differ:\nlocal  %s\nremote %s", changesShape(local), changesShape(remote))
		}
		local := listFiles(ctx, cwd)
		sort.Strings(local)
		sort.Strings(files)
		if !reflect.DeepEqual(local, files) {
			t.Errorf("files differ: %d here, %d there", len(local), len(files))
		}
		if local := commandsFor("claude", cwd); !reflect.DeepEqual(local, cmds) {
			t.Errorf("commands differ:\nlocal  %v\nremote %v", local, cmds)
		}
	}

	// The endpoints, read-only.
	s.herdr = findTool("herdr-app")
	q := "?terminal=" + terminal
	for _, c := range []struct {
		h    func(w http.ResponseWriter, r *http.Request)
		path string
		key  string
	}{
		{s.machinesHandler, "/api/machines", "machines"},
		{s.folders, "/api/folders?machine=" + machine, "projects"},
		{s.agentsHandler, "/api/agents?machine=" + machine, "agents"},
		{s.changes, "/api/changes" + q, "files"},
		{s.files, "/api/files" + q + "&q=go", "files"},
		{s.commands, "/api/commands" + q, "commands"},
		{s.chat, "/api/chat" + q, "items"},
		{s.modelsHandler, "/api/models" + q, "supported"},
		{s.sessions, "/api/sessions?machine=" + machine, "note"},
		{s.state, "/api/state", "machines"},
	} {
		start := time.Now()
		code, out := call(c.h, "GET", c.path, "")
		v, has := out[c.key]
		b, _ := json.Marshal(v)
		if code != 200 || !has {
			t.Errorf("%s: %d %v", c.path, code, out)
		}
		t.Logf("%s: %d in %s, %s: %s", c.path, code, time.Since(start).Round(time.Millisecond), c.key, firstLines(string(b), 1, 160))
	}

	// The process tree there, with nothing to find under a shell.
	if now.fg.Pid > 0 {
		if p, err := locateTranscript(ctx, h, "tego", home, now.fg.Pid); err != nil || p != "" {
			t.Errorf("a shell's transcript: %q %v", p, err)
		}
	}

	if !shared {
		return
	}
	// A repository with changes of every kind, read here and there.
	dir, g, write := commitRepo(t)
	dir, _ = filepath.EvalSymlinks(dir)
	write("a.txt", "a changed\n")
	os.Remove(filepath.Join(dir, "gone.txt"))
	write("new file.txt", "hello\n")
	write("sub/deep.txt", "deep\n")
	os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{0, 1, 2}, 0o644)
	g("add", "bin.dat")
	remote, err = gitChangesOn(ctx, h, dir)
	if local := gitChanges(ctx, dir); err != nil || changesShape(local) != changesShape(remote) || len(remote.Files) != 5 {
		t.Errorf("a repository with changes: %v\nlocal  %s\nremote %s", err, changesShape(local), changesShape(remote))
	}
	if err := gitDiscardOn(ctx, h, dir, "new file.txt"); err != nil {
		t.Errorf("discard a new file there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new file.txt")); !os.IsNotExist(err) {
		t.Errorf("the new file is still there: %v", err)
	}
	t.Logf("a repository with changes: %d files, the same here and there; a new file discarded there", len(remote.Files))

	// A throwaway terminal there holding a transcript open in a temporary home.
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tr := filepath.Join(tmp, ".tego", "sessions", "--live--", "s.jsonl")
	os.MkdirAll(filepath.Dir(tr), 0o755)
	os.WriteFile(tr, []byte(`{"type":"header","id":"s"}`+"\n"+
		`{"type":"message","id":"u1","parent":null,"message":{"role":"user","content":[{"type":"text","text":"live question"}]}}`+"\n"+
		`{"type":"message","id":"a1","parent":"u1","message":{"role":"assistant","content":[{"type":"text","text":"live answer"}]}}`+"\n"), 0o644)
	out, err := exec.CommandContext(ctx, s.hc, "run", "--json", "--bg", "--rm", "--no-parent", "--on", machine, "--",
		"sh", "-c", `exec 3<"$1"; sleep 120`, "sh", tr).Output()
	if err != nil {
		t.Fatalf("hc run: %v %s", err, out)
	}
	var started struct{ Address string }
	json.Unmarshal(out, &started)
	if !strings.HasPrefix(started.Address, machine+"/") {
		t.Fatalf("hc run said %s", out)
	}
	t.Logf("started %s", started.Address)
	defer func() {
		out, err := exec.Command(s.hc, "kill", "--json", started.Address).CombinedOutput()
		t.Logf("killed %s: %v %s", started.Address, err, strings.TrimSpace(string(out)))
	}()
	var got string
	for end := time.Now().Add(10 * time.Second); got == "" && time.Now().Before(end); time.Sleep(300 * time.Millisecond) {
		tn, err := s.terminalNow(ctx, started.Address)
		if err != nil || tn.fg.Pid <= 0 {
			continue
		}
		got, err = locateTranscript(ctx, h, "tego", tmp, tn.fg.Pid)
		if err != nil {
			t.Fatal(err)
		}
	}
	if got != tr {
		t.Fatalf("located %q, want %q", got, tr)
	}
	size, mtime, err := h.statFile(ctx, got)
	if err != nil {
		t.Fatal(err)
	}
	r, err := readChatOn(ctx, h, "tego", got, size, mtime, chatMaxItems)
	items := r.items
	if err != nil || len(items) != 2 || items[0].Text != "live question" || items[1].Text != "live answer" {
		t.Errorf("chat: %+v %v", items, err)
	}
	t.Logf("transcript located and read through %s", machine)
}
