package main

// The conversation of the agent in a terminal, read from the agent's own
// transcript, for the phone's chat view: what you asked, what it answered,
// and the tools it ran in between (folded).
//
// Known agents and where they write:
//   tego         ~/.tego/sessions/<cwd>/<id>.jsonl, held open while it runs (lsof finds it)
//   Claude Code  ~/.claude/projects/<cwd, non-alphanumerics as ->/<session>.jsonl, the session
//                named in ~/.claude/sessions/<pid>.json
//   Codex        ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl, the newest for the cwd
//
// The file is always the one that process names or holds open (it, or a
// process it started: a wrapper script runs the real program), never "the
// newest in that folder": two sessions in one folder would show each
// other's chat. A session that has said nothing yet has no file: the chat is
// empty.
//
// Transcripts grow to tens of MB: only the tail is read (more when a rewind
// went back further than that, or the phone scrolls back to older messages),
// what was read is kept, and a growing file only has its new lines read.

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ChatItem is one thing in the conversation.
type ChatItem struct {
	ID     string     `json:"id,omitempty"` // stable across reads: the transcript entry it starts at
	Kind   string     `json:"kind"`         // user, assistant, tools, note
	Text   string     `json:"text,omitempty"`
	Images []string   `json:"images,omitempty"` // ids for /api/image (chatimage.go)
	Tools  []ChatTool `json:"tools,omitempty"`
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
	Start   bool        `json:"start"`            // nothing older to come: the items begin the conversation (or the most read does)
	Limit   int         `json:"limit"`            // the most items asked for
	Current             // the model and thinking level it runs with now (models.go)
}

// The most read of a transcript on this Mac: back to where a rewind branched
// off, or to older messages (a variable for tests).
var chatReadMax int64 = 128 << 20

const (
	chatTail     = 3 << 20 // bytes read from the end of a transcript
	chatMaxItems = 120     // items sent unless the phone asks for more (?limit=)
	chatAllItems = 20000   // the most it may ask for: the whole conversation, for search
)

// The items a read gave, for one transcript (on one machine) and limit,
// until the file changes.
type chatKey struct {
	machine, path string
	limit         int
}

type chatCacheEntry struct {
	size  int64
	mtime time.Time
	items []ChatItem
	start bool
}

var chatCache = struct {
	sync.Mutex
	m map[chatKey]chatCacheEntry
}{m: map[chatKey]chatCacheEntry{}}

// chatLimit: ?limit=, within bounds; the default when absent.
func chatLimit(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 {
		return chatMaxItems
	}
	return min(n, chatAllItems)
}

// cachedChat: the items of the last read, if the file hasn't changed since
// and the images they name can still be served.
func cachedChat(path string, limit int, st os.FileInfo) (chatCacheEntry, bool) {
	return cachedChatOn("", path, limit, st.Size(), st.ModTime())
}

func cachedChatOn(machine, path string, limit int, size int64, mtime time.Time) (chatCacheEntry, bool) {
	chatCache.Lock()
	c, ok := chatCache.m[chatKey{machine, path, limit}]
	chatCache.Unlock()
	if !ok || c.size != size || !c.mtime.Equal(mtime) {
		return c, false
	}
	for _, it := range c.items {
		for _, id := range it.Images {
			if !images.has(id) {
				return c, false
			}
		}
	}
	return c, true
}

// GET /api/chat?terminal=t12[&version=…][&limit=300]: the last limit items
// of the conversation (120 by default) and whether they reach its start.
// The phone asks for more as you scroll back, and keeps asking with that
// limit; with the version of its last answer, an unchanged conversation
// comes back without its items (same: true).
func (s *server) chat(w http.ResponseWriter, r *http.Request) {
	t := r.URL.Query().Get("terminal")
	if !validTerminal(t) {
		writeErr(w, http.StatusBadRequest, errors.New("need terminal"))
		return
	}
	limit := chatLimit(r)
	fg, err := s.foreground(r, t)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	h := s.hostOf(t)
	agent, path := s.findTranscriptOn(r.Context(), h, fg)
	res := chatResult{Agent: agent, Source: path, Items: []ChatItem{}, Limit: limit, Start: true}
	if s.events != nil {
		s.events.mu.Lock()
		if cur, ok := s.events.current[t]; ok {
			res.Status = &cur
		}
		s.events.mu.Unlock()
	}
	// The screen: the model and level tego and Claude Code show.
	if agent == "tego" || agent == "claude" {
		if screen, err := s.screenText(r.Context(), t); err == nil {
			if h.remote() {
				res.Current = screenCurrent(agent, screen) // its settings are on that machine
			} else {
				res.Current = s.current(agent, fg.Argv, screen, path)
			}
		}
	}
	if path != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		size, mtime, err := h.statFile(ctx, path)
		if err == nil {
			res.Version = fmt.Sprintf("%d-%d-%d", size, mtime.UnixNano(), limit)
			if c, ok := cachedChatOn(h.machine, path, limit, size, mtime); ok && r.URL.Query().Get("version") == res.Version {
				writeJSON(w, map[string]any{"agent": agent, "source": path, "version": res.Version, "same": true,
					"status": res.Status, "start": c.start, "limit": limit,
					"model": res.Model, "modelLabel": res.ModelLabel, "thinking": res.Thinking})
				return
			}
			res.Items, res.Start, err = readChatOn(ctx, h, agent, path, size, mtime, limit)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
		}
	}
	writeJSONGzip(w, r, res)
}

// writeJSONGzip: a long history is hundreds of KB of JSON, a fifth of that
// compressed (the phone's HTTP client asks for gzip and unpacks it itself).
func writeJSONGzip(w http.ResponseWriter, r *http.Request, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if len(b) < 8<<10 || !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Write(append(b, '\n'))
		return
	}
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Add("Vary", "Accept-Encoding")
	z, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)
	z.Write(append(b, '\n'))
	z.Close()
}

// foregroundInfo is what runs in a terminal now, per hc.
type foregroundInfo struct {
	Name string
	Argv []string
	Cwd  string
	Pid  int
}

func (s *server) foreground(r *http.Request, terminal string) (foregroundInfo, error) {
	now, err := s.terminalNow(r.Context(), terminal)
	return now.fg, err
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
	pids := pidTree(fg.Pid)
	switch agent {
	case "tego": // opens its session file when it starts
		return agent, openFile(pids, filepath.Join(home, ".tego", "sessions"))
	case "claude":
		for _, pid := range pids {
			if p := claudeSession(home, pid); p != "" {
				return agent, p
			}
		}
		// An older Claude Code without per-process files: the file it has open, if any.
		return agent, openFile(pids, filepath.Join(home, ".claude", "projects"))
	case "codex":
		return agent, openFile(pids, filepath.Join(home, ".codex", "sessions"))
	}
	return agent, ""
}

// findTranscriptOn: findTranscript on the terminal's machine (a remote
// foreground pid is a pid there).
func (s *server) findTranscriptOn(ctx context.Context, h host, fg foregroundInfo) (agent, path string) {
	if !h.remote() {
		return findTranscript(fg)
	}
	agent = agentOf(fg)
	if agent == "" || fg.Pid <= 0 {
		return agent, ""
	}
	key := fmt.Sprintf("%s\x00%d\x00%s", h.machine, fg.Pid, agent)
	remoteTranscripts.Lock()
	e, ok := remoteTranscripts.m[key]
	remoteTranscripts.Unlock()
	if ok && time.Since(e.at) < remoteTranscriptTTL {
		return agent, e.path
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	home, err := h.homeDir(ctx)
	if err != nil {
		return agent, ""
	}
	path, err = locateTranscript(ctx, h, agent, home, fg.Pid)
	if err != nil {
		return agent, "" // not kept: asked again next time
	}
	remoteTranscripts.Lock()
	if len(remoteTranscripts.m) > 256 {
		clear(remoteTranscripts.m)
	}
	remoteTranscripts.m[key] = transcriptEntry{time.Now(), path}
	remoteTranscripts.Unlock()
	return agent, path
}

// Where the agents on other machines write, by machine, pid and agent: kept
// a little while (a round trip saved on each poll), short enough to follow a
// new session in the same process (/clear, /new).
const remoteTranscriptTTL = 10 * time.Second

var remoteTranscripts = struct {
	sync.Mutex
	m map[string]transcriptEntry
}{m: map[string]transcriptEntry{}}

type transcriptEntry struct {
	at   time.Time
	path string
}

// On another machine (macOS or Linux) in one round trip: the process tree
// under $1 (BFS, at most 64), Claude Code's per-process files for those
// pids (with $2 = claude; $3 the home), and the .jsonl files they hold open:
// lsof where there is one, else /proc. Lines: "T pids…", "S<pid> <json>",
// "n<path>".
const locateScript = `tree=$(ps -A -o pid=,ppid= 2>/dev/null | awk -v root="$1" '
{ kids[$2] = kids[$2] " " $1 }
END {
  out = root; q = root; n = 1
  while (q != "" && n < 64) {
    k = split(q, a, " "); q = ""
    for (i = 1; i <= k; i++) if (a[i] in kids) {
      m = split(kids[a[i]], b, " ")
      for (j = 1; j <= m && n < 64; j++) { out = out " " b[j]; q = q " " b[j]; n++ }
    }
  }
  print out
}')
[ -n "$tree" ] || tree=$1
echo "T $tree"
if [ "$2" = claude ]; then
  for p in $tree; do
    f="$3/.claude/sessions/$p.json"
    [ -f "$f" ] && { printf 'S%s ' "$p"; tr -d '\n' < "$f"; echo; }
  done
fi
l=$(command -v lsof 2>/dev/null) || l=
[ -z "$l" ] && [ -x /usr/sbin/lsof ] && l=/usr/sbin/lsof
if [ -n "$l" ]; then
  "$l" -a -p "$(echo $tree | tr ' ' ',')" -Fn 2>/dev/null | grep '^n.*\.jsonl$'
elif [ -d /proc ]; then
  for p in $tree; do
    for fd in /proc/$p/fd/*; do
      t=$(readlink "$fd" 2>/dev/null) || continue
      case "$t" in *.jsonl) echo "n$t";; esac
    done
  done
fi
exit 0`

// locateTranscript: the transcript of an agent running as pid on another
// machine, as findTranscript finds it here ("" when it has none yet).
func locateTranscript(ctx context.Context, h host, agent, home string, pid int) (string, error) {
	out, err := h.run(ctx, "", "sh", "-c", locateScript, "sh", strconv.Itoa(pid), agent, home)
	if err != nil {
		return "", err
	}
	var tree []int
	sessions := map[int][]byte{}
	var open []string
	for _, l := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(l, "T "):
			for _, f := range strings.Fields(l[2:]) {
				if p, err := strconv.Atoi(f); err == nil {
					tree = append(tree, p)
				}
			}
		case strings.HasPrefix(l, "S"):
			if p, js, ok := strings.Cut(l[1:], " "); ok {
				if n, err := strconv.Atoi(p); err == nil {
					sessions[n] = []byte(js)
				}
			}
		case strings.HasPrefix(l, "n"):
			open = append(open, l[1:])
		}
	}
	held := func(dir string) string {
		for _, f := range open {
			if strings.HasPrefix(f, dir+"/") && strings.HasSuffix(f, ".jsonl") {
				return f
			}
		}
		return ""
	}
	switch agent {
	case "tego":
		return held(path.Join(home, ".tego", "sessions")), nil
	case "claude":
		for _, p := range tree {
			if f := claudeSessionIn(home, sessions[p]); f != "" {
				return f, nil
			}
		}
		return held(path.Join(home, ".claude", "projects")), nil
	case "codex":
		return held(path.Join(home, ".codex", "sessions")), nil
	}
	return "", nil
}

// pidTree: the process and every process under it.
func pidTree(pid int) []int {
	if pid <= 0 {
		return nil
	}
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=").Output()
	if err != nil {
		return []int{pid}
	}
	children := map[int][]int{}
	for _, l := range strings.Split(string(out), "\n") {
		var p, pp int
		if n, _ := fmt.Sscan(l, &p, &pp); n == 2 {
			children[pp] = append(children[pp], p)
		}
	}
	tree := []int{pid}
	for i := 0; i < len(tree) && len(tree) < 64; i++ {
		tree = append(tree, children[tree[i]]...)
	}
	return tree
}

// lsofPath: macOS keeps lsof in /usr/sbin, which a LaunchAgent's PATH may
// leave out.
func lsofPath() string {
	if _, err := os.Stat("/usr/sbin/lsof"); err == nil {
		return "/usr/sbin/lsof"
	}
	return "lsof"
}

// openFile finds a .jsonl under dir that one of the processes has open.
func openFile(pids []int, dir string) string {
	if len(pids) == 0 {
		return ""
	}
	list := make([]string, len(pids))
	for i, p := range pids {
		list[i] = fmt.Sprint(p)
	}
	out, err := exec.Command(lsofPath(), "-a", "-p", strings.Join(list, ","), "-Fn").Output()
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

// claudeSession: the transcript of the session Claude Code runs in that
// process, from the file it keeps per process (it names the session, and
// follows /clear and /resume). It may not exist yet: nothing said.
func claudeSession(home string, pid int) string {
	if pid <= 0 {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude", "sessions", fmt.Sprintf("%d.json", pid)))
	if err != nil {
		return ""
	}
	return claudeSessionIn(home, b)
}

// claudeSessionIn: the transcript a per-process file of Claude Code's names.
func claudeSessionIn(home string, b []byte) string {
	if len(b) == 0 {
		return ""
	}
	var s struct{ SessionID, Cwd string }
	if json.Unmarshal(b, &s) != nil || s.SessionID == "" || strings.ContainsAny(s.SessionID, "/\\") {
		return ""
	}
	return filepath.Join(home, ".claude", "projects", claudeSlug(s.Cwd), s.SessionID+".jsonl")
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

// readChat: the last limit items of the conversation in a transcript, and
// whether they begin it (nothing older to come).
func readChat(agent, path string, st os.FileInfo, limit int) ([]ChatItem, bool, error) {
	return readChatOn(context.Background(), host{}, agent, path, st.Size(), st.ModTime(), limit)
}

// readChatOn: readChat of a transcript on a machine, of that size and mtime.
func readChatOn(ctx context.Context, h host, agent, path string, size int64, mtime time.Time, limit int) ([]ChatItem, bool, error) {
	if c, ok := cachedChatOn(h.machine, path, limit, size, mtime); ok {
		return c.items, c.start, nil
	}
	var items []ChatItem
	var start bool
	var err error
	if agent == "codex" {
		items, start, err = readCodex(ctx, h, path, size, limit)
	} else {
		items, start, err = readTree(ctx, h, agent, path, size, limit)
	}
	if err != nil {
		return nil, false, err
	}
	if len(items) > limit {
		items = items[len(items)-limit:]
	}
	if items == nil {
		items = []ChatItem{} // a new session: an empty list, not null
	}
	chatCache.Lock()
	for k, c := range chatCache.m { // older reads of this file are done with
		if k.machine == h.machine && k.path == path && (c.size != size || !c.mtime.Equal(mtime)) {
			delete(chatCache.m, k)
		}
	}
	if len(chatCache.m) > 64 {
		clear(chatCache.m)
	}
	chatCache.m[chatKey{h.machine, path, limit}] = chatCacheEntry{size, mtime, items, start}
	chatCache.Unlock()
	return items, start, nil
}

// readTree reads tego's and Claude Code's transcripts through a scan kept
// per file: a grown file has only its new lines read. The scan reaches back
// further (and is read again from there) while the branch in play goes on
// before it (a rewind to an old point) or it holds fewer items than asked.
func readTree(ctx context.Context, h host, agent, path string, size int64, limit int) ([]ChatItem, bool, error) {
	readMax := h.chatReadMax()
	sc := scanOf(h.machine + "\x00" + path)
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.host, sc.files = h, images.resolver(h)
	if sc.agent != agent || !sc.loaded || size < sc.to {
		if err := sc.load(ctx, agent, path, size-chatTail); err != nil {
			return nil, false, err
		}
	} else if size > sc.to {
		if err := sc.extend(ctx, path); err != nil {
			return nil, false, err
		}
	}
	for {
		items, cut := sc.build()
		read := sc.to - sc.from
		// Capped by what was asked for, not what came: a line cut at the start
		// is dropped, so the lines read always fall short of readMax (and asking
		// for that again would read the same forever).
		whole, capped := sc.from == 0 || sc.asked == 0, sc.to-sc.asked >= readMax
		if whole || capped || (!cut && len(items) >= limit) {
			return items, len(items) <= limit && (whole || capped), nil
		}
		// Twice as far back; for more items, as far as they should take at
		// the bytes per item so far (one read for the whole conversation).
		next := max(read*2, chatTail)
		if !cut && len(items) > 0 {
			next = max(next, read/int64(len(items))*int64(limit)*5/4)
		}
		if err := sc.load(ctx, agent, path, sc.to-min(next, readMax)); err != nil {
			return nil, false, err
		}
	}
}

// The most read of a transcript on another machine (a round trip per 6 MB).
const chatRemoteReadMax = 4 * remoteReadMax

func (h host) chatReadMax() int64 {
	if h.remote() {
		return chatRemoteReadMax
	}
	return chatReadMax
}

// readCodex: Codex's rollouts are a plain list; the tail is read again,
// further back while it holds fewer items than asked.
func readCodex(ctx context.Context, h host, path string, size int64, limit int) ([]ChatItem, bool, error) {
	readMax := int64(chatReadMax)
	if h.remote() {
		readMax = remoteReadMax // one round trip
	}
	for n := min(int64(chatTail), readMax); ; n = min(n*2, readMax) {
		lines, err := tailLinesOn(ctx, h, path, n)
		if err != nil {
			return nil, false, err
		}
		items := parseCodexWith(lines, images.resolver(h)) // (it names no pictures by path)
		whole, capped := n >= size, n >= readMax
		if whole || capped || len(items) >= limit {
			return items, len(items) <= limit && (whole || capped), nil
		}
	}
}

// The scans kept, one per transcript, the least recently used dropped.
const scansKept = 8

var scans = struct {
	sync.Mutex
	m map[string]*treeScan
}{m: map[string]*treeScan{}}

func scanOf(path string) *treeScan {
	scans.Lock()
	defer scans.Unlock()
	sc, ok := scans.m[path]
	if !ok {
		if len(scans.m) >= scansKept {
			var old string
			var at time.Time
			for p, s := range scans.m {
				if old == "" || s.used.Before(at) {
					old, at = p, s.used
				}
			}
			delete(scans.m, old)
		}
		sc = &treeScan{}
		scans.m[path] = sc
	}
	sc.used = time.Now()
	return sc
}

// readLines returns the whole lines between from and the file's last
// newline: a line cut by from is dropped, as is a last line still being
// written. first: where the first line returned starts; end: after the last.
func readLines(path string, from int64) (lines [][]byte, first, end int64, err error) {
	return readLinesOn(context.Background(), host{}, path, from)
}

func readLinesOn(ctx context.Context, h host, path string, from int64) (lines [][]byte, first, end int64, err error) {
	from = max(from, 0)
	at := max(from-1, 0) // one byte before: is from the start of a line?
	buf, size, err := h.readAll(ctx, path, at, math.MaxInt64)
	if err != nil {
		return nil, 0, 0, err
	}
	if at >= size {
		return nil, from, from, nil
	}
	first = at
	if from > 0 {
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			return nil, from, from, nil
		}
		buf, first = buf[i+1:], at+int64(i)+1
	}
	last := bytes.LastIndexByte(buf, '\n')
	if last < 0 {
		return nil, first, first, nil
	}
	buf, end = buf[:last+1], first+int64(last)+1
	for _, l := range bytes.Split(buf, []byte("\n")) {
		if len(bytes.TrimSpace(l)) > 0 {
			lines = append(lines, l)
		}
	}
	return lines, first, end, nil
}

// tailLines returns the whole lines in the last n bytes of a file.
func tailLines(path string, n int64) ([][]byte, error) {
	return tailLinesOn(context.Background(), host{}, path, n)
}

func tailLinesOn(ctx context.Context, h host, path string, n int64) ([][]byte, error) {
	buf, size, err := h.readFrom(ctx, path, -n, n)
	if err != nil {
		return nil, err
	}
	if start := size - int64(len(buf)); start > 0 { // the first line is cut: drop it
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
	// An image: tego writes media_type and data in the block, Anthropic's
	// shape in a source.
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	Source    *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	} `json:"source"`
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
	at         int    // its place among the entries read
	parts      []block
	decoded    bool
}

// blocks: the message's content, decoded once and kept small: a call keeps
// the line that says what it did, a result its first lines.
func (e *treeEntry) blocks() []block {
	if e.decoded || e.msg == nil {
		return e.parts
	}
	e.decoded = true
	c := e.msg.Content
	if len(c) > 0 && c[0] == '"' {
		var s string
		json.Unmarshal(c, &s)
		e.parts = []block{{Type: "text", Text: s}}
	} else {
		json.Unmarshal(c, &e.parts)
	}
	for i := range e.parts {
		bl := &e.parts[i]
		switch bl.Type {
		case "tool_use":
			bl.Text = toolDetail(bl.Input)
		case "tool_result":
			bl.Text = firstLines(contentText(bl.Content), 3, 240)
		case "image":
			if bl.Source != nil && bl.Data == "" && bl.Source.Type == "base64" {
				bl.MediaType, bl.Data = bl.Source.MediaType, bl.Source.Data
			}
		}
		bl.Input, bl.Content, bl.Source = nil, nil, nil
	}
	e.msg.Content = nil
	return e.parts
}

// A walk that stops this many entries after the start of what was read left
// a dead branch behind it: the branch in play goes on further back.
const chatDeadBranch = 16

// treeScan: what was read of a tego or Claude Code transcript, from a line
// at from to the end of the line before to. A grown file adds its new lines.
type treeScan struct {
	mu       sync.Mutex
	host     host                // the machine the transcript is on
	files    func(string) string // the id of a picture a message names (chatimage.go)
	used     time.Time           // for dropping the least recently used
	loaded   bool
	agent    string
	from, to int64
	asked    int64 // where the last load was asked to start (from: after a line cut there)
	entries  map[string]*treeEntry
	order    []*treeEntry
	last     string
}

func newTreeScan(agent string) *treeScan {
	return &treeScan{agent: agent, loaded: true, entries: map[string]*treeEntry{}}
}

// load reads the transcript afresh from offset from (a line cut there is dropped).
func (sc *treeScan) load(ctx context.Context, agent, path string, from int64) error {
	lines, first, end, err := readLinesOn(ctx, sc.host, path, from)
	if err != nil {
		sc.loaded = false
		return err
	}
	if sc.host.remote() {
		images.prefetch(ctx, sc.host, lines)
	}
	fresh := newTreeScan(agent)
	fresh.add(lines)
	fresh.from, fresh.to = first, end
	sc.loaded, sc.agent, sc.from, sc.to, sc.asked = true, agent, first, end, max(from, 0)
	sc.entries, sc.order, sc.last = fresh.entries, fresh.order, fresh.last
	return nil
}

// extend reads the lines written since.
func (sc *treeScan) extend(ctx context.Context, path string) error {
	lines, _, end, err := readLinesOn(ctx, sc.host, path, sc.to)
	if err != nil {
		return err
	}
	if sc.host.remote() {
		images.prefetch(ctx, sc.host, lines)
	}
	sc.add(lines)
	sc.to = max(sc.to, end)
	return nil
}

// parseTree reads tego's and Claude Code's transcripts: entries linked to
// their parent, so a rewound or branched conversation shows only the branch
// in play: the one the last entry is on, or, in tego, the one a rewind
// (a "leaf" entry) moved to.
//
// cut: the branch in play goes back past the lines given, behind entries of
// another branch (a rewind to a point older than the tail): read more.
func parseTree(agent string, lines [][]byte) (items []ChatItem, cut bool) {
	sc := newTreeScan(agent)
	sc.add(lines)
	return sc.build()
}

// add takes in lines, in the file's order.
func (sc *treeScan) add(lines [][]byte) {
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
		te := &treeEntry{}
		if sc.agent == "claude" {
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
			// tego: only messages and compactions are nodes of the tree. A
			// rewind (/tree, editing an earlier message) writes
			// {"type":"leaf","id":<node>}: the id is the node the conversation
			// now continues from, not a node of its own; "" starts over.
			switch e.Type {
			case "message":
				te.msg = e.Message
			case "compaction":
				te.note = "Earlier messages were summarised to free context."
			case "leaf":
				sc.last = e.ID
				continue
			default:
				continue
			}
			if e.ID == "" {
				continue
			}
			te.id = e.ID
			if e.Parent != nil {
				te.parent = *e.Parent
			}
		}
		if old, ok := sc.entries[te.id]; ok { // written again: same place
			te.at = old.at
			*old = *te
			te = old
		} else {
			te.at = len(sc.order)
			sc.entries[te.id] = te
			sc.order = append(sc.order, te)
		}
		if te.msg != nil || te.note != "" {
			sc.last = te.id
		}
	}
}

// build: the items of the branch in play, as far back as was read.
func (sc *treeScan) build() (items []ChatItem, cut bool) {
	entries, order := sc.entries, sc.order
	// The branch in play: from the last entry back to the first.
	on := map[string]bool{}
	oldest := len(order) // where the walk stopped: the entries read before it are on other branches
	for id := sc.last; id != "" && !on[id]; {
		e, ok := entries[id]
		if !ok {
			cut = true // older than the lines read
			break
		}
		on[id] = true
		oldest = e.at
		id = e.parent
	}
	if cut {
		// Only a walk that stops well after the start left another branch
		// behind; one that stops at the start is simply a long conversation.
		dead := 0
		for _, e := range order[:oldest] {
			if !on[e.id] {
				dead++
			}
		}
		cut = dead > chatDeadBranch
	}
	if sc.agent == "claude" {
		parallelResults(order, entries, on)
	}

	b := newChatBuilder()
	if sc.files != nil {
		b.files = sc.files
	}
	for _, e := range order { // the file's order: a parent before its children
		if !on[e.id] {
			continue
		}
		b.cur = e.id
		if e.note != "" {
			b.add(ChatItem{Kind: "note", Text: e.note})
			continue
		}
		if e.msg != nil {
			b.message(e.msg.Role, e.blocks())
		}
	}
	return b.items, cut
}

// parallelResults: Claude Code writes the calls of one answer as a chain of
// entries (one per tool_use) and each result as a child of its call, so the
// conversation goes on from only one of them and the other calls and results
// hang off the branch in play. They belong to it: an off-branch entry joins
// when its parent is on the branch and it is the same answer (same message
// id) or only results to calls on the branch. A rewind's branch starts with
// a message you wrote, so it never joins.
func parallelResults(order []*treeEntry, entries map[string]*treeEntry, on map[string]bool) {
	calls := map[string]bool{}
	callsOf := func(e *treeEntry) {
		if e.msg != nil && e.msg.Role == "assistant" {
			for _, bl := range e.blocks() {
				if bl.Type == "tool_use" && bl.ID != "" {
					calls[bl.ID] = true
				}
			}
		}
	}
	for _, e := range order {
		if on[e.id] {
			callsOf(e)
		}
	}
	for changed := true; changed; {
		changed = false
		for _, e := range order {
			p, ok := entries[e.parent]
			if on[e.id] || e.msg == nil || !ok || !on[p.id] {
				continue
			}
			join := false
			switch e.msg.Role {
			case "assistant":
				join = e.msg.ID != "" && p.msg != nil && p.msg.ID == e.msg.ID
			case "user":
				if blocks := e.blocks(); len(blocks) > 0 {
					join = true
					for _, bl := range blocks {
						if bl.Type != "tool_result" || !calls[bl.ToolUseID] {
							join = false
						}
					}
				}
			}
			if join {
				on[e.id] = true
				callsOf(e)
				changed = true
			}
		}
	}
}

type chatBuilder struct {
	files func(string) string // the id of a picture a message names by its path; "" when it isn't one
	items []ChatItem
	byID  map[string][2]int // tool calls waiting for their result: item, tool
	cur   string            // the entry being read: the id of the items it starts
	seq   map[string]int    // items started per entry
}

func newChatBuilder() *chatBuilder {
	return &chatBuilder{files: images.addFile, byID: map[string][2]int{}, seq: map[string]int{}}
}

// call adds a tool call and remembers where it went, for its result.
func (b *chatBuilder) call(id string, t ChatTool) {
	b.add(ChatItem{Kind: "tools", Tools: []ChatTool{t}})
	i := len(b.items) - 1
	if id != "" {
		b.byID[id] = [2]int{i, len(b.items[i].Tools) - 1}
	}
}

// result fills in a call's result (its first lines); nothing when the call isn't known.
func (b *chatBuilder) result(id, text string, isErr bool) {
	at, ok := b.byID[id]
	if !ok {
		return
	}
	t := &b.items[at[0]].Tools[at[1]]
	t.Result = text
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
	// The id: the entry it starts at, and which of that entry's items it is.
	if b.cur != "" {
		it.ID = b.cur
		if n := b.seq[b.cur]; n > 0 {
			it.ID += "." + strconv.Itoa(n)
		}
		b.seq[b.cur]++
	}
	b.items = append(b.items, it)
}

func (b *chatBuilder) message(role string, blocks []block) {
	var userText, userImages []string
	for _, bl := range blocks {
		switch bl.Type {
		case "text":
			t := strings.TrimSpace(bl.Text)
			if t == "" || hiddenText(t) {
				continue
			}
			if role == "user" {
				userText = append(userText, t)
			} else {
				b.add(ChatItem{Kind: "assistant", Text: t})
			}
		case "image":
			if role != "user" {
				continue
			}
			if id := images.addBlock(bl.MediaType, bl.Data); id != "" {
				userImages = append(userImages, id)
			} else {
				userText = append(userText, "[image]")
			}
		case "tool_use":
			b.call(bl.ID, ChatTool{Name: bl.Name, Detail: bl.Text})
		case "tool_result":
			b.result(bl.ToolUseID, bl.Text, bl.IsError)
		}
	}
	if len(userText) == 0 && len(userImages) == 0 {
		return
	}
	text, pics := userImagesWith(strings.Join(userText, "\n\n"), userImages, b.files)
	if text != "" || len(pics) > 0 {
		b.add(ChatItem{Kind: "user", Text: text, Images: pics})
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
func parseCodex(lines [][]byte) []ChatItem { return parseCodexWith(lines, images.addFile) }

// parseCodexWith: parseCodex, with pictures named by path found by files.
func parseCodexWith(lines [][]byte, files func(string) string) []ChatItem {
	b := newChatBuilder()
	b.files = files
	seen := map[string]int{}
	for _, l := range lines {
		// Codex's lines have no ids: an item's is a hash of the line it starts at.
		h := fnv.New64a()
		h.Write(l)
		b.cur = fmt.Sprintf("x%016x", h.Sum64())
		if n := seen[b.cur]; n > 0 {
			seen[b.cur]++
			b.cur += "-" + strconv.Itoa(n)
		} else {
			seen[b.cur] = 1
		}
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
				b.result(it.CallID, firstLines(out, 3, 240), false)
			}
		}
	}
	return b.items
}
