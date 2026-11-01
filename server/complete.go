package main

// What the phone's message box offers as you type: "/" lists the agent's
// commands (its own, your custom commands and skills), "@" the files of the
// folder the agent works in.

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type command struct {
	Name string `json:"name"` // with its slash: "/compact", "/skill:frontend-design"
	Help string `json:"help,omitempty"`
}

// tego's own commands (cmd/tego/repl.go builtinCommands).
var tegoCommands = []command{
	{"/help", "Show commands"},
	{"/model", "Pick a model, or /model <query|provider/id>"},
	{"/thinking", "Pick or set thinking: off|low|medium|high|xhigh|max"},
	{"/fast", "Fast mode on or off"},
	{"/resume", "Resume a different session"},
	{"/branch", "Rewind to a previous turn, keeping the old path as a branch"},
	{"/undo", "Put the files back as they were before a prompt"},
	{"/plan", "Plan mode: investigate and propose, change nothing until you approve"},
	{"/permissions", "Which tool calls wait for your approval: [yolo|edits|ask]"},
	{"/tree", "Navigate the session tree"},
	{"/extensions", "Extensions: what runs, with its tools and commands"},
	{"/agents", "Show what a subagent of the latest task did"},
	{"/fork", "New session from before a chosen prompt"},
	{"/clone", "Duplicate the current session into a new one"},
	{"/compact", "Summarise history to free context: /compact [focus]"},
	{"/new", "Start a fresh session (alias /clear)"},
	{"/tasks", "Background tasks: see what they did, or stop them"},
	{"/session", "Session info and token usage"},
	{"/retry", "Re-run the model on the current history"},
	{"/skills", "List discovered skills"},
	{"/usage", "Subscription usage and resets"},
	{"/stats", "Requests, tokens and estimated cost"},
	{"/mcp", "MCP server status and tool counts"},
	{"/exit", "Quit"},
}

var claudeCommands = []command{
	{"/clear", "Clear the conversation"},
	{"/compact", "Summarise the conversation: /compact [instructions]"},
	{"/context", "Show what fills the context"},
	{"/cost", "Token usage and cost of this session"},
	{"/model", "Pick the model"},
	{"/resume", "Resume a conversation"},
	{"/review", "Review a pull request"},
	{"/init", "Write a CLAUDE.md for this project"},
	{"/memory", "Edit memory files"},
	{"/agents", "Manage subagents"},
	{"/mcp", "MCP servers"},
	{"/permissions", "Allowed and denied tools"},
	{"/config", "Settings"},
	{"/status", "Version, model, account"},
	{"/usage", "Plan usage limits"},
	{"/help", "Help"},
	{"/exit", "Quit"},
}

var codexCommands = []command{
	{"/model", "Pick the model and reasoning effort"},
	{"/approvals", "What Codex may do without asking"},
	{"/new", "Start a new chat"},
	{"/compact", "Summarise the conversation"},
	{"/diff", "Show the git diff"},
	{"/mention", "Mention a file"},
	{"/status", "Session configuration and token usage"},
	{"/init", "Write an AGENTS.md"},
	{"/quit", "Quit"},
}

func (s *server) commands(w http.ResponseWriter, r *http.Request) {
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
	agent := agentOf(fg)
	h := s.hostOf(t)
	if !h.remote() {
		writeJSON(w, map[string]any{"agent": agent, "commands": commandsFor(agent, fg.Cwd)})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	list, err := commandsOn(ctx, h, agent, fg.Cwd)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, map[string]any{"agent": agent, "commands": list})
}

// mdFile is a command's or skill's Markdown file and its first lines.
type mdFile struct{ path, text string }

func commandsFor(agent, cwd string) []command {
	home, _ := os.UserHomeDir()
	return commandList(agent, cwd, home, func(dir string) []mdFile {
		var out []mdFile
		files, _ := filepath.Glob(filepath.Join(dir, "commands", "*.md"))
		skills, _ := filepath.Glob(filepath.Join(dir, "skills", "*", "SKILL.md"))
		for _, f := range append(files, skills...) {
			out = append(out, mdFile{f, mdHead(f)})
		}
		return out
	})
}

// On another machine: each folder's commands and skills, with their first
// lines, in one script (a NUL, the path, a NUL, the lines).
const commandsScript = `for d; do
  for f in "$d"/commands/*.md "$d"/skills/*/SKILL.md; do
    [ -f "$f" ] || continue
    printf '\0%s\0' "$f"; head -n 60 "$f"
  done
done`

// commandsOn: commandsFor on the terminal's machine, in one round trip
// (with its home, asked once).
func commandsOn(ctx context.Context, h host, agent, cwd string) ([]command, error) {
	if !h.remote() {
		return commandsFor(agent, cwd), nil
	}
	if agent != "tego" && agent != "claude" {
		return commandList(agent, cwd, "", nil), nil
	}
	home, err := h.homeDir(ctx)
	if err != nil {
		return nil, err
	}
	dirs := commandDirs(agent, cwd, home)
	b, err := h.run(ctx, "", append([]string{"sh", "-c", commandsScript, "sh"}, dirs...)...)
	if err != nil {
		return nil, err
	}
	byDir := map[string][]mdFile{}
	parts := strings.Split(string(b), "\x00")
	for i := 1; i+1 < len(parts); i += 2 {
		f := mdFile{parts[i], parts[i+1]}
		for _, d := range dirs {
			if strings.HasPrefix(f.path, d+"/") {
				byDir[d] = append(byDir[d], f)
				break
			}
		}
	}
	return commandList(agent, cwd, home, func(dir string) []mdFile { return byDir[dir] }), nil
}

// commandDirs: where custom commands and skills are looked for: the user's,
// then each folder from cwd up to home, each in the agent's config folders.
func commandDirs(agent, cwd, home string) []string {
	dirs := []string{home}
	for d := cwd; d != "" && d != "/" && d != home && strings.HasPrefix(d, home); d = filepath.Dir(d) {
		dirs = append(dirs, d)
	}
	kinds := []string{".claude"}
	if agent == "tego" {
		kinds = []string{".agents", ".claude", ".tego"}
	}
	var out []string
	for _, d := range dirs {
		for _, k := range kinds {
			out = append(out, filepath.Join(d, k))
		}
	}
	return out
}

// commandList: the agent's own commands, then the custom commands and skills
// files gives for each of its folders.
func commandList(agent, cwd, home string, files func(dir string) []mdFile) []command {
	var out []command
	switch agent {
	case "tego":
		out = append(out, tegoCommands...)
	case "claude":
		out = append(out, claudeCommands...)
	case "codex":
		return codexCommands
	default:
		return []command{}
	}
	seen := map[string]bool{}
	for _, c := range out {
		seen[c.Name] = true
	}
	add := func(c command) {
		if !seen[c.Name] {
			seen[c.Name] = true
			out = append(out, c)
		}
	}
	for _, d := range commandDirs(agent, cwd, home) {
		for _, f := range files(d) {
			if filepath.Base(f.path) != "SKILL.md" {
				name := strings.TrimSuffix(filepath.Base(f.path), ".md")
				add(command{"/" + name, mdDescriptionIn(f.text)})
				continue
			}
			name := filepath.Base(filepath.Dir(f.path))
			if n := frontmatterIn(f.text, "name"); n != "" {
				name = n
			}
			if agent == "tego" {
				add(command{"/skill:" + name, mdDescriptionIn(f.text)})
			} else {
				add(command{"/" + name, mdDescriptionIn(f.text)})
			}
		}
	}
	return out
}

// mdHead: the start of a Markdown file, enough for its front matter and
// first line of text.
func mdHead(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, 64<<10))
	return string(b)
}

// frontmatter reads one key of a Markdown file's YAML front matter.
func frontmatter(path, key string) string { return frontmatterIn(mdHead(path), key) }

func frontmatterIn(text, key string) string {
	sc := bufio.NewScanner(strings.NewReader(text))
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return ""
	}
	for i := 0; sc.Scan() && i < 40; i++ {
		l := sc.Text()
		if strings.TrimSpace(l) == "---" {
			break
		}
		if v, ok := strings.CutPrefix(l, key+":"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// mdDescription: the front matter's description, else the first line of text.
func mdDescription(path string) string { return mdDescriptionIn(mdHead(path)) }

func mdDescriptionIn(text string) string {
	if d := frontmatterIn(text, "description"); d != "" {
		return firstLines(d, 1, 120)
	}
	sc := bufio.NewScanner(strings.NewReader(text))
	inFront := false
	for i := 0; sc.Scan() && i < 60; i++ {
		l := strings.TrimSpace(sc.Text())
		if i == 0 && l == "---" {
			inFront = true
			continue
		}
		if inFront {
			if l == "---" {
				inFront = false
			}
			continue
		}
		if l = strings.TrimLeft(l, "# "); l != "" {
			return firstLines(l, 1, 120)
		}
	}
	return ""
}

// Files of a folder, kept for a while: listing a big repository takes time.
var fileLists = struct {
	sync.Mutex
	m map[string]fileList
}{m: map[string]fileList{}}

type fileList struct {
	at    time.Time
	files []string
}

const maxListed = 20000

func listFiles(ctx context.Context, dir string) []string {
	fileLists.Lock()
	l, ok := fileLists.m[dir]
	fileLists.Unlock()
	if ok && time.Since(l.at) < 30*time.Second {
		return l.files
	}
	var files []string
	// A git repository: what git tracks, and what it doesn't ignore if git
	// lists that quickly. (A repository tracking nothing, like a home folder
	// kept under git for a few dotfiles, is walked instead.)
	lines := func(out []byte) {
		for _, f := range strings.Split(string(out), "\n") {
			if f != "" && len(files) < maxListed {
				files = append(files, f)
			}
		}
	}
	tracked, err := exec.CommandContext(ctx, "git", "-C", dir, "ls-files", "--cached").Output()
	if err == nil && len(tracked) > 0 {
		lines(tracked)
		quick, cancel := context.WithTimeout(ctx, 2*time.Second)
		if others, err := exec.CommandContext(quick, "git", "-C", dir, "ls-files", "--others", "--exclude-standard").Output(); err == nil {
			lines(others)
		}
		cancel()
	} else {
		// Elsewhere (a home folder): a shallow walk without hidden folders and
		// dependencies, for at most a second and a half.
		deadline := time.Now().Add(1500 * time.Millisecond)
		filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if len(files) >= 5000 || time.Now().After(deadline) || ctx.Err() != nil {
				return filepath.SkipAll
			}
			if err != nil {
				return nil // unreadable: left out
			}
			rel, _ := filepath.Rel(dir, p)
			if d.IsDir() {
				if p != dir && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" || d.Name() == "Library" || d.Name() == "vendor" || d.Name() == "build" || strings.Count(rel, "/") >= 3) {
					return filepath.SkipDir
				}
				return nil
			}
			files = append(files, rel)
			return nil
		})
	}
	fileLists.Lock()
	fileLists.m[dir] = fileList{time.Now(), files}
	fileLists.Unlock()
	return files
}

// listFilesOn: listFiles on the terminal's machine: git's lists in one round
// trip; outside a repository, a shallow find for about two seconds.
func listFilesOn(ctx context.Context, h host, dir string) ([]string, error) {
	if !h.remote() {
		return listFiles(ctx, dir), nil
	}
	key := h.machine + "\x00" + dir
	fileLists.Lock()
	l, ok := fileLists.m[key]
	fileLists.Unlock()
	if ok && time.Since(l.at) < 30*time.Second {
		return l.files, nil
	}
	var files []string
	lines := func(out []byte, max int) {
		for _, f := range strings.Split(string(out), "\n") {
			if f = strings.TrimPrefix(f, "./"); f != "" && f != "." && len(files) < max {
				files = append(files, f)
			}
		}
	}
	out, err := h.batch(ctx, "", [][]string{
		{"git", "-C", dir, "ls-files", "--cached"},
		{"git", "-C", dir, "ls-files", "--others", "--exclude-standard"},
	})
	if err != nil {
		return nil, err
	}
	if out[0].ok && len(out[0].out) > 0 {
		lines(out[0].out, maxListed)
		if out[1].ok {
			lines(out[1].out, maxListed)
		}
	} else {
		quick, cancel := context.WithTimeout(ctx, 3500*time.Millisecond) // two seconds there
		r, err := h.exec(quick, dir, nil, "find", ".", "-maxdepth", "4",
			"(", "-type", "d", "(", "-name", ".?*", "-o", "-name", "node_modules", "-o", "-name", "Library",
			"-o", "-name", "vendor", "-o", "-name", "build", ")", "-prune", ")", "-o", "-type", "f", "-print")
		cancel()
		if err != nil {
			return nil, err
		}
		found := r.stdout
		if r.timedOut { // the last line may be cut
			found = found[:bytes.LastIndexByte(found, '\n')+1]
		}
		lines(found, 5000) // what it found in time, unreadable folders left out
	}
	fileLists.Lock()
	if len(fileLists.m) > 64 {
		clear(fileLists.m)
	}
	fileLists.m[key] = fileList{time.Now(), files}
	fileLists.Unlock()
	return files, nil
}

// matchFiles ranks the files for a query: a name that starts with it first,
// then a name that holds it, then a path that holds its letters in order.
func matchFiles(files []string, q string, limit int) []string {
	q = strings.ToLower(strings.TrimSpace(q))
	type hit struct {
		f     string
		score int
	}
	var hits []hit
	for _, f := range files {
		lf := strings.ToLower(f)
		base := strings.ToLower(filepath.Base(f))
		score := 0
		switch {
		case q == "":
			score = 1
		case strings.HasPrefix(base, q):
			score = 400
		case strings.Contains(base, q):
			score = 300
		case strings.Contains(lf, q):
			score = 200
		case subsequence(lf, q):
			score = 100
		}
		if score == 0 {
			continue
		}
		score -= strings.Count(f, "/")*5 + len(f)/10 // shallower and shorter first
		if strings.HasPrefix(f, ".") || strings.Contains(f, "/.") {
			score -= 60 // dotfiles last
		}
		hits = append(hits, hit{f, score})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].f < hits[j].f
	})
	out := []string{}
	for i := 0; i < len(hits) && i < limit; i++ {
		out = append(out, hits[i].f)
	}
	return out
}

func subsequence(s, q string) bool {
	i := 0
	for _, c := range s {
		if i < len(q) && byte(c) == q[i] {
			i++
		}
	}
	return i == len(q)
}

func (s *server) files(w http.ResponseWriter, r *http.Request) {
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
	if fg.Cwd == "" {
		writeJSON(w, map[string]any{"cwd": "", "files": []string{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	list, err := listFilesOn(ctx, s.hostOf(t), fg.Cwd)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, map[string]any{"cwd": fg.Cwd, "files": matchFiles(list, r.URL.Query().Get("q"), 40)})
}
