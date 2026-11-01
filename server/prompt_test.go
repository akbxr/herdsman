package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParsePrompt(t *testing.T) {
	cases := []struct {
		name, screen string
		want         *prompt
	}{
		{"tego", `
 ⏺ I'll run the tests.

Allow bash: go test ./...?
›
❯ Yes  run it once
  Yes, always for this session
  No
  3/3 · ↑↓ move · enter select · esc cancel
`, &prompt{Question: "Allow bash: go test ./...?", Options: []string{"Yes", "Yes, always for this session", "No"}, Selected: 0}},
		{"claude", `
╭──────────────────────────────────────────────────────╮
│ Bash command                                         │
│                                                      │
│   npm test -- login                                  │
│                                                      │
│ Do you want to proceed?                              │
│   1. Yes                                             │
│ ❯ 2. Yes, and don't ask again for npm test commands  │
│   3. No, and tell Claude what to do differently (esc)│
╰──────────────────────────────────────────────────────╯
   Esc to cancel · Tab to amend
`, &prompt{Question: "Do you want to proceed?", Options: []string{"Yes", "Yes, and don't ask again for npm test commands", "No, and tell Claude what to do differently (esc)"}, Selected: 1}},
		{"codex", `
  Would you like to run the following command?
  $ cargo build

› 1. Yes, proceed (y)
  2. Yes, and don't ask again for this command (a)
  3. No, and tell Codex what to do differently (esc)

  Press enter to confirm or esc to cancel
`, &prompt{Question: "$ cargo build", Options: []string{"Yes, proceed (y)", "Yes, and don't ask again for this command (a)", "No, and tell Codex what to do differently (esc)"}, Selected: 0}},
		{"no menu: a quote and a prompt line", `
> a quoted line
plain text
❯ 
`, nil},
		{"one marked line alone", "❯ only one\n\nsomething else\n", nil},
	}
	for _, c := range cases {
		got := parsePrompt(strings.TrimPrefix(c.screen, "\n"))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got  %+v\n want %+v", c.name, got, c.want)
		}
	}
}

func TestPickKeys(t *testing.T) {
	p := &prompt{Options: []string{"Yes", "Always", "No"}, Selected: 1}
	for opt, want := range map[string][]string{
		"Yes":    {"up", "enter"},
		"Always": {"enter"},
		"No":     {"down", "enter"},
		"Maybe":  nil,
	} {
		if got := pickKeys(p, opt); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v, want %v", opt, got, want)
		}
	}
	if pickKeys(nil, "Yes") != nil {
		t.Error("no menu: keys")
	}
}

func TestGitChanges(t *testing.T) {
	dir := t.TempDir()
	g := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	write := func(name, body string) { os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644) }
	ctx := context.Background()

	g("init", "-q", "-b", "main")
	if res := gitChanges(ctx, dir); res.Repo {
		t.Fatal("a repository tracking nothing counts as one")
	}
	write("a.go", "package a\n\nfunc A() {}\n")
	write("gone.txt", "bye\n")
	g("add", ".")
	g("commit", "-q", "-m", "init")
	if res := gitChanges(ctx, dir); !res.Repo || res.Branch != "main" || len(res.Files) != 0 {
		t.Fatalf("clean: %+v", res)
	}

	write("a.go", "package a\n\nfunc A() { B() }\n")
	os.Remove(filepath.Join(dir, "gone.txt"))
	write("new.md", "# New\nline\n")
	write("blob.bin", "a\x00b")
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	write("sub/x.txt", "x\n")
	res := gitChanges(ctx, dir)
	got := map[string]changedFile{}
	for _, f := range res.Files {
		got[f.Path] = f
	}
	if a := got["a.go"]; a.Status != "M" || a.Added != 1 || a.Deleted != 1 || !strings.HasPrefix(a.Patch, "@@") || !strings.Contains(a.Patch, "+func A() { B() }") {
		t.Errorf("a.go: %+v", a)
	}
	if d := got["gone.txt"]; d.Status != "D" || d.Deleted != 1 {
		t.Errorf("gone.txt: %+v", d)
	}
	if n := got["new.md"]; n.Status != "?" || n.Added != 2 || n.Patch != "@@ -0,0 +1,2 @@\n+# New\n+line" {
		t.Errorf("new.md: %+v", n)
	}
	if b := got["blob.bin"]; !b.Binary || b.Patch != "" {
		t.Errorf("blob.bin: %+v", b)
	}
	if _, ok := got["sub/x.txt"]; !ok {
		t.Errorf("sub/x.txt missing: %v", got)
	}

	// Same files, same counts, other content: another version.
	v := res.Version
	write("a.go", "package a\n\nfunc A() { C() }\n")
	if again := gitChanges(ctx, dir); again.Version == v {
		t.Error("version didn't change with the content")
	}
}
