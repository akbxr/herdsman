package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// commitRepo makes a repository with a.txt, b.txt, gone.txt and keep.txt
// committed, and the user's own git config out of the way (no global hooks
// or signing). git runs it as the tests' commands do.
func commitRepo(t *testing.T) (dir string, g func(args ...string) string, write func(name, body string)) {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	os.WriteFile(cfg, nil, 0o644)
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL": cfg, "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t",
	} {
		t.Setenv(k, v)
	}
	dir = t.TempDir()
	g = func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return string(out)
	}
	write = func(name, body string) {
		t.Helper()
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	g("init", "-q", "-b", "main")
	for _, f := range []string{"a.txt", "b.txt", "gone.txt", "keep.txt"} {
		write(f, f+"\n")
	}
	g("add", ".")
	g("commit", "-q", "-m", "init")
	return dir, g, write
}

func porcelain(g func(...string) string) string {
	return strings.TrimRight(g("status", "--porcelain", "--untracked-files=all"), "\n")
}

func TestCommitPicked(t *testing.T) {
	dir, g, write := commitRepo(t)
	ctx := context.Background()
	write("a.txt", "a changed\n")
	write("b.txt", "b changed\n")
	os.Remove(filepath.Join(dir, "gone.txt"))
	write("src/new.go", "package src\n")
	write("other.txt", "not picked\n")
	write("staged.txt", "staged by the agent\n")
	g("add", "staged.txt")

	res, err := gitCommit(ctx, dir, "feat: pick some\n\n- body line", []string{"a.txt", "gone.txt", "src/new.go", "a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Subject != "feat: pick some" || len(res.Hash) < 7 || res.Files != 3 {
		t.Errorf("result: %+v", res)
	}
	if got := g("show", "--name-status", "--format=%B", "HEAD"); !strings.Contains(got, "- body line") ||
		!strings.Contains(got, "M\ta.txt") || !strings.Contains(got, "D\tgone.txt") || !strings.Contains(got, "A\tsrc/new.go") ||
		strings.Contains(got, "b.txt") || strings.Contains(got, "staged.txt") || strings.Contains(got, "other.txt") {
		t.Errorf("commit:\n%s", got)
	}
	// What wasn't picked is as it was: b.txt changed, other.txt new, staged.txt still staged.
	if st := porcelain(g); st != " M b.txt\nA  staged.txt\n?? other.txt" {
		t.Errorf("status after:\n%s", st)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "a changed\n" {
		t.Errorf("a.txt in the worktree: %q", b)
	}
}

// Names that look like globs or pathspec magic name only themselves.
func TestCommitLiteralNames(t *testing.T) {
	dir, g, write := commitRepo(t)
	ctx := context.Background()
	write("a1.txt", "1\n")
	write("a*.txt", "star\n")
	write("n[1].txt", "n\n")
	write("n1.txt", "n1\n")
	if _, err := gitCommit(ctx, dir, "chore: star", []string{"a*.txt"}); err != nil {
		t.Fatal(err)
	}
	if got := g("show", "--name-only", "--format=", "HEAD"); got != "a*.txt\n" {
		t.Errorf("committed %q", got)
	}
	if err := gitDiscard(ctx, dir, "n[1].txt"); err != nil {
		t.Fatal(err)
	}
	if st := porcelain(g); st != "?? a1.txt\n?? n1.txt" {
		t.Errorf("status:\n%s", st)
	}
	// A tracked one, restored by git.
	g("add", "a1.txt")
	g("commit", "-q", "-m", "a1")
	write("a1.txt", "1 changed\n")
	write("a*.txt", "star changed\n")
	if err := gitDiscard(ctx, dir, "a*.txt"); err != nil {
		t.Fatal(err)
	}
	if st := porcelain(g); st != " M a1.txt\n?? n1.txt" {
		t.Errorf("status after restoring a*.txt:\n%s", st)
	}
}

func TestCommitHookFails(t *testing.T) {
	dir, g, write := commitRepo(t)
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	os.WriteFile(hook, []byte("#!/bin/sh\necho 'lint: 2 problems in new.txt' >&2\nexit 1\n"), 0o755)
	write("a.txt", "a changed\n")
	write("new.txt", "new\n")
	before := g("rev-parse", "HEAD")

	_, err := gitCommit(context.Background(), dir, "fix: x", []string{"a.txt", "new.txt"})
	if err == nil || !strings.Contains(err.Error(), "lint: 2 problems in new.txt") {
		t.Fatalf("hook error not passed on: %v", err)
	}
	var bad errBadRequest
	if errors.As(err, &bad) {
		t.Error("a hook's failure counts as a bad request")
	}
	if g("rev-parse", "HEAD") != before {
		t.Error("committed anyway")
	}
	// The new file is untracked again, not left half-added.
	if st := porcelain(g); st != " M a.txt\n?? new.txt" {
		t.Errorf("status after a failed commit:\n%s", st)
	}
}

func TestCommitRefuses(t *testing.T) {
	dir, _, write := commitRepo(t)
	ctx := context.Background()
	write("a.txt", "a changed\n")
	os.Symlink("/etc/hosts", filepath.Join(dir, "link"))
	for _, c := range []struct {
		name, msg string
		paths     []string
	}{
		{"no message", "  \n", []string{"a.txt"}},
		{"no files", "chore: x", nil},
		{"unchanged file", "chore: x", []string{"keep.txt"}},
		{"absolute", "chore: x", []string{filepath.Join(dir, "a.txt")}},
		{"dot dot", "chore: x", []string{"../a.txt"}},
		{"inner dot dot", "chore: x", []string{"src/../a.txt"}},
		{"nul", "chore: x", []string{"a.txt\x00b"}},
		{"glob", "chore: x", []string{"*.txt"}},
		{"magic", "chore: x", []string{":(glob)*"}},
		{"missing", "chore: x", []string{"nope.txt"}},
	} {
		_, err := gitCommit(ctx, dir, c.msg, c.paths)
		var bad errBadRequest
		if !errors.As(err, &bad) {
			t.Errorf("%s: %v, want a refusal", c.name, err)
		}
	}
}

func TestCommitInitial(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t"} {
		t.Setenv(k, v)
	}
	dir := t.TempDir()
	exec.Command("git", "-C", dir, "init", "-q", "-b", "main").Run()
	os.WriteFile(filepath.Join(dir, "first.txt"), []byte("1\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "later.txt"), []byte("2\n"), 0o644)
	res, err := gitCommit(context.Background(), dir, "chore: first", []string{"first.txt"})
	if err != nil || res.Subject != "chore: first" {
		t.Fatalf("%+v %v", res, err)
	}
	out, _ := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if string(out) != "?? later.txt\n" {
		t.Errorf("status: %q", out)
	}
}

func TestDiscard(t *testing.T) {
	dir, g, write := commitRepo(t)
	ctx := context.Background()
	write("a.txt", "a changed\n")
	g("add", "a.txt")
	write("a.txt", "a changed again\n") // staged and unstaged: both go
	os.Remove(filepath.Join(dir, "gone.txt"))
	write("src/new.txt", "new\n")
	write("added.txt", "added\n")
	g("add", "added.txt")

	for _, p := range []string{"a.txt", "gone.txt", "src/new.txt", "added.txt"} {
		if err := gitDiscard(ctx, dir, p); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	if st := porcelain(g); st != "" {
		t.Errorf("not clean:\n%s", st)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "a.txt\n" {
		t.Errorf("a.txt: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "gone.txt")); string(b) != "gone.txt\n" {
		t.Errorf("gone.txt: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "src", "new.txt")); !os.IsNotExist(err) {
		t.Error("src/new.txt still there")
	}
}

func TestDiscardRefuses(t *testing.T) {
	dir, _, write := commitRepo(t)
	ctx := context.Background()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(outside, []byte("keep me\n"), 0o644)
	os.Symlink(outside, filepath.Join(dir, "escape"))            // a new link out of the repository
	os.Symlink(filepath.Dir(outside), filepath.Join(dir, "out")) // a new link to a folder outside
	os.Symlink("keep.txt", filepath.Join(dir, "inner-link"))     // a new link inside: may go
	// A nested repository shows as one new entry, a folder.
	nested := filepath.Join(dir, "nested")
	os.MkdirAll(nested, 0o755)
	exec.Command("git", "-C", nested, "init", "-q").Run()
	os.WriteFile(filepath.Join(nested, "x"), []byte("x"), 0o644)
	write("a.txt", "a changed\n")

	for _, p := range []string{"keep.txt", "../secret.txt", outside, "src/../a.txt", "escape", "out", "nested/", "nested", "", "a.txt\x00"} {
		err := gitDiscard(ctx, dir, p)
		var bad errBadRequest
		if !errors.As(err, &bad) {
			t.Errorf("%q: %v, want a refusal", p, err)
		}
	}
	if b, err := os.ReadFile(outside); err != nil || string(b) != "keep me\n" {
		t.Errorf("the file outside: %q %v", b, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "escape")); err != nil {
		t.Error("the link out was removed")
	}
	if _, err := os.Stat(filepath.Join(nested, "x")); err != nil {
		t.Error("the nested repository was touched")
	}
	if err := gitDiscard(ctx, dir, "inner-link"); err != nil {
		t.Errorf("a link inside: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "inner-link")); !os.IsNotExist(err) {
		t.Error("inner-link still there")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "keep.txt")); string(b) != "keep.txt\n" {
		t.Errorf("the link's target went too: %q", b)
	}
}

func TestDiscardThroughLinkedFolder(t *testing.T) {
	dir, g, _ := commitRepo(t)
	// A folder link inside the repository to one outside: git lists the
	// link, never the files behind it, so nothing out there can be named.
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "f.txt"), []byte("f\n"), 0o644)
	os.Symlink(outside, filepath.Join(dir, "lnk"))
	if err := gitDiscard(context.Background(), dir, "lnk/f.txt"); err == nil {
		t.Error("discarded a file behind a link")
	}
	// Even if git did list it, the check on the file's folder refuses.
	if err := newFileInside(dir, "lnk/f.txt"); err == nil {
		t.Error("newFileInside let a file through a linked folder")
	}
	if _, err := os.Stat(filepath.Join(outside, "f.txt")); err != nil {
		t.Error("the file outside is gone")
	}
	_ = g
}

func TestSuggestMessage(t *testing.T) {
	cases := []struct {
		set  map[string]string
		want string
	}{
		{map[string]string{"server/commit.go": "?"}, "feat(server): add commit.go"},
		{map[string]string{"README.md": "M"}, "docs: update README.md"},
		{map[string]string{"server/commit_test.go": "?", "server/chat_test.go": "M"}, "test(server): update chat_test.go and commit_test.go\n\n- Update server/chat_test.go\n- Add server/commit_test.go"},
		{map[string]string{"go.mod": "M", "go.sum": "M"}, "build: update go.mod and go.sum\n\n- Update go.mod\n- Update go.sum"},
		{map[string]string{"a/x.go": "D", "b/y.go": "D"}, "refactor: remove x.go and y.go\n\n- Remove a/x.go\n- Remove b/y.go"},
		{map[string]string{"android/A.kt": "M", "android/B.kt": "M", "android/C.kt": "M", "android/D.kt": "?"},
			"feat(android): update 4 files\n\n- Update android/A.kt\n- Update android/B.kt\n- Update android/C.kt\n- Add android/D.kt"},
		{map[string]string{"x/latest.go": "M"}, "chore(x): update latest.go"},
		{map[string]string{".github/workflows/ci.yml": "M"}, "ci: update ci.yml"},
	}
	for _, c := range cases {
		if got := suggestMessage(c.set); got != c.want {
			t.Errorf("%v:\n got  %q\n want %q", c.set, got, c.want)
		}
	}
	if suggestMessage(map[string]string{}) != "" {
		t.Error("a message for nothing")
	}
}
