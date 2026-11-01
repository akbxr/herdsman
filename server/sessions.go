package main

// Earlier sessions, for the phone to pick one up again: the agents'
// transcripts, newest first, and a new tab that resumes one.
//
// Where they are and what they are called:
//   tego         <tego dir>/sessions/<cwd, / as ->/<YYYYMMDD-HHMMSS-hex>.jsonl; a header
//                line names the cwd, a "title" entry the title (generated after
//                the first message; /rename writes another, the last wins).
//                Resumed with `tego --resume <id>`.
//   Claude Code  ~/.claude/projects/<cwd, non-alphanumerics as ->/<uuid>.jsonl; every
//                entry carries the cwd, "ai-title" entries the generated title,
//                "custom-title" the one you gave it (/rename), "summary" older
//                versions' summaries. Resumed with `claude --resume <uuid>`.
// The title falls back to the first message you wrote. A session without one
// (opened and left) is not offered.
//
// Codex is not offered: the Codex installed here (0.29) has no resume
// command, only an experimental config key that can't be tried without an
// account, and its rollouts can't be checked to come back right.
//
// Transcripts grow to hundreds of MB: only the first 256 KB and the last
// 64 KB are read for the title and folder (the title and first message are
// near the start, a rename at the end), and kept until the file changes;
// the scan gives up after a fixed time with what it found. Messages in long
// transcripts are counted for a short while per request, going on where the
// last count stopped (they only grow), so the counts are exact after a few
// requests and "at least" until then.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Session is an earlier conversation an agent can pick up again.
type Session struct {
	Agent    string `json:"agent"` // tego, claude
	ID       string `json:"id"`
	Title    string `json:"title"` // the agent's title, else the first message you wrote
	Cwd      string `json:"cwd"`
	Time     int64  `json:"time"`               // unix ms: last written
	Messages int    `json:"messages"`           // messages you wrote in it
	Estimate bool   `json:"estimate,omitempty"` // not counted to the end yet: at least Messages
	Size     int64  `json:"size"`
	Running  string `json:"running,omitempty"` // the terminal it runs in now: open that, don't resume it twice

	path  string
	named bool // Title is the agent's, not your first message
}

const (
	sessionsMax     = 50
	sessionsMaxAsk  = 200
	sessionHead     = 256 << 10
	sessionTail     = 64 << 10
	sessionTitleMax = 100
)

var (
	sessionScan  = 1500 * time.Millisecond // the scan stops here, with what it found (variables for the tests)
	sessionCount = 400 * time.Millisecond  // at most this long counting the messages in long transcripts

	tegoIDRe   = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{4,16}$`)
	claudeIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	hcFromRe   = regexp.MustCompile(`^<hc from="[^"]*">\s*`)
	skillRe    = regexp.MustCompile(`(?s)^<skill name="([^"]*)"[^>]*>.*?</skill>\s*`)
)

// resumable: the agents a session can be resumed with.
var resumable = []string{"tego", "claude"}

func validSessionID(agent, id string) bool {
	switch agent {
	case "tego":
		return tegoIDRe.MatchString(id)
	case "claude":
		return claudeIDRe.MatchString(id)
	}
	return false
}

// sessionFile is a transcript on disk, before it is read.
type sessionFile struct {
	agent, id, path string
	mtime           time.Time
	size            int64
}

// sessionRoot: the folder of folders an agent keeps its transcripts in.
func (s *server) sessionRoot(agent string) string {
	switch agent {
	case "tego":
		return filepath.Join(s.tegoDir(), "sessions")
	case "claude":
		return filepath.Join(s.homeDir(), ".claude", "projects")
	}
	return ""
}

// tegoSlug: tego's folder for a cwd (internal/session.Dir).
func tegoSlug(cwd string) string {
	return strings.Trim(strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' {
			return '-'
		}
		return r
	}, cwd), "-")
}

// sessionDirs: the folders that may hold an agent's sessions for cwd (every
// folder when cwd is ""). Both spellings: the agents resolve symlinks
// (/tmp is /private/tmp), not always the one asking.
func (s *server) sessionDirs(agent, cwd string) []string {
	root := s.sessionRoot(agent)
	if cwd == "" {
		return subdirs(root)
	}
	spellings := []string{cwd}
	if real, err := filepath.EvalSymlinks(cwd); err == nil && real != cwd {
		spellings = append(spellings, real)
	}
	var dirs []string
	seen := map[string]bool{}
	for _, c := range spellings {
		slug := tegoSlug(c)
		if agent == "claude" {
			slug = claudeSlug(c)
		}
		if d := filepath.Join(root, slug); !seen[d] && isDir(d) {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// sessionFiles: an agent's transcripts in those folders.
func sessionFiles(agent string, dirs []string) []sessionFile {
	var out []sessionFile
	for _, d := range dirs {
		ents, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range ents {
			id, ok := strings.CutSuffix(e.Name(), ".jsonl")
			if !ok || e.IsDir() || !validSessionID(agent, id) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, sessionFile{agent, id, filepath.Join(d, e.Name()), info.ModTime(), info.Size()})
		}
	}
	return out
}

// findSession: the transcript of an agent's session by id ("" when there is none).
func (s *server) findSession(agent, id string) (sessionFile, bool) {
	if !validSessionID(agent, id) { // no path of its own: "../x", "a/b", "*"
		return sessionFile{}, false
	}
	files := sessionFiles(agent, subdirs(s.sessionRoot(agent)))
	var best sessionFile
	found := false
	for _, f := range files {
		if f.id == id && (!found || f.mtime.After(best.mtime)) {
			best, found = f, true
		}
	}
	return best, found
}

// sessionHeadInfo: what the start and end of a transcript say.
type sessionHeadInfo struct {
	title, first, cwd string
	prompts           int
	estimate          bool
}

// The heads read, until their file changes.
var headCache = struct {
	sync.Mutex
	m map[string]headCacheEntry
}{m: map[string]headCacheEntry{}}

type headCacheEntry struct {
	size  int64
	mtime time.Time
	info  sessionHeadInfo
}

func readSessionHead(f sessionFile) sessionHeadInfo {
	headCache.Lock()
	c, ok := headCache.m[f.path]
	headCache.Unlock()
	if ok && c.size == f.size && c.mtime.Equal(f.mtime) {
		return c.info
	}
	info := parseSessionHead(f.agent, f.path, f.size)
	headCache.Lock()
	if len(headCache.m) > 2000 {
		clear(headCache.m)
	}
	headCache.m[f.path] = headCacheEntry{f.size, f.mtime, info}
	headCache.Unlock()
	return info
}

// parseSessionHead reads the first sessionHead and last sessionTail bytes
// (the whole file when it is small).
func parseSessionHead(agent, path string, size int64) sessionHeadInfo {
	var info sessionHeadInfo
	file, err := os.Open(path)
	if err != nil {
		return info
	}
	defer file.Close()
	whole := size <= sessionHead+sessionTail
	n := min(size, sessionHead)
	if whole {
		n = size
	}
	head := make([]byte, n)
	if k, _ := file.ReadAt(head, 0); int64(k) < n {
		head = head[:k]
	}
	headLines := bytes.Split(head, []byte{'\n'})
	if !whole && len(headLines) > 0 {
		headLines = headLines[:len(headLines)-1] // cut in the middle
	}
	info.cwd = cwdIn(head)
	var titles sessionTitles
	for _, l := range headLines {
		if p := titles.line(agent, l); p != "" {
			info.prompts++
			if info.first == "" {
				info.first = p
			}
		}
	}
	if !whole {
		info.estimate = true // the middle is not read: at least this many
		tail := make([]byte, sessionTail)
		k, _ := file.ReadAt(tail, size-sessionTail)
		tail = tail[:k]
		if i := bytes.IndexByte(tail, '\n'); i >= 0 {
			tail = tail[i+1:] // cut in the middle
		}
		for _, l := range bytes.Split(tail, []byte{'\n'}) {
			if titles.line(agent, l) != "" {
				info.prompts++
			}
		}
	}
	info.title = titles.best()
	return info
}

// The messages counted in long transcripts: how far, how many. Transcripts
// only grow, so a count goes on from where it stopped, in this request or
// the next.
var promptCounts = struct {
	sync.Mutex
	m map[string]promptCount
}{m: map[string]promptCount{}}

type promptCount struct {
	offset   int64 // counted up to here: the start of a line
	count    int
	skipping bool          // offset is in a line longer than a chunk
	titles   sessionTitles // the titles on the way (older tego titled late)
}

var countChunk = 4 << 20 // a variable for the tests

// countPrompts counts the messages you wrote in a whole transcript, until
// the time is up, and finds its title on the way; done when it got to the end.
func countPrompts(f sessionFile, until time.Time) (n int, title string, done bool) {
	promptCounts.Lock()
	e := promptCounts.m[f.path]
	promptCounts.Unlock()
	if f.size < e.offset { // rewritten
		e = promptCount{}
	}
	defer func() {
		promptCounts.Lock()
		if len(promptCounts.m) > 2000 {
			clear(promptCounts.m)
		}
		promptCounts.m[f.path] = e
		promptCounts.Unlock()
	}()
	file, err := os.Open(f.path)
	if err != nil {
		return e.count, e.titles.best(), false
	}
	defer file.Close()
	buf := make([]byte, min(int64(countChunk), max(f.size-e.offset, 1)))
	for {
		if e.offset >= f.size {
			return e.count, e.titles.best(), true
		}
		if time.Now().After(until) {
			return e.count, e.titles.best(), false
		}
		k, _ := file.ReadAt(buf[:min(int64(len(buf)), f.size-e.offset)], e.offset)
		if k == 0 {
			return e.count, e.titles.best(), false
		}
		chunk := buf[:k]
		end := bytes.LastIndexByte(chunk, '\n')
		if end < 0 {
			if e.offset+int64(k) >= f.size {
				return e.count, e.titles.best(), true // the rest is the line being written
			}
			// A line longer than a chunk (pasted images): judged by its start.
			if !e.skipping && promptLine(f.agent, chunk) {
				e.count++
			}
			e.skipping = true
			e.offset += int64(k)
			continue
		}
		lines := chunk[:end]
		if e.skipping { // the end of that long line
			i := bytes.IndexByte(chunk, '\n')
			lines = chunk[min(i+1, end):end]
			e.skipping = false
		}
		for len(lines) > 0 {
			l := lines
			if i := bytes.IndexByte(lines, '\n'); i >= 0 {
				l, lines = lines[:i], lines[i+1:]
			} else {
				lines = nil
			}
			if promptLine(f.agent, l) {
				if e.titles.line(f.agent, l) != "" {
					e.count++
				}
			} else if titleLine(f.agent, l) {
				e.titles.line(f.agent, l)
			}
		}
		e.offset += int64(end + 1)
	}
}

// promptLine: whether a transcript line may be a message you wrote, without
// decoding it (tool results and the agents' own lines are most of the bytes).
func promptLine(agent string, l []byte) bool {
	if agent == "claude" {
		return bytes.Contains(l, []byte(`"type":"user"`)) &&
			(bytes.Contains(l, []byte(`"role":"user","content":"`)) || bytes.Contains(l, []byte(`"role":"user","content":[{"type":"text"`)))
	}
	return bytes.Contains(l, []byte(`"role":"user","content":[{"type":"text"`))
}

// titleLine: whether a transcript line may be a title (they start with their type).
func titleLine(agent string, l []byte) bool {
	if agent == "claude" {
		return bytes.HasPrefix(l, []byte(`{"type":"ai-title"`)) || bytes.HasPrefix(l, []byte(`{"type":"custom-title"`)) || bytes.HasPrefix(l, []byte(`{"type":"summary"`))
	}
	return bytes.HasPrefix(l, []byte(`{"type":"title"`))
}

// sessionTitles: the titles a transcript gives itself, as its lines go by.
type sessionTitles struct{ custom, ai, summary string }

func (t *sessionTitles) best() string {
	for _, s := range []string{t.custom, t.ai, t.summary} {
		if s != "" {
			return s
		}
	}
	return ""
}

// line takes in one transcript line: a title is kept (the last of each
// kind wins); a message you wrote comes back as its text.
func (t *sessionTitles) line(agent string, l []byte) string {
	l = bytes.TrimSpace(l)
	if len(l) == 0 {
		return ""
	}
	// Most lines are neither: don't decode what can't be one.
	markers := [][]byte{[]byte(`"type":"message"`), []byte(`"type":"title"`)}
	if agent == "claude" {
		markers = [][]byte{[]byte(`"type":"user"`), []byte(`"type":"ai-title"`), []byte(`"type":"custom-title"`), []byte(`"type":"summary"`)}
	}
	hit := false
	for _, m := range markers {
		if bytes.Contains(l, m) {
			hit = true
			break
		}
	}
	if !hit {
		return ""
	}
	var e struct {
		Type        string   `json:"type"`
		Title       string   `json:"title"`
		AITitle     string   `json:"aiTitle"`
		CustomTitle string   `json:"customTitle"`
		Summary     string   `json:"summary"`
		IsMeta      bool     `json:"isMeta"`
		IsSidechain bool     `json:"isSidechain"`
		Message     *message `json:"message"`
	}
	if json.Unmarshal(l, &e) != nil {
		return ""
	}
	switch e.Type {
	case "title":
		if s := cleanTitle(e.Title); s != "" {
			t.ai = s
		}
	case "ai-title":
		if s := cleanTitle(e.AITitle); s != "" {
			t.ai = s
		}
	case "custom-title":
		if s := cleanTitle(e.CustomTitle); s != "" {
			t.custom = s
		}
	case "summary":
		if s := cleanTitle(e.Summary); s != "" {
			t.summary = s
		}
	case "message", "user":
		if e.Message == nil || e.Message.Role != "user" || e.IsMeta || e.IsSidechain {
			return ""
		}
		return promptText(e.Message.Content)
	}
	return ""
}

// promptText: what you wrote in a user message, one line; "" for tool
// results and what the agent adds for itself.
func promptText(raw json.RawMessage) string {
	var parts []block
	if len(raw) > 0 && raw[0] == '"' {
		var s string
		json.Unmarshal(raw, &s)
		parts = []block{{Type: "text", Text: s}}
	} else if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	for _, b := range parts {
		if b.Type != "text" {
			continue
		}
		t := strings.TrimSpace(b.Text)
		if t == "" || hiddenText(t) || strings.HasPrefix(t, "<bash-") || strings.HasPrefix(t, "<task-notification>") {
			continue
		}
		// A skill you invoked comes first, its whole SKILL.md: your words follow it.
		skill := ""
		for {
			m := skillRe.FindStringSubmatch(t)
			if m == nil {
				break
			}
			skill, t = m[1], t[len(m[0]):]
		}
		if t == "" && skill != "" {
			t = "/" + skill
		}
		if s := cleanTitle(t); s != "" {
			return s
		}
	}
	return ""
}

// cleanTitle: one line, without hc's sender tag, at most sessionTitleMax runes.
func cleanTitle(s string) string {
	s = strings.TrimSpace(s)
	if m := hcFromRe.FindString(s); m != "" {
		s = strings.TrimSuffix(strings.TrimSpace(s[len(m):]), "</hc>")
	}
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > sessionTitleMax {
		s = strings.TrimSpace(string(r[:sessionTitleMax-1])) + "…"
	}
	return s
}

// sameDir: two spellings of one folder.
func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ia, oka := idOf(a)
	ib, okb := idOf(b)
	return oka && okb && ia == ib
}

// resumeAgent: the installed agent that resumes sessions of this kind (its
// argv from programs.json, flags and all); nil when none is.
func (s *server) resumeAgent(kind string) *AgentInfo {
	list := s.agents()
	for _, a := range list {
		if a.ID == kind {
			return &a
		}
	}
	for _, a := range list {
		if agentOf(foregroundInfo{Name: a.ID, Argv: a.Argv}) == kind {
			return &a
		}
	}
	return nil
}

// What /api/sessions says for another machine.
const remoteSessionsNote = "Earlier sessions are listed for this Mac only."

// runningSessions: the transcripts open in live terminals now, by file, and
// the terminal of each.
func (s *server) runningSessions(ctx context.Context) map[fileID]string {
	out := map[fileID]string{}
	ls, err := run(ctx, 3*time.Second, s.hc, "ls", "--json")
	if err != nil {
		return out
	}
	var list struct {
		Terminals []struct{ Address, State string }
	}
	if json.Unmarshal(ls, &list) != nil {
		return out
	}
	transcript := s.transcriptOf
	if transcript == nil {
		transcript = func(ctx context.Context, terminal string) string {
			now, err := s.terminalNow(ctx, terminal)
			if err != nil {
				return ""
			}
			_, path := findTranscript(now.fg)
			return path
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, t := range list.Terminals {
		if (t.State != "" && t.State != "live") || !validTerminal(t.Address) {
			continue
		}
		if m, _ := splitTerminal(t.Address); m != "" {
			continue // on another machine: its files aren't this Mac's
		}
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			p := transcript(ctx, addr)
			if p == "" {
				return
			}
			if id, ok := idOf(p); ok {
				mu.Lock()
				out[id] = addr
				mu.Unlock()
			}
		}(t.Address)
	}
	wg.Wait()
	return out
}

// GET /api/sessions[?cwd=/Users/me/Code/app][&agent=tego][&limit=50]: the
// sessions that can be resumed, newest first: in that folder, or the recent
// ones anywhere (not in temporary folders, not in folders gone since);
// home: the Mac user's home, for showing paths as ~.
func (s *server) sessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if m := q.Get("machine"); m != "" {
		if !validMachine(m) {
			writeErr(w, http.StatusBadRequest, errors.New("bad machine name"))
			return
		}
		// Reading many transcripts there would take a round trip each.
		reply := map[string]any{"sessions": []Session{}, "partial": false, "note": remoteSessionsNote}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if home, err := s.on(m).homeDir(ctx); err == nil {
			reply["home"] = home
		}
		writeJSON(w, reply)
		return
	}
	cwd := q.Get("cwd")
	if cwd != "" {
		cwd = filepath.Clean(cwd)
		if !filepath.IsAbs(cwd) {
			writeErr(w, http.StatusBadRequest, errors.New("the folder must be a full path"))
			return
		}
	}
	agents := resumable
	if a := q.Get("agent"); a != "" {
		if a != "tego" && a != "claude" {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("%s sessions can't be resumed from here (tego or claude)", agentName(a)))
			return
		}
		agents = []string{a}
	}
	limit := sessionsMax
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
		limit = min(n, sessionsMaxAsk)
	}
	list, partial := s.listSessions(r.Context(), cwd, agents, limit)
	writeJSON(w, map[string]any{"home": s.homeDir(), "sessions": list, "partial": partial})
}

// listSessions: newest first, at most limit; partial when the scan ran out of time.
func (s *server) listSessions(ctx context.Context, cwd string, agents []string, limit int) ([]Session, bool) {
	deadline := time.Now().Add(sessionScan)
	running := make(chan map[fileID]string, 1)
	go func() { running <- s.runningSessions(ctx) }()

	var files []sessionFile
	for _, a := range agents {
		if s.resumeAgent(a) == nil {
			continue // not installed: nothing could resume it
		}
		files = append(files, sessionFiles(a, s.sessionDirs(a, cwd))...)
	}
	sort.Slice(files, func(i, j int) bool {
		if !files[i].mtime.Equal(files[j].mtime) {
			return files[i].mtime.After(files[j].mtime)
		}
		return files[i].path < files[j].path
	})

	list := []Session{}
	partial := false
	okDir := map[string]bool{}
	for _, f := range files {
		if len(list) >= limit {
			break
		}
		if time.Now().After(deadline) {
			partial = true
			break
		}
		h := readSessionHead(f)
		if h.prompts == 0 || h.cwd == "" || !filepath.IsAbs(h.cwd) {
			continue // nothing said in it, or nowhere to resume it
		}
		if cwd != "" {
			if !sameDir(h.cwd, cwd) {
				continue
			}
		} else {
			ok, known := okDir[h.cwd]
			if !known {
				ok = !temporary(h.cwd) && isDir(h.cwd)
				okDir[h.cwd] = ok
			}
			if !ok {
				continue
			}
		}
		title, named := h.title, h.title != ""
		if !named {
			title = h.first
		}
		list = append(list, Session{
			Agent: f.agent, ID: f.id, Title: title, Cwd: h.cwd, Time: f.mtime.UnixMilli(),
			Messages: h.prompts, Estimate: h.estimate, Size: f.size, path: f.path, named: named,
		})
	}
	// Long transcripts are counted whole, newest first, for a little while
	// (what is counted is kept); the rest say "at least".
	countUntil := time.Now().Add(sessionCount)
	if deadline.Before(countUntil) {
		countUntil = deadline
	}
	for i := range list {
		if !list[i].Estimate {
			continue
		}
		f := sessionFile{agent: list[i].Agent, path: list[i].path, size: list[i].Size}
		n, title, done := countPrompts(f, countUntil)
		if title != "" && !list[i].named {
			list[i].Title, list[i].named = title, true
		}
		if done {
			list[i].Messages, list[i].Estimate = n, false
		} else {
			list[i].Messages = max(list[i].Messages, n)
		}
	}
	if run := <-running; len(run) > 0 {
		for i := range list {
			if id, ok := idOf(list[i].path); ok {
				list[i].Running = run[id]
			}
		}
	}
	return list, partial
}

// POST /api/resume {"agent": "tego", "id": "20261101-083706-ee31c7",
// "model": "…", "thinking": "high"}: a new tab in the session's folder
// running the agent on it again; model and thinking are optional.
func (s *server) resume(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Agent    string `json:"agent"`
		ID       string `json:"id"`
		Model    string `json:"model"`
		Thinking string `json:"thinking"`
		Machine  string `json:"machine"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("need {agent, id}"))
		return
	}
	if req.Machine != "" {
		writeErr(w, http.StatusBadRequest, errors.New("Earlier sessions are resumed on this Mac only."))
		return
	}
	if req.Agent != "tego" && req.Agent != "claude" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("%s sessions can't be resumed from here (tego or claude)", agentName(req.Agent)))
		return
	}
	if !validSessionID(req.Agent, req.ID) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("%q is not a %s session id", req.ID, agentName(req.Agent)))
		return
	}
	f, ok := s.findSession(req.Agent, req.ID)
	if !ok {
		writeErr(w, http.StatusNotFound, fmt.Errorf("no %s session %s on the Mac", agentName(req.Agent), req.ID))
		return
	}
	h := readSessionHead(f)
	if h.prompts == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("nothing was said in that session: there is nothing to resume"))
		return
	}
	if h.cwd == "" || !filepath.IsAbs(h.cwd) || !isDir(h.cwd) {
		writeErr(w, http.StatusConflict, fmt.Errorf("the session's folder %s is gone from the Mac", h.cwd))
		return
	}
	agent := s.resumeAgent(req.Agent)
	if agent == nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("%s is not installed on the Mac", agentName(req.Agent)))
		return
	}
	if id, ok := idOf(f.path); ok {
		if t := s.runningSessions(r.Context())[id]; t != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": "it runs in " + t + " now: open that", "terminal": t})
			return
		}
	}
	flags, err := s.modelFlags(*agent, req.Model, req.Thinking)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	argv := append(append(append([]string(nil), agent.Argv...), flags...), "--resume", req.ID)
	made, code, err := s.openTab(r.Context(), h.cwd, argv)
	if err != nil {
		writeErr(w, code, err)
		return
	}
	writeJSON(w, map[string]string{"terminal": made.Terminal, "tab": made.Tab, "space": made.Space, "agent": req.Agent, "id": req.ID, "cwd": h.cwd})
}
