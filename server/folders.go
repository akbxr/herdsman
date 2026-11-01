package main

// The folders a new task can start in (the phone's folder picker): where
// you worked lately, then the git repositories in the usual places.
//
// Recent comes from
//   live terminals  hc ls (their cwd), newest of all
//   Claude Code     ~/.claude/sessions/*.json (running sessions), ~/.claude/projects/*/
//                   (the newest transcript of each folder names its cwd), ~/.claude/history.jsonl
//   tego            ~/.tego/sessions/<folder>/*.jsonl (the header line names its cwd)
//   Codex           ~/.codex/sessions/YYYY/MM/DD/*.jsonl
// newest first, each folder once, only folders that still exist, none in /tmp.
//
// Projects: directories holding .git (a repository or a worktree) under
// ~/Code, ~/Projects, ~/src, ~/Developer, ~/dev and the like, at most three
// levels down, never inside a repository, and given up after a fixed time.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Folder is one place to start a task in.
type Folder struct {
	Path string `json:"path"`
	Name string `json:"name"` // its last part; "~" for home
	Time int64  `json:"time"` // unix ms: last worked in (recent), last git activity (projects)
}

const (
	recentMax    = 15
	projectsMax  = 400
	projectDepth = 3
	projectScan  = 800 * time.Millisecond // the walk stops here, with what it found
)

var (
	// Where people keep their code. Missing ones are skipped.
	projectRoots = []string{"Code", "Code/projects", "Projects", "projects", "src", "Developer", "dev", "code",
		"repos", "git", "workspace", "work", "Documents/GitHub", "Documents/Projects", "Documents/Code"}
	// Not worth descending into.
	skipDirs = map[string]bool{"node_modules": true, "vendor": true, "Library": true, "target": true, "build": true, "dist": true}
	cwdJSON  = regexp.MustCompile(`"cwd"\s*:\s*"((?:[^"\\]|\\.)*)"`)
	cwdTag   = regexp.MustCompile(`<cwd>([^<]+)</cwd>`)
)

func (s *server) folders(w http.ResponseWriter, r *http.Request) {
	home, _ := os.UserHomeDir()
	if s.home != "" {
		home = s.home
	}
	recent := recentFolders(r.Context(), home, s.hc)
	seen := map[fileID]bool{}
	for _, f := range recent {
		if id, ok := idOf(f.Path); ok {
			seen[id] = true
		}
	}
	var projects []Folder
	for _, f := range projectFolders(home, time.Now().Add(projectScan)) {
		if id, ok := idOf(f.Path); !ok || !seen[id] {
			projects = append(projects, f)
		}
	}
	if projects == nil {
		projects = []Folder{}
	}
	writeJSON(w, map[string]any{"home": home, "recent": recent, "projects": projects})
}

// recentFolders: where terminals and agents ran lately, newest first.
func recentFolders(ctx context.Context, home, hc string) []Folder {
	when := map[string]time.Time{}
	note := func(path string, t time.Time) {
		if path == "" || !filepath.IsAbs(path) {
			return
		}
		path = filepath.Clean(path)
		if t.After(when[path]) {
			when[path] = t
		}
	}

	// Live terminals: where you are now.
	if out, err := run(ctx, 3*time.Second, hc, "ls", "--json"); err == nil {
		var ls struct{ Terminals []struct{ Cwd, State string } }
		if json.Unmarshal(out, &ls) == nil {
			now := time.Now()
			for _, t := range ls.Terminals {
				if t.State == "" || t.State == "live" {
					note(t.Cwd, now)
				}
			}
		}
	}

	// Claude Code: running sessions, then each folder's newest transcript, then what you typed.
	claude := filepath.Join(home, ".claude")
	if files, _ := filepath.Glob(filepath.Join(claude, "sessions", "*.json")); files != nil {
		for _, f := range files {
			b, err := os.ReadFile(f)
			st, serr := os.Stat(f)
			var sess struct{ Cwd string }
			if err == nil && serr == nil && json.Unmarshal(b, &sess) == nil {
				note(sess.Cwd, st.ModTime())
			}
		}
	}
	for _, dir := range subdirs(filepath.Join(claude, "projects")) {
		if f, t := newestFile(dir, ".jsonl"); f != "" {
			note(cwdOf(f), t)
		}
	}
	for _, l := range tailFileLines(filepath.Join(claude, "history.jsonl"), 128<<10) {
		var h struct {
			Project   string
			Timestamp int64
		}
		if json.Unmarshal(l, &h) == nil && h.Timestamp > 0 {
			note(h.Project, time.UnixMilli(h.Timestamp))
		}
	}

	// tego: a folder per cwd, a file per session.
	for _, dir := range subdirs(filepath.Join(home, ".tego", "sessions")) {
		if f, t := newestFile(dir, ".jsonl"); f != "" {
			note(cwdOf(f), t)
		}
	}

	// Codex: by date; the last days are enough.
	for _, f := range codexRecent(filepath.Join(home, ".codex", "sessions"), 20) {
		if st, err := os.Stat(f); err == nil {
			note(cwdOf(f), st.ModTime())
		}
	}

	list := make([]Folder, 0, len(when))
	for p, t := range when {
		if temporary(p) || !isDir(p) {
			continue
		}
		list = append(list, Folder{Path: p, Name: folderName(p, home), Time: t.UnixMilli()})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Time != list[j].Time {
			return list[i].Time > list[j].Time
		}
		return list[i].Path < list[j].Path
	})
	// One folder by two spellings (~/code and ~/Code on a case-insensitive
	// disk, a symlink): the newest keeps its place.
	seen := map[fileID]bool{}
	kept := list[:0]
	for _, f := range list {
		id, ok := idOf(f.Path)
		if ok && seen[id] {
			continue
		}
		seen[id] = true
		kept = append(kept, f)
	}
	list = kept
	if len(list) > recentMax {
		list = list[:recentMax]
	}
	return list
}

// projectFolders: git repositories under the usual roots, most recently
// used first. It stops at the deadline with what it has.
func projectFolders(home string, deadline time.Time) []Folder {
	seen := map[fileID]bool{} // ~/code is ~/Code on most Macs; ~/Code/projects is in ~/Code
	var list []Folder
	type entry struct {
		path  string
		depth int
	}
	for _, root := range projectRoots {
		queue := []entry{{filepath.Join(home, root), 0}}
		for len(queue) > 0 && len(list) < projectsMax {
			if time.Now().After(deadline) {
				goto done
			}
			e := queue[0]
			queue = queue[1:]
			ents, err := os.ReadDir(e.path)
			if err != nil {
				continue
			}
			for _, d := range ents {
				name := d.Name()
				if strings.HasPrefix(name, ".") || skipDirs[name] {
					continue
				}
				p := filepath.Join(e.path, name)
				if !d.IsDir() {
					if d.Type()&os.ModeSymlink == 0 || !isDir(p) {
						continue
					}
				}
				id, ok := idOf(p)
				if !ok || seen[id] {
					continue
				}
				seen[id] = true
				if t, ok := gitActivity(p); ok {
					list = append(list, Folder{Path: p, Name: name, Time: t.UnixMilli()})
					continue // its own folders aren't projects of their own
				}
				if e.depth+1 < projectDepth {
					queue = append(queue, entry{p, e.depth + 1})
				}
			}
		}
	}
done:
	sort.Slice(list, func(i, j int) bool {
		if list[i].Time != list[j].Time {
			return list[i].Time > list[j].Time
		}
		return list[i].Path < list[j].Path
	})
	return list
}

// gitActivity: whether dir is a repository (or a worktree: .git is a file),
// and when git last touched it.
func gitActivity(dir string) (time.Time, bool) {
	st, err := os.Stat(filepath.Join(dir, ".git"))
	if err != nil {
		return time.Time{}, false
	}
	t := st.ModTime()
	if st.IsDir() {
		for _, f := range []string{"index", "HEAD", "FETCH_HEAD"} {
			if s, err := os.Stat(filepath.Join(dir, ".git", f)); err == nil && s.ModTime().After(t) {
				t = s.ModTime()
			}
		}
	}
	return t, true
}

// fileID: a folder's identity on disk, whatever the spelling of its path.
type fileID struct{ dev, ino uint64 }

func idOf(p string) (fileID, bool) {
	st, err := os.Stat(p)
	if err != nil {
		return fileID{}, false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return fileID{}, false
	}
	return fileID{uint64(sys.Dev), uint64(sys.Ino)}, true
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// tempRoots: scratch space, not worth offering again (a variable for the tests).
var tempRoots = []string{"/tmp", "/private/tmp", "/var/folders", "/private/var/folders", filepath.Clean(os.TempDir())}

func temporary(p string) bool {
	for _, t := range tempRoots {
		if t != "/" && (p == t || strings.HasPrefix(p, t+"/")) {
			return true
		}
	}
	return false
}

func folderName(p, home string) string {
	if p == home {
		return "~"
	}
	return filepath.Base(p)
}

func subdirs(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// newestFile: the most recently written file in dir with that suffix.
func newestFile(dir, suffix string) (string, time.Time) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", time.Time{}
	}
	var best string
	var bt time.Time
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), suffix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(bt) {
			best, bt = filepath.Join(dir, e.Name()), info.ModTime()
		}
	}
	return best, bt
}

// cwdOf: the first cwd a transcript names in its first 64 KB ("cwd": "…"
// in a JSON line, or <cwd>…</cwd> in Codex's context message).
func cwdOf(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	head, _ := io.ReadAll(io.LimitReader(f, 64<<10))
	if m := cwdJSON.FindSubmatch(head); m != nil {
		var s string
		if json.Unmarshal(append(append([]byte{'"'}, m[1]...), '"'), &s) == nil {
			return s
		}
	}
	if m := cwdTag.FindSubmatch(head); m != nil {
		return strings.TrimSpace(string(m[1]))
	}
	return ""
}

// tailFileLines: the complete lines in the last n bytes of a file.
func tailFileLines(path string, n int64) [][]byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	start := st.Size() - n
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	var lines [][]byte
	first := start > 0
	for sc.Scan() {
		if first { // cut in the middle
			first = false
			continue
		}
		if l := bytes.TrimSpace(sc.Bytes()); len(l) > 0 {
			lines = append(lines, append([]byte(nil), l...))
		}
	}
	return lines
}

// codexRecent: up to n of Codex's newest session files (YYYY/MM/DD folders).
func codexRecent(dir string, n int) []string {
	var out []string
	desc := func(d string) []string {
		s := subdirs(d)
		sort.Sort(sort.Reverse(sort.StringSlice(s)))
		return s
	}
	for _, y := range desc(dir) {
		for _, m := range desc(y) {
			for _, d := range desc(m) {
				files, _ := filepath.Glob(filepath.Join(d, "*.jsonl"))
				sort.Sort(sort.Reverse(sort.StringSlice(files)))
				for _, f := range files {
					out = append(out, f)
					if len(out) >= n {
						return out
					}
				}
			}
		}
	}
	return out
}
