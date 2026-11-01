package main

// The conversation of the agent in a terminal, read from the agent's own
// transcript, for the phone's chat view: what you asked, what it answered,
// and the tools it ran in between (folded).
//
// Known agents and where they write:
//   tego         ~/.tego/sessions/<cwd>/<id>.jsonl, held open while it runs (lsof finds it)
//   Claude Code  ~/.claude/projects/<cwd, non-alphanumerics as ->/<uuid>.jsonl, the newest
//   Codex        ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl, the newest for the cwd
//
// Transcripts grow to tens of MB: only the tail is read, and a parse is kept
// until the file changes.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ChatItem is one thing in the conversation.
type ChatItem struct {
	Kind  string     `json:"kind"` // user, assistant, tools, note
	Text  string     `json:"text,omitempty"`
	Tools []ChatTool `json:"tools,omitempty"`
}

// ChatTool is one tool call, with a line about what it did.
type ChatTool struct {
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"` // the command, the file, the pattern
	Result string `json:"result,omitempty"` // the first lines of what came back
	Error  bool   `json:"error,omitempty"`
}

type chatResult struct {
	Agent   string      `json:"agent"`  // tego, claude, codex; "" when unknown
	Source  string      `json:"source"` // the transcript's path; "" when none was found
	Items   []ChatItem  `json:"items"`
	Version string      `json:"version"`          // changes when the transcript does
	Status  *AgentEvent `json:"status,omitempty"` // what it reports now: waiting for you, and on what
}

const (
	chatTail     = 3 << 20 // bytes read from the end of a transcript
	chatMaxItems = 120
)

type chatCacheEntry struct {
	size  int64
	mtime time.Time
	items []ChatItem
}

var chatCache = struct {
	sync.Mutex
	m map[string]chatCacheEntry
}{m: map[string]chatCacheEntry{}}

func (s *server) chat(w http.ResponseWriter, r *http.Request) {
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
	agent, path := findTranscript(fg)
	res := chatResult{Agent: agent, Source: path, Items: []ChatItem{}}
	if s.events != nil {
		s.events.mu.Lock()
		if cur, ok := s.events.current[t]; ok {
			res.Status = &cur
		}
		s.events.mu.Unlock()
	}
	if path != "" {
		st, err := os.Stat(path)
		if err == nil {
			res.Version = fmt.Sprintf("%d-%d", st.Size(), st.ModTime().UnixNano())
			if r.URL.Query().Get("version") == res.Version {
				writeJSON(w, map[string]any{"agent": agent, "source": path, "version": res.Version, "same": true, "status": res.Status})
				return
			}
			res.Items, err = readChat(agent, path, st)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
		}
	}
	writeJSON(w, res)
}

// foregroundInfo is what runs in a terminal now, per hc.
type foregroundInfo struct {
	Name string
	Argv []string
	Cwd  string
	Pid  int
}

func (s *server) foreground(r *http.Request, terminal string) (foregroundInfo, error) {
	out, err := run(r.Context(), 5*time.Second, s.hc, "get", "--json", terminal)
	if err != nil {
		return foregroundInfo{}, err
	}
	var g struct {
		Cwd        string
		Foreground *foregroundInfo
		Status     *struct{ App string }
	}
	if err := json.Unmarshal(out, &g); err != nil {
		return foregroundInfo{}, err
	}
	fg := foregroundInfo{Cwd: g.Cwd}
	if g.Foreground != nil {
		fg = *g.Foreground
		if fg.Cwd == "" {
			fg.Cwd = g.Cwd
		}
	}
	if fg.Name == "" && g.Status != nil {
		fg.Name = g.Status.App // reported by the program itself
	}
	return fg, nil
}

// agentOf names the agent a foreground program is.
func agentOf(fg foregroundInfo) string {
	words := append([]string{fg.Name}, fg.Argv...)
	for _, w := range words {
		b := strings.ToLower(filepath.Base(w))
		switch {
		case b == "tego":
			return "tego"
		case b == "claude" || strings.Contains(b, "claude-code") || b == "claude-code":
			return "claude"
		case b == "codex":
			return "codex"
		}
	}
	return ""
}

func findTranscript(fg foregroundInfo) (agent, path string) {
	agent = agentOf(fg)
	home, _ := os.UserHomeDir()
	switch agent {
	case "tego":
		if p := openFile(fg.Pid, filepath.Join(home, ".tego", "sessions")); p != "" {
			return agent, p
		}
		return agent, newest(filepath.Join(home, ".tego", "sessions", tegoSlug(fg.Cwd)), ".jsonl")
	case "claude":
		return agent, newest(filepath.Join(home, ".claude", "projects", claudeSlug(fg.Cwd)), ".jsonl")
	case "codex":
		return agent, newestCodex(filepath.Join(home, ".codex", "sessions"), fg.Cwd)
	}
	return agent, ""
}

// openFile finds a .jsonl under dir that the process has open.
func openFile(pid int, dir string) string {
	if pid <= 0 {
		return ""
	}
	out, err := exec.Command("lsof", "-p", fmt.Sprint(pid), "-Fn").Output()
	if err != nil && len(out) == 0 {
		return ""
	}
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, "n"+dir+"/") && strings.HasSuffix(l, ".jsonl") {
			return l[1:]
		}
	}
	return ""
}

func newest(dir, ext string) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	best, bestT := "", time.Time{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ext) {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().After(bestT) {
			best, bestT = filepath.Join(dir, e.Name()), info.ModTime()
		}
	}
	return best
}

// tego names a cwd's session folder like this (internal/session.Dir): the
// real path with / \ : as dashes, without dashes at the ends.
func tegoSlug(cwd string) string {
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	return strings.Trim(strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' {
			return '-'
		}
		return r
	}, cwd), "-")
}

// Claude Code: every character but a letter or digit becomes a dash.
func claudeSlug(cwd string) string {
	b := []byte(cwd)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			b[i] = '-'
		}
	}
	return string(b)
}

// The newest Codex rollout, of the last week, whose session began in cwd.
func newestCodex(root, cwd string) string {
	var files []string
	for d := 0; d < 7; d++ {
		day := time.Now().AddDate(0, 0, -d)
		m, _ := filepath.Glob(filepath.Join(root, day.Format("2006/01/02"), "rollout-*.jsonl"))
		files = append(files, m...)
	}
	sort.Slice(files, func(i, j int) bool { return files[i] > files[j] }) // names start with the time
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		line, _ := bufio.NewReader(fh).ReadBytes('\n')
		fh.Close()
		var meta struct {
			Payload struct{ Cwd string }
			Cwd     string
		}
		json.Unmarshal(line, &meta)
		if meta.Payload.Cwd == cwd || meta.Cwd == cwd {
			return f
		}
	}
	return ""
}

func readChat(agent, path string, st os.FileInfo) ([]ChatItem, error) {
	chatCache.Lock()
	c, ok := chatCache.m[path]
	chatCache.Unlock()
	if ok && c.size == st.Size() && c.mtime.Equal(st.ModTime()) {
		return c.items, nil
	}
	lines, err := tailLines(path, chatTail)
	if err != nil {
		return nil, err
	}
	var items []ChatItem
	switch agent {
	case "codex":
		items = parseCodex(lines)
	default:
		items = parseTree(agent, lines)
	}
	if len(items) > chatMaxItems {
		items = items[len(items)-chatMaxItems:]
	}
	chatCache.Lock()
	chatCache.m[path] = chatCacheEntry{st.Size(), st.ModTime(), items}
	chatCache.Unlock()
	return items, nil
}

// tailLines returns the whole lines in the last n bytes of a file.
func tailLines(path string, n int64) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	start := st.Size() - n
	if start < 0 {
		start = 0
	}
	buf := make([]byte, st.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && len(buf) > 0 {
		if _, err2 := f.Seek(0, 0); err2 != nil {
			return nil, err
		}
	}
	if start > 0 { // the first line is cut: drop it
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}
	var out [][]byte
	for _, l := range bytes.Split(buf, []byte("\n")) {
		if len(bytes.TrimSpace(l)) > 0 {
			out = append(out, l)
		}
	}
	return out, nil
}

// A content block, as tego and Claude Code write them (Anthropic's shape).
type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Name      string          `json:"name"`
	ID        string          `json:"id"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type message struct {
	Role    string          `json:"role"`
	ID      string          `json:"id"`
	Content json.RawMessage `json:"content"`
}

// One transcript line with what places it in the conversation's tree.
type treeEntry struct {
	id, parent string
	msg        *message
	note       string // compaction and the like
}

// parseTree reads tego's and Claude Code's transcripts: entries linked to
// their parent, so a rewound or branched conversation shows only the branch
// in play (the one the last entry is on).
func parseTree(agent string, lines [][]byte) []ChatItem {
	entries := map[string]treeEntry{}
	last := ""
	for _, l := range lines {
		var e struct {
			Type        string   `json:"type"`
			ID          string   `json:"id"`
			Parent      *string  `json:"parent"`
			UUID        string   `json:"uuid"`
			ParentUUID  *string  `json:"parentUuid"`
			IsSidechain bool     `json:"isSidechain"`
			IsMeta      bool     `json:"isMeta"`
			Message     *message `json:"message"`
		}
		if json.Unmarshal(l, &e) != nil {
			continue
		}
		te := treeEntry{}
		if agent == "claude" {
			if e.UUID == "" || e.IsSidechain {
				continue
			}
			te.id = e.UUID
			if e.ParentUUID != nil {
				te.parent = *e.ParentUUID
			}
			if (e.Type == "user" || e.Type == "assistant") && !e.IsMeta {
				te.msg = e.Message
			}
		} else {
			if e.ID == "" {
				continue
			}
			te.id = e.ID
			if e.Parent != nil {
				te.parent = *e.Parent
			}
			switch e.Type {
			case "message":
				te.msg = e.Message
			case "compaction":
				te.note = "Earlier messages were summarised to free context."
			}
		}
		entries[te.id] = te
		if te.msg != nil || te.note != "" {
			last = te.id
		}
	}
	// The branch in play, oldest first.
	var chain []treeEntry
	seen := map[string]bool{}
	for id := last; id != "" && !seen[id]; {
		seen[id] = true
		e, ok := entries[id]
		if !ok {
			break // older than the tail read
		}
		chain = append(chain, e)
		id = e.parent
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}

	b := &chatBuilder{byID: map[string][2]int{}}
	for _, e := range chain {
		if e.note != "" {
			b.add(ChatItem{Kind: "note", Text: e.note})
			continue
		}
		if e.msg != nil {
			b.message(e.msg)
		}
	}
	return b.items
}

type chatBuilder struct {
	items []ChatItem
	byID  map[string][2]int // tool calls waiting for their result: item, tool
}

// call adds a tool call and remembers where it went, for its result.
func (b *chatBuilder) call(id string, t ChatTool) {
	b.add(ChatItem{Kind: "tools", Tools: []ChatTool{t}})
	i := len(b.items) - 1
	if id != "" {
		b.byID[id] = [2]int{i, len(b.items[i].Tools) - 1}
	}
}

// result fills in a call's result; false when the call isn't known.
func (b *chatBuilder) result(id, text string, isErr bool) {
	at, ok := b.byID[id]
	if !ok {
		return
	}
	t := &b.items[at[0]].Tools[at[1]]
	t.Result = firstLines(text, 3, 240)
	t.Error = isErr
	delete(b.byID, id)
}

func (b *chatBuilder) add(it ChatItem) {
	// Consecutive tool calls fold into one row; consecutive answers join.
	if n := len(b.items); n > 0 && b.items[n-1].Kind == it.Kind && (it.Kind == "tools" || it.Kind == "assistant") {
		if it.Kind == "tools" {
			b.items[n-1].Tools = append(b.items[n-1].Tools, it.Tools...)
		} else {
			b.items[n-1].Text += "\n\n" + it.Text
		}
		return
	}
	b.items = append(b.items, it)
}

func (b *chatBuilder) message(m *message) {
	var blocks []block
	if len(m.Content) > 0 && m.Content[0] == '"' {
		var s string
		json.Unmarshal(m.Content, &s)
		blocks = []block{{Type: "text", Text: s}}
	} else {
		json.Unmarshal(m.Content, &blocks)
	}
	var userText []string
	for _, bl := range blocks {
		switch bl.Type {
		case "text":
			t := strings.TrimSpace(bl.Text)
			if t == "" || hiddenText(t) {
				continue
			}
			if m.Role == "user" {
				userText = append(userText, t)
			} else {
				b.add(ChatItem{Kind: "assistant", Text: t})
			}
		case "image":
			if m.Role == "user" {
				userText = append(userText, "[image]")
			}
		case "tool_use":
			b.call(bl.ID, ChatTool{Name: bl.Name, Detail: toolDetail(bl.Input)})
		case "tool_result":
			b.result(bl.ToolUseID, contentText(bl.Content), bl.IsError)
		}
	}
	if len(userText) > 0 {
		b.add(ChatItem{Kind: "user", Text: strings.Join(userText, "\n\n")})
	}
}

// Text an agent adds to your message for itself, not something you wrote.
func hiddenText(t string) bool {
	for _, p := range []string{"<system-reminder>", "<context-summary>", "<command-name>", "<command-message>", "<local-command-stdout>", "<local-command-caveat>", "Caveat: The messages below", "[Request interrupted"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		json.Unmarshal(raw, &s)
		return s
	}
	var blocks []block
	json.Unmarshal(raw, &blocks)
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// toolDetail picks what says most about a call: its command, file or query.
func toolDetail(raw json.RawMessage) string {
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil {
		return ""
	}
	for _, k := range []string{"command", "cmd", "file_path", "path", "pattern", "query", "url", "description", "prompt", "name"} {
		if v, ok := in[k].(string); ok && strings.TrimSpace(v) != "" {
			return firstLines(v, 1, 160)
		}
		if v, ok := in[k].([]any); ok && len(v) > 0 { // codex: ["bash", "-lc", "…"]
			parts := make([]string, 0, len(v))
			for _, p := range v {
				parts = append(parts, fmt.Sprint(p))
			}
			return firstLines(strings.Join(parts, " "), 1, 160)
		}
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v, ok := in[k].(string); ok && v != "" {
			return firstLines(v, 1, 160)
		}
	}
	return ""
}

func firstLines(s string, n, max int) string {
	s = strings.TrimSpace(s)
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = append(lines[:n], "…")
	}
	s = strings.Join(lines, "\n")
	if r := []rune(s); len(r) > max {
		s = string(r[:max]) + "…"
	}
	return s
}

// parseCodex reads a Codex rollout: messages, function calls and their output.
func parseCodex(lines [][]byte) []ChatItem {
	b := &chatBuilder{byID: map[string][2]int{}}
	for _, l := range lines {
		var e struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(l, &e) != nil {
			continue
		}
		raw := l
		if e.Type == "response_item" {
			raw = e.Payload
		}
		var it struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type, Text string
			} `json:"content"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			CallID    string `json:"call_id"`
			Output    any    `json:"output"`
		}
		if json.Unmarshal(raw, &it) != nil {
			continue
		}
		switch it.Type {
		case "message":
			var parts []string
			for _, c := range it.Content {
				if t := strings.TrimSpace(c.Text); t != "" && !strings.HasPrefix(t, "<environment_context>") && !strings.HasPrefix(t, "<user_instructions>") {
					parts = append(parts, t)
				}
			}
			if len(parts) == 0 || it.Role == "developer" || it.Role == "system" {
				continue
			}
			kind := "assistant"
			if it.Role == "user" {
				kind = "user"
			}
			b.add(ChatItem{Kind: kind, Text: strings.Join(parts, "\n\n")})
		case "function_call", "custom_tool_call", "local_shell_call":
			b.call(it.CallID, ChatTool{Name: it.Name, Detail: toolDetail(json.RawMessage(it.Arguments))})
		case "function_call_output", "custom_tool_call_output":
			if _, ok := b.byID[it.CallID]; ok {
				out := ""
				switch o := it.Output.(type) {
				case string:
					out = o
					var wrapped struct{ Output string }
					if json.Unmarshal([]byte(o), &wrapped) == nil && wrapped.Output != "" {
						out = wrapped.Output
					}
				default:
					bs, _ := json.Marshal(o)
					out = string(bs)
				}
				b.result(it.CallID, out, false)
			}
		}
	}
	return b.items
}
