package main

// What the phone's message box offers as you type: "/" lists the agent's
// commands (its own, your custom commands and skills), "@" the files of the
// folder the agent works in.

import (
	"bufio"
	"context"
	"errors"
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
	writeJSON(w, map[string]any{"agent": agent, "commands": commandsFor(agent, fg.Cwd)})
}

func commandsFor(agent, cwd string) []command {
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
	home, _ := os.UserHomeDir()
	// Custom commands and skills: the user's, then each folder from cwd up to home.
	dirs := []string{home}
	for d := cwd; d != "" && d != "/" && d != home && strings.HasPrefix(d, home); d = filepath.Dir(d) {
		dirs = append(dirs, d)
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
	kinds := []string{".claude"}
	if agent == "tego" {
		kinds = []string{".agents", ".claude", ".tego"}
	}
	for _, d := range dirs {
		for _, k := range kinds {
			files, _ := filepath.Glob(filepath.Join(d, k, "commands", "*.md"))
			for _, f := range files {
				name := strings.TrimSuffix(filepath.Base(f), ".md")
				add(command{"/" + name, mdDescription(f)})
			}
			skills, _ := filepath.Glob(filepath.Join(d, k, "skills", "*", "SKILL.md"))
			for _, f := range skills {
				name := filepath.Base(filepath.Dir(f))
				if n := frontmatter(f, "name"); n != "" {
					name = n
				}
				if agent == "tego" {
					add(command{"/skill:" + name, mdDescription(f)})
				} else {
					add(command{"/" + name, mdDescription(f)})
				}
			}
		}
	}
	return out
}

// frontmatter reads one key of a Markdown file's YAML front matter.
func frontmatter(path, key string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
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
func mdDescription(path string) string {
	if d := frontmatter(path, "description"); d != "" {
		return firstLines(d, 1, 120)
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
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
	writeJSON(w, map[string]any{"cwd": fg.Cwd, "files": matchFiles(listFiles(ctx, fg.Cwd), r.URL.Query().Get("q"), 40)})
}
