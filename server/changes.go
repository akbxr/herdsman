package main

// What the agent changed: the git status and diff of the folder it works in,
// against the last commit, for the phone's Changes view. Uncommitted work
// only, staged or not, and new files git doesn't ignore.
//
// Bounded for a phone: up to 80 files with their patches, each cut at 1500
// lines, new files shown when they are small text.

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type changedFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"` // M modified, A added, D deleted, ? new (untracked), T type
	Added     int    `json:"added"`
	Deleted   int    `json:"deleted"`
	Binary    bool   `json:"binary,omitempty"`
	Patch     string `json:"patch,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type changesResult struct {
	Cwd     string        `json:"cwd"`
	Repo    bool          `json:"repo"`
	Root    string        `json:"root,omitempty"`
	Branch  string        `json:"branch,omitempty"`
	Files   []changedFile `json:"files"`
	More    int           `json:"more,omitempty"` // files left out past the limit
	Version string        `json:"version"`
}

const (
	maxChangedFiles = 80
	maxPatchLines   = 1500
	maxNewFileBytes = 64 << 10
)

// git's empty tree: what a repository without a commit is compared with.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

func (s *server) changes(w http.ResponseWriter, r *http.Request) {
	t := r.URL.Query().Get("terminal")
	if !validTerminal(t) {
		writeErr(w, http.StatusBadRequest, errors.New("need terminal"))
		return
	}
	fg, err := s.foreground(r, t)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	res := gitChanges(ctx, fg.Cwd)
	if v := r.URL.Query().Get("version"); v != "" && v == res.Version {
		writeJSON(w, map[string]any{"same": true, "version": v})
		return
	}
	writeJSON(w, res)
}

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
}

func gitChanges(ctx context.Context, cwd string) changesResult {
	res := changesResult{Cwd: cwd, Files: []changedFile{}}
	if cwd == "" {
		return res
	}
	top, err := git(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return res
	}
	root := strings.TrimSpace(string(top))
	base := "HEAD"
	if _, err := git(ctx, root, "rev-parse", "--verify", "-q", "HEAD"); err != nil {
		// No commit yet: a repository only if it tracks something (a home
		// folder kept under git for nothing would list every file in it).
		if tracked, _ := git(ctx, root, "ls-files", "--cached"); len(bytes.TrimSpace(tracked)) == 0 {
			return res
		}
		base = emptyTree
	}
	res.Repo, res.Root = true, root
	if b, err := git(ctx, root, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		res.Branch = strings.TrimSpace(string(b))
	}

	status, _ := git(ctx, root, "diff", "--name-status", "--no-renames", "-z", base)
	numstat, _ := git(ctx, root, "diff", "--numstat", "--no-renames", "-z", base)
	quick, cancel := context.WithTimeout(ctx, 3*time.Second)
	untracked, _ := git(quick, root, "ls-files", "--others", "--exclude-standard", "-z")
	cancel()

	sum := sha1.New()
	sum.Write(status)
	sum.Write(numstat)
	sum.Write(untracked)

	byPath := map[string]*changedFile{}
	var order []string
	f := strings.Split(string(status), "\x00")
	for i := 0; i+1 < len(f); i += 2 {
		st, p := f[i], f[i+1]
		if st == "" || p == "" {
			continue
		}
		byPath[p] = &changedFile{Path: p, Status: st[:1]}
		order = append(order, p)
	}
	for _, rec := range strings.Split(string(numstat), "\x00") {
		parts := strings.SplitN(rec, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		c := byPath[parts[2]]
		if c == nil {
			continue
		}
		if parts[0] == "-" {
			c.Binary = true
			continue
		}
		c.Added, _ = strconv.Atoi(parts[0])
		c.Deleted, _ = strconv.Atoi(parts[1])
	}
	for _, p := range strings.Split(string(untracked), "\x00") {
		if p != "" && byPath[p] == nil {
			byPath[p] = &changedFile{Path: p, Status: "?"}
			order = append(order, p)
		}
	}

	if len(order) > maxChangedFiles {
		res.More = len(order) - maxChangedFiles
		order = order[:maxChangedFiles]
	}
	for _, p := range order {
		c := byPath[p]
		switch {
		case c.Status == "?":
			newFilePatch(filepath.Join(root, p), c, sum)
		case !c.Binary:
			out, _ := git(ctx, root, "diff", "--no-color", "--no-renames", "--no-ext-diff", "-U3", base, "--", p)
			sum.Write(out)
			c.Patch, c.Truncated = hunks(string(out))
		}
		res.Files = append(res.Files, *c)
	}
	res.Version = hex.EncodeToString(sum.Sum(nil))[:16]
	return res
}

// hunks keeps a patch from its first hunk on (the headers say nothing a
// phone needs), cut at maxPatchLines.
func hunks(patch string) (string, bool) {
	if i := strings.Index(patch, "\n@@"); i >= 0 {
		patch = patch[i+1:]
	} else if strings.HasPrefix(patch, "diff --git") {
		return "", false // a mode change or an empty file: no lines
	}
	lines := strings.Split(strings.TrimRight(patch, "\n"), "\n")
	if len(lines) > maxPatchLines {
		return strings.Join(lines[:maxPatchLines], "\n"), true
	}
	return strings.Join(lines, "\n"), false
}

// A new file reads as all added, when it is small and text.
func newFilePatch(path string, c *changedFile, sum interface{ Write([]byte) (int, error) }) {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return
	}
	if st.Size() > maxNewFileBytes {
		c.Truncated = true
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	sum.Write(b) // a new file changing changes the version too
	if bytes.IndexByte(b, 0) >= 0 {
		c.Binary = true
		return
	}
	text := strings.TrimRight(string(b), "\n")
	if text == "" {
		return
	}
	lines := strings.Split(text, "\n")
	c.Added = len(lines)
	if len(lines) > maxPatchLines {
		lines, c.Truncated = lines[:maxPatchLines], true
	}
	c.Patch = "@@ -0,0 +1," + strconv.Itoa(c.Added) + " @@\n+" + strings.Join(lines, "\n+")
}
