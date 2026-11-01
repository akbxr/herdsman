package main

// Committing and discarding from the phone's Changes view, in the git root
// of the folder the agent works in.
//
// A commit takes exactly the files picked: git's own partial commit
// (git commit --only -- <paths>) builds it from HEAD plus those paths, so
// whatever else is staged stays staged and out of it. New files are first
// added with --intent-to-add (a partial commit only takes paths git knows),
// and taken out of the index again when the commit fails. It is a plain
// git commit: the repository's hooks and config apply, and what they print
// on failure goes back to the phone as it is.
//
// Discard puts one file back as the last commit has it; a new file is
// deleted, only when it is a regular file (or a link to one) inside the
// repository.
//
// Paths go after "--" as literal pathspecs (no globs, no other magic), and
// each must be one of the files git lists as changed right now.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	maxCommitMessage = 20000
	maxCommitPaths   = 5000
	maxGitError      = 6000
)

// errBadRequest marks a request the phone shouldn't have sent (400); other
// failures are git's (422) and are shown as they are.
type errBadRequest struct{ msg string }

func (e errBadRequest) Error() string { return e.msg }

// gitDo runs git in dir for a change: no prompts, and on failure the error
// is what git (and its hooks) printed, trimmed. Paths go through literal().
func gitDo(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true")
	var stdout, both bytes.Buffer
	cmd.Stdout = &multi{&stdout, &both}
	cmd.Stderr = &both
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(both.String())
		if ctx.Err() != nil {
			msg = strings.TrimSpace(msg + "\n(git took too long and was stopped)")
		}
		if msg == "" {
			msg = err.Error()
		}
		if len(msg) > maxGitError {
			msg = "…" + msg[len(msg)-maxGitError:]
		}
		return stdout.Bytes(), errors.New(msg)
	}
	return stdout.Bytes(), nil
}

// literal makes paths pathspecs that match only themselves: a file named
// "*.go" is that file. (Per path rather than GIT_LITERAL_PATHSPECS, which
// the repository's hooks would inherit.)
func literal(paths ...string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = ":(literal)" + p
	}
	return out
}

type multi struct{ a, b *bytes.Buffer }

func (m *multi) Write(p []byte) (int, error) { m.a.Write(p); return m.b.Write(p) }

// repoRoot: the top of the git repository cwd is in.
func repoRoot(ctx context.Context, cwd string) (string, error) { return repoRootOn(ctx, host{}, cwd) }

func repoRootOn(ctx context.Context, h host, cwd string) (string, error) {
	if cwd == "" {
		return "", errBadRequest{"the terminal's folder isn't known"}
	}
	top, err := h.git(ctx, cwd, "rev-parse", "--show-toplevel")
	var unreached hcErr
	if errors.As(err, &unreached) {
		return "", err // the machine, not the folder
	}
	if err != nil {
		return "", errBadRequest{"the terminal's folder isn't in a git repository"}
	}
	return strings.TrimSpace(string(top)), nil
}

// changedSet: every changed path (no limit) and its status against the last
// commit, as the Changes view lists them; "?" for new files git doesn't
// ignore.
func changedSet(ctx context.Context, root string) (map[string]string, error) {
	return changedSetOn(ctx, host{}, root)
}

func changedSetOn(ctx context.Context, h host, root string) (map[string]string, error) {
	base := "HEAD"
	if _, err := h.git(ctx, root, "rev-parse", "--verify", "-q", "HEAD"); err != nil {
		base = emptyTree
	}
	status, err := h.git(ctx, root, "diff", "--name-status", "--no-renames", "-z", base)
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	untracked, err := h.git(ctx, root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	set := map[string]string{}
	f := strings.Split(string(status), "\x00")
	for i := 0; i+1 < len(f); i += 2 {
		if f[i] != "" && f[i+1] != "" {
			set[f[i+1]] = f[i][:1]
		}
	}
	for _, p := range strings.Split(string(untracked), "\x00") {
		if _, ok := set[p]; p != "" && !ok {
			set[p] = "?"
		}
	}
	return set, nil
}

// checkPath: a path relative to the repository, nothing that climbs out of it.
func checkPath(p string) error {
	switch {
	case p == "":
		return errBadRequest{"an empty path"}
	case strings.ContainsRune(p, 0):
		return errBadRequest{"a path with a NUL byte"}
	case strings.HasPrefix(p, "/") || filepath.IsAbs(p):
		return errBadRequest{fmt.Sprintf("%q is absolute; paths are relative to the repository", p)}
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return errBadRequest{fmt.Sprintf("%q leaves the repository", p)}
		}
	}
	return nil
}

// pickChanged checks each path and that it is changed now; returns them
// without duplicates, in order, with their statuses.
func pickChanged(set map[string]string, paths []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		if err := checkPath(p); err != nil {
			return nil, err
		}
		if _, ok := set[p]; !ok {
			return nil, errBadRequest{fmt.Sprintf("%s has no changes now", p)}
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out, nil
}

type commitResult struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
	Files   int    `json:"files"`
}

// gitCommit commits exactly paths (each a changed file) with message.
func gitCommit(ctx context.Context, root, message string, paths []string) (commitResult, error) {
	return gitCommitOn(ctx, host{}, root, message, paths)
}

func gitCommitOn(ctx context.Context, h host, root, message string, paths []string) (commitResult, error) {
	message = strings.TrimSpace(message)
	switch {
	case message == "":
		return commitResult{}, errBadRequest{"the commit needs a message"}
	case len(message) > maxCommitMessage:
		return commitResult{}, errBadRequest{"the message is too long"}
	case len(paths) == 0:
		return commitResult{}, errBadRequest{"pick at least one file to commit"}
	case len(paths) > maxCommitPaths:
		return commitResult{}, errBadRequest{"too many files for one commit from the phone"}
	}
	set, err := changedSetOn(ctx, h, root)
	if err != nil {
		return commitResult{}, err
	}
	paths, err = pickChanged(set, paths)
	if err != nil {
		return commitResult{}, err
	}
	var fresh []string
	for _, p := range paths {
		if set[p] == "?" {
			fresh = append(fresh, p)
		}
	}
	if len(fresh) > 0 {
		if _, err := h.gitDo(ctx, root, append([]string{"add", "--intent-to-add", "--"}, literal(fresh...)...)...); err != nil {
			return commitResult{}, err
		}
	}
	if _, err := h.gitDo(ctx, root, append([]string{"commit", "-m", message, "--only", "--"}, literal(paths...)...)...); err != nil {
		if len(fresh) > 0 {
			// New files again, as they were: not in the index.
			undo, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			h.gitDo(undo, root, append([]string{"rm", "--cached", "-q", "-f", "--ignore-unmatch", "--"}, literal(fresh...)...)...)
			cancel()
		}
		return commitResult{}, err
	}
	res := commitResult{Files: len(paths)}
	if out, err := h.git(ctx, root, "log", "-1", "--format=%h%x00%s"); err == nil {
		res.Hash, res.Subject, _ = strings.Cut(strings.TrimSpace(string(out)), "\x00")
	}
	return res, nil
}

// gitDiscard puts one changed file back as the last commit has it, or
// deletes it when it is new.
func gitDiscard(ctx context.Context, root, p string) error { return gitDiscardOn(ctx, host{}, root, p) }

func gitDiscardOn(ctx context.Context, h host, root, p string) error {
	set, err := changedSetOn(ctx, h, root)
	if err != nil {
		return err
	}
	if _, err := pickChanged(set, []string{p}); err != nil {
		return err
	}
	switch set[p] {
	case "?":
		return h.removeNew(ctx, root, p)
	case "A": // staged, not in the last commit: out of the index, then gone
		if err := h.newFileInside(ctx, root, p); err != nil {
			return err
		}
		if _, err := h.gitDo(ctx, root, append([]string{"rm", "--cached", "-q", "-f", "--"}, literal(p)...)...); err != nil {
			return err
		}
		return h.removeNew(ctx, root, p)
	}
	_, err = h.gitDo(ctx, root, append([]string{"restore", "--source=HEAD", "--staged", "--worktree", "--"}, literal(p)...)...)
	if err != nil && strings.Contains(err.Error(), "restore") && strings.Contains(err.Error(), "not a git command") {
		_, err = h.gitDo(ctx, root, append([]string{"checkout", "HEAD", "--"}, literal(p)...)...) // git older than 2.23
	}
	return err
}

// newFileInside: p is a regular file, or a link to one, inside the
// repository, through no link that leads out of it.
func newFileInside(root, p string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	inside := func(x string) bool {
		rel, err := filepath.Rel(realRoot, x)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
	}
	full := filepath.Join(root, filepath.FromSlash(p))
	dir, err := filepath.EvalSymlinks(filepath.Dir(full))
	if err != nil || !inside(dir) {
		return errBadRequest{fmt.Sprintf("%s is outside the repository", p)}
	}
	st, err := os.Lstat(full)
	if err != nil {
		return err
	}
	switch {
	case st.IsDir():
		return errBadRequest{fmt.Sprintf("%s is a folder; only files are discarded", p)}
	case st.Mode()&os.ModeSymlink != 0:
		target, err := filepath.EvalSymlinks(full)
		if err != nil || !inside(target) {
			return errBadRequest{fmt.Sprintf("%s links outside the repository; left alone", p)}
		}
		if t, err := os.Stat(target); err != nil || !t.Mode().IsRegular() {
			return errBadRequest{fmt.Sprintf("%s links to a folder; left alone", p)}
		}
	case !st.Mode().IsRegular():
		return errBadRequest{fmt.Sprintf("%s isn't a regular file; left alone", p)}
	}
	return nil
}

// removeNew deletes a new file (a link: the link itself).
func removeNew(root, p string) error {
	if err := newFileInside(root, p); err != nil {
		return err
	}
	return os.Remove(filepath.Join(root, filepath.FromSlash(p)))
}

// On another machine, newFileInside's checks (and with "rm", removeNew's
// delete) in one script: it prints what it found. readlink -f is in GNU,
// busybox and macOS 12.3 on; without it a link is left alone.
const newFileScript = `root=$(cd -P "$1" 2>/dev/null && pwd -P) || { echo noroot; exit 0; }
full="$1/$2"
inside() { case "$1" in "$root"/*) return 0;; *) return 1;; esac; }
d=$(cd -P "$(dirname "$full")" 2>/dev/null && pwd -P) || { echo outside; exit 0; }
if [ "$d" != "$root" ] && ! inside "$d"; then echo outside; exit 0; fi
if [ -L "$full" ]; then
  t=$(readlink -f "$full" 2>/dev/null) || t=
  if [ -z "$t" ] || ! inside "$t"; then echo linkout; exit 0; fi
  [ -f "$t" ] || { echo linkdir; exit 0; }
elif [ -d "$full" ]; then echo dir; exit 0
elif [ ! -e "$full" ]; then echo missing; exit 0
elif [ ! -f "$full" ]; then echo special; exit 0
fi
if [ "$3" = rm ]; then rm -f -- "$full" || exit 1; fi
echo ok`

// newFile checks (and with rm, deletes) a new file on the host.
func (h host) newFile(ctx context.Context, root, p string, rm bool) error {
	if !h.remote() {
		if rm {
			return removeNew(root, p)
		}
		return newFileInside(root, p)
	}
	mode := "check"
	if rm {
		mode = "rm"
	}
	out, err := h.run(ctx, "", "sh", "-c", newFileScript, "sh", root, p, mode)
	if err != nil {
		return err
	}
	switch strings.TrimSpace(string(out)) {
	case "ok":
		return nil
	case "noroot":
		return fmt.Errorf("%s: the repository's folder is gone", h.machine)
	case "outside":
		return errBadRequest{fmt.Sprintf("%s is outside the repository", p)}
	case "linkout":
		return errBadRequest{fmt.Sprintf("%s links outside the repository; left alone", p)}
	case "linkdir":
		return errBadRequest{fmt.Sprintf("%s links to a folder; left alone", p)}
	case "dir":
		return errBadRequest{fmt.Sprintf("%s is a folder; only files are discarded", p)}
	case "missing":
		return fmt.Errorf("%s: no such file", p)
	case "special":
		return errBadRequest{fmt.Sprintf("%s isn't a regular file; left alone", p)}
	}
	return fmt.Errorf("%s: couldn't check %s: %s", h.machine, p, firstLines(string(out), 2, 200))
}

func (h host) newFileInside(ctx context.Context, root, p string) error {
	return h.newFile(ctx, root, p, false)
}

func (h host) removeNew(ctx context.Context, root, p string) error {
	return h.newFile(ctx, root, p, true)
}

// suggestMessage writes a Conventional Commits message from the paths and
// statuses alone: a type from what kind of files they are, the top folder
// as the scope, and a bullet per file. Same files, same message.
func suggestMessage(set map[string]string) string {
	paths := make([]string, 0, len(set))
	for p := range set {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return ""
	}

	kinds := map[string]int{}
	newCode, added, deleted := false, 0, 0
	for _, p := range paths {
		k := fileKind(p)
		kinds[k]++
		switch set[p] {
		case "?", "A":
			added++
			if k == "code" {
				newCode = true
			}
		case "D":
			deleted++
		}
	}
	typ := "chore"
	switch {
	case len(kinds) == 1 && kinds["docs"] > 0:
		typ = "docs"
	case len(kinds) == 1 && kinds["test"] > 0:
		typ = "test"
	case len(kinds) == 1 && kinds["ci"] > 0:
		typ = "ci"
	case kinds["code"] == 0 && kinds["build"] > 0 && kinds["docs"] == 0 && kinds["test"] == 0:
		typ = "build"
	case newCode:
		typ = "feat"
	case deleted == len(paths):
		typ = "refactor"
	}

	scope := ""
	for i, p := range paths {
		top, _, nested := strings.Cut(p, "/")
		if !nested {
			top = ""
		}
		if i == 0 {
			scope = top
		} else if top != scope {
			scope = ""
			break
		}
	}
	if strings.HasPrefix(scope, ".") {
		scope = ""
	}

	verb := "update"
	switch {
	case added == len(paths):
		verb = "add"
	case deleted == len(paths):
		verb = "remove"
	}
	head := typ
	if scope != "" {
		head += "(" + scope + ")"
	}
	head += ": " + verb + " "
	what := fmt.Sprintf("%d files", len(paths))
	if len(paths) <= 3 {
		names := make([]string, len(paths))
		seen := map[string]bool{}
		dup := false
		for i, p := range paths {
			names[i] = path.Base(p)
			dup = dup || seen[names[i]]
			seen[names[i]] = true
		}
		if dup {
			copy(names, paths)
		}
		list := names[0]
		if len(names) > 1 {
			list = strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
		}
		if len(head)+len(list) <= 72 {
			what = list
		}
	}
	subject := head + what
	if len(paths) == 1 {
		return subject
	}
	var b strings.Builder
	b.WriteString(subject + "\n")
	for i, p := range paths {
		if i == 15 {
			fmt.Fprintf(&b, "\n- …and %d more", len(paths)-i)
			break
		}
		v := "Update"
		switch set[p] {
		case "?", "A":
			v = "Add"
		case "D":
			v = "Remove"
		}
		b.WriteString("\n- " + v + " " + p)
	}
	return b.String()
}

// fileKind: docs, test, ci, build or code, by name.
func fileKind(p string) string {
	lower := strings.ToLower(p)
	base := path.Base(lower)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(path.Base(p), path.Ext(p)) // as written: FooTest, not latest
	switch {
	case strings.HasPrefix(lower, ".github/") || strings.HasPrefix(base, ".gitlab-ci") || strings.HasPrefix(lower, ".circleci/"):
		return "ci"
	case strings.HasSuffix(base, "_test.go") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") ||
		strings.HasPrefix(base, "test_") || (strings.HasSuffix(stem, "Test") || strings.HasSuffix(stem, "Tests")) && (ext == ".kt" || ext == ".java" || ext == ".swift") ||
		strings.Contains("/"+lower, "/test/") || strings.Contains("/"+lower, "/tests/") || strings.Contains("/"+lower, "/__tests__/"):
		return "test"
	case ext == ".md" || ext == ".mdx" || ext == ".rst" || ext == ".adoc" || strings.HasPrefix(lower, "docs/") || strings.Contains(lower, "/docs/") ||
		strings.HasPrefix(base, "readme") || strings.HasPrefix(base, "changelog") || strings.HasPrefix(base, "license"):
		return "docs"
	}
	switch base {
	case "go.mod", "go.sum", "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lockb", "cargo.toml", "cargo.lock",
		"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", "gradle.properties", "makefile", "dockerfile",
		"requirements.txt", "pyproject.toml", "gemfile", "gemfile.lock", "libs.versions.toml":
		return "build"
	}
	return "code"
}

// POST /api/commit {terminal, message, paths}: commits those files in the
// terminal's repository. {hash, subject, files}, or the error git printed.
func (s *server) commit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Terminal string   `json:"terminal"`
		Message  string   `json:"message"`
		Paths    []string `json:"paths"`
	}
	if err := decode(r, &req); err != nil || !validTerminal(req.Terminal) {
		writeErr(w, http.StatusBadRequest, errors.New("need {terminal, message, paths}"))
		return
	}
	h, root, ok := s.repoOf(w, r, req.Terminal)
	if !ok {
		return
	}
	// Hooks (tests, linters) can take a while.
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	res, err := gitCommitOn(ctx, h, root, req.Message, req.Paths)
	if err != nil {
		writeGitErr(w, err)
		return
	}
	writeJSON(w, res)
}

// POST /api/discard {terminal, path}: that file as the last commit has it
// (a new file: deleted).
func (s *server) discard(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Terminal string `json:"terminal"`
		Path     string `json:"path"`
	}
	if err := decode(r, &req); err != nil || !validTerminal(req.Terminal) {
		writeErr(w, http.StatusBadRequest, errors.New("need {terminal, path}"))
		return
	}
	h, root, ok := s.repoOf(w, r, req.Terminal)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := gitDiscardOn(ctx, h, root, req.Path); err != nil {
		writeGitErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "path": req.Path})
}

// GET /api/commit/suggest?terminal=…[&path=…]…: a message for those changed
// files (all of them without path), written from their names and statuses.
func (s *server) commitSuggest(w http.ResponseWriter, r *http.Request) {
	t := r.URL.Query().Get("terminal")
	if !validTerminal(t) {
		writeErr(w, http.StatusBadRequest, errors.New("need terminal"))
		return
	}
	h, root, ok := s.repoOf(w, r, t)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	set, err := changedSetOn(ctx, h, root)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if paths := r.URL.Query()["path"]; len(paths) > 0 {
		picked, err := pickChanged(set, paths)
		if err != nil {
			writeGitErr(w, err)
			return
		}
		some := map[string]string{}
		for _, p := range picked {
			some[p] = set[p]
		}
		set = some
	}
	writeJSON(w, map[string]string{"message": suggestMessage(set)})
}

// repoOf: the machine a terminal runs on and the root of the repository
// it works in; false when it answered the phone with an error.
func (s *server) repoOf(w http.ResponseWriter, r *http.Request, terminal string) (host, string, bool) {
	h := s.hostOf(terminal)
	fg, err := s.foreground(r, terminal)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return h, "", false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	root, err := repoRootOn(ctx, h, fg.Cwd)
	if err != nil {
		writeGitErr(w, err)
		return h, "", false
	}
	return h, root, true
}

func writeGitErr(w http.ResponseWriter, err error) {
	var bad errBadRequest
	if errors.As(err, &bad) {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var hcFailed hcErr
	if errors.As(err, &hcFailed) {
		writeErr(w, http.StatusBadGateway, err) // the machine couldn't be reached
		return
	}
	writeErr(w, http.StatusUnprocessableEntity, err)
}
