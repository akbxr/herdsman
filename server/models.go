package main

// The model and thinking level an agent runs with: what it offers, what a
// running session uses now, and switching it, from the phone.
//
//	        models                     thinking          at start                 live
//	tego    ~/.tego/models.json        off…max           --model p/id --thinking  /model p/id:level, /thinking level
//	        (its own cache of each
//	        provider's list)
//	Claude  the /model picker's list   effort low…max    --model id --effort      /model id, /effort level
//	Codex   its presets (gpt-5)        reasoning         -m id -c model_reasoning_effort=level
//	                                   minimal…high      (a running Codex 0.29 has no /model <id>)
//
// What a running session uses now: tego shows both on its status line
// (" ◆ Opus 5.5 · ~ · … · ✦ high"; no ✦ when thinking is off); Claude Code
// writes the model of each answer in its transcript (and "Set model to …"
// after /model) and shows its effort above the input box ("● high ·
// /effort") for a while after a change, else its transcript, banner,
// flags or settings say. Both agents keep what /model and /thinking (/effort) pick as
// their default for new sessions too.
//
// Commands are typed with hc send only while the agent is idle or done: a
// working agent would take "/model …" as the next message (or not at all),
// one waiting on a question would type it into the question.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ModelInfo is a model an agent can run.
type ModelInfo struct {
	ID    string `json:"id"`              // what its flag and command take
	Label string `json:"label"`           // "Opus 5.5"
	Group string `json:"group,omitempty"` // the provider, when there are several
}

// LevelInfo is a thinking (effort, reasoning) level.
type LevelInfo struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
}

// ModelChoices is what GET /api/models answers for one agent.
type ModelChoices struct {
	Agent        string      `json:"agent"`
	Models       []ModelInfo `json:"models"`
	Thinking     []LevelInfo `json:"thinking"`
	ThinkingName string      `json:"thinkingName"` // what the agent calls it: Thinking, Effort, Reasoning
	Live         bool        `json:"live"`         // a running session can switch
	Note         string      `json:"note,omitempty"`
	Supported    bool        `json:"supported"` // false: the terminal runs on another machine
}

// What the phone hears about switching models of a terminal on another
// machine: its agents' settings and model lists are there.
var remoteModels = map[string]any{"supported": false, "why": "Models are switched on this Mac only."}

var tegoLevels = []LevelInfo{
	{"off", "Off", ""},
	{"low", "Low", ""},
	{"medium", "Medium", ""},
	{"high", "High", ""},
	{"xhigh", "Extra high", "Long agentic work; newest models only"},
	{"max", "Max", "No limit on thinking; newest models only"},
}

var claudeLevels = []LevelInfo{
	{"low", "Low", "Quick and straightforward"},
	{"medium", "Medium", ""},
	{"high", "High", ""},
	{"xhigh", "Extra high", ""},
	{"max", "Max", "May use many tokens; for the hardest tasks"},
}

var codexLevels = []LevelInfo{
	{"minimal", "Minimal", "Fastest, little reasoning"},
	{"low", "Low", ""},
	{"medium", "Medium", ""},
	{"high", "High", ""},
}

// Claude Code's /model picker (2.1.316), in its order. "default" is the
// account's default model (no --model at start).
var claudeModels = []string{
	"default", "claude-opus-5-5", "claude-fable-5-1", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-haiku-4-5",
	"claude-sonnet-5", "claude-opus-5", "claude-fable-5", "claude-opus-4-8", "claude-opus-4-7", "claude-opus-4-6", "claude-sonnet-4-6",
}

// Codex 0.29's presets are gpt-5 at four reasoning levels.
var codexModels = []string{"gpt-5"}

// A model id or level goes into argv and into a typed slash command: one
// word of these characters, never a space, quote or newline.
var choiceRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/\[\]-]{0,99}$`)

var claudeNameRe = regexp.MustCompile(`^claude-(opus|sonnet|haiku|fable)-(\d+)(?:-(\d{1,2}))?(?:-\d{8})?$`)

// modelLabel: "claude-sonnet-5-5" as "Sonnet 5.5" (as tego shows it); other ids as they are.
func modelLabel(id string) string {
	if id == "default" {
		return "Default"
	}
	m := claudeNameRe.FindStringSubmatch(id)
	if m == nil {
		return id
	}
	v := m[2]
	if m[3] != "" {
		v += "." + m[3]
	}
	return strings.ToUpper(m[1][:1]) + m[1][1:] + " " + v
}

func (s *server) homeDir() string {
	if s.home != "" {
		return s.home
	}
	home, _ := os.UserHomeDir()
	return home
}

// tegoDir: where tego keeps its config (TEGO_HOME overrides ~/.tego).
func (s *server) tegoDir() string {
	if s.home == "" {
		if d := os.Getenv("TEGO_HOME"); d != "" {
			return d
		}
	}
	return filepath.Join(s.homeDir(), ".tego")
}

// The choices, kept a minute: the phone asks each time a sheet opens.
var choicesCache = struct {
	sync.Mutex
	m map[string]choicesEntry
}{m: map[string]choicesEntry{}}

type choicesEntry struct {
	at time.Time
	c  *ModelChoices
}

const choicesTTL = time.Minute

// modelChoices: what an agent offers; nil for an agent it knows nothing about.
func (s *server) modelChoices(agent string) *ModelChoices {
	key := s.homeDir() + "\x00" + agent
	choicesCache.Lock()
	e, ok := choicesCache.m[key]
	choicesCache.Unlock()
	if ok && time.Since(e.at) < choicesTTL {
		return e.c
	}
	var c *ModelChoices
	switch agent {
	case "tego":
		c = &ModelChoices{Agent: agent, Models: s.tegoModels(), Thinking: tegoLevels, ThinkingName: "Thinking", Live: true,
			Note: "tego keeps what you pick as its default for new sessions too."}
	case "claude":
		c = &ModelChoices{Agent: agent, Models: s.claudeModelList(), Thinking: claudeLevels, ThinkingName: "Effort", Live: true,
			Note: "Claude Code keeps what you pick as its default for new sessions too."}
	case "codex":
		models := make([]ModelInfo, len(codexModels))
		for i, m := range codexModels {
			models[i] = ModelInfo{ID: m, Label: m}
		}
		c = &ModelChoices{Agent: agent, Models: models, Thinking: codexLevels, ThinkingName: "Reasoning",
			Note: "Codex takes these when it starts; a running Codex can't switch from the phone."}
	default:
		return nil
	}
	choicesCache.Lock()
	choicesCache.m[key] = choicesEntry{time.Now(), c}
	choicesCache.Unlock()
	return c
}

// tegoModels: the lists tego fetched from each provider (its models.json,
// refreshed when its /model picker opens), the provider of its default
// model first. Its default model is in, listed or not.
func (s *server) tegoModels() []ModelInfo {
	dir := s.tegoDir()
	var cache map[string]struct {
		Models []string `json:"models"`
	}
	if b, err := os.ReadFile(filepath.Join(dir, "models.json")); err == nil {
		json.Unmarshal(b, &cache)
	}
	var cfg struct{ Model string }
	if b, err := os.ReadFile(filepath.Join(dir, "config.json")); err == nil {
		json.Unmarshal(b, &cfg)
	}
	first, _, _ := strings.Cut(cfg.Model, "/")
	providers := make([]string, 0, len(cache))
	for p := range cache {
		providers = append(providers, p)
	}
	sort.Slice(providers, func(i, j int) bool {
		if (providers[i] == first) != (providers[j] == first) {
			return providers[i] == first
		}
		return providers[i] < providers[j]
	})
	var out []ModelInfo
	seen := map[string]bool{}
	for _, p := range providers {
		for _, id := range cache[p].Models {
			spec := p + "/" + id
			if !choiceRe.MatchString(spec) || seen[spec] {
				continue
			}
			seen[spec] = true
			out = append(out, ModelInfo{ID: spec, Label: modelLabel(id), Group: p})
		}
	}
	if p, id, ok := strings.Cut(cfg.Model, "/"); ok && !seen[cfg.Model] && choiceRe.MatchString(cfg.Model) {
		out = append([]ModelInfo{{ID: cfg.Model, Label: modelLabel(id), Group: p}}, out...)
	}
	if out == nil {
		out = []ModelInfo{}
	}
	return out
}

// claudeModelList: the picker's models, then Anthropic models tego knows of
// that the picker doesn't list (newer ones, when Claude Code is older).
func (s *server) claudeModelList() []ModelInfo {
	var out []ModelInfo
	labels := map[string]bool{}
	for _, id := range claudeModels {
		out = append(out, ModelInfo{ID: id, Label: modelLabel(id)})
		labels[modelLabel(id)] = true
	}
	var cache map[string]struct {
		Models []string `json:"models"`
	}
	if b, err := os.ReadFile(filepath.Join(s.tegoDir(), "models.json")); err == nil {
		json.Unmarshal(b, &cache)
	}
	for _, id := range cache["anthropic"].Models {
		if l := modelLabel(id); claudeNameRe.MatchString(id) && !labels[l] {
			labels[l] = true
			out = append(out, ModelInfo{ID: id, Label: l})
		}
	}
	return out
}

func (c *ModelChoices) hasModel(id string) bool {
	for _, m := range c.Models {
		if m.ID == id {
			return true
		}
	}
	return false
}

func (c *ModelChoices) hasLevel(id string) bool {
	for _, l := range c.Thinking {
		if l.ID == id {
			return true
		}
	}
	return false
}

// labelOf: the label of a model id, or its id.
func (c *ModelChoices) labelOf(id string) string {
	for _, m := range c.Models {
		if m.ID == id {
			return m.Label
		}
	}
	return modelLabel(id)
}

// byLabel: the id of the model with that label; prefer, when it is one of
// several (tego: the same model from two providers).
func (c *ModelChoices) byLabel(label, prefer string) string {
	found := ""
	for _, m := range c.Models {
		if m.Label != label {
			continue
		}
		if m.ID == prefer {
			return m.ID
		}
		if found == "" {
			found = m.ID
		}
	}
	return found
}

// check: an error saying what is wrong with a model and level for this agent.
func (c *ModelChoices) check(model, thinking string) error {
	name := agentName(c.Agent)
	if model != "" && (!choiceRe.MatchString(model) || !c.hasModel(model)) {
		return fmt.Errorf("%s doesn't offer the model %q", name, model)
	}
	if thinking != "" && (!choiceRe.MatchString(thinking) || !c.hasLevel(thinking)) {
		return fmt.Errorf("%s has no %s level %q", name, strings.ToLower(c.ThinkingName), thinking)
	}
	return nil
}

func agentName(agent string) string {
	switch agent {
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "":
		return "This program"
	}
	return agent
}

// startFlags: the flags that start an agent with a model and level ("": its default).
func startFlags(agent, model, thinking string) []string {
	var f []string
	switch agent {
	case "tego":
		if model != "" {
			f = append(f, "--model", model)
		}
		if thinking != "" {
			f = append(f, "--thinking", thinking)
		}
	case "claude":
		if model != "" && model != "default" {
			f = append(f, "--model", model)
		}
		if thinking != "" {
			f = append(f, "--effort", thinking)
		}
	case "codex":
		if model != "" {
			f = append(f, "-m", model)
		}
		if thinking != "" {
			f = append(f, "-c", "model_reasoning_effort="+thinking)
		}
	}
	return f
}

// GET /api/models?agent=tego: the models and levels it offers.
func (s *server) modelsHandler(w http.ResponseWriter, r *http.Request) {
	if t := r.URL.Query().Get("terminal"); t != "" {
		if !validTerminal(t) {
			writeErr(w, http.StatusBadRequest, errors.New("bad terminal"))
			return
		}
		if m, _ := splitTerminal(t); m != "" {
			writeJSON(w, remoteModels)
			return
		}
	}
	agent := r.URL.Query().Get("agent")
	c := s.modelChoices(agent)
	if c == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("no model choices for %q (tego, claude or codex)", agent))
		return
	}
	reply := *c
	reply.Supported = true
	writeJSON(w, reply)
}

// screenCurrent: what a session's screen says of its model and level (all
// there is for one on another machine).
func screenCurrent(agent, screen string) Current {
	switch agent {
	case "tego":
		if label, thinking, ok := tegoStatus(screen); ok {
			return Current{ModelLabel: label, Thinking: thinking}
		}
	case "claude":
		model, effort := claudeHeader(screen)
		if e := claudeEffort(screen); e != "" {
			effort = e
		}
		return Current{ModelLabel: model, Thinking: effort}
	}
	return Current{}
}

// Current is the model and level a running session uses now ("": not known).
type Current struct {
	Model      string `json:"model,omitempty"`
	ModelLabel string `json:"modelLabel,omitempty"`
	Thinking   string `json:"thinking,omitempty"`
}

// current reads them off the agent's screen and transcript (argv: how it
// was started).
func (s *server) current(agent string, argv []string, screen, transcript string) Current {
	switch agent {
	case "tego":
		label, thinking, ok := tegoStatus(screen)
		if !ok {
			return Current{}
		}
		c := Current{ModelLabel: label, Thinking: thinking}
		if label != "" {
			var cfg struct{ Model string }
			if b, err := os.ReadFile(filepath.Join(s.tegoDir(), "config.json")); err == nil {
				json.Unmarshal(b, &cfg)
			}
			if choices := s.modelChoices("tego"); choices != nil {
				c.Model = choices.byLabel(label, cfg.Model)
			}
		}
		return c
	case "claude":
		return s.claudeCurrent(argv, screen, transcript)
	}
	return Current{}
}

// tegoStatus reads tego's status line, the last on its screen with "◆ ":
// " ◆ Opus 5.5 · ~ · master · ◔ 32.8%/1.0M · ✦ high · ↑1.2M ↓30k".
// No ✦: thinking is off.
func tegoStatus(screen string) (model, thinking string, ok bool) {
	lines := strings.Split(strings.ReplaceAll(screen, "\r", ""), "\n")
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-12; i-- {
		if !strings.Contains(lines[i], "◆ ") {
			continue
		}
		thinking = "off"
		for _, part := range strings.Split(strings.TrimSpace(lines[i]), " · ") {
			part = strings.TrimSpace(part)
			if v, found := strings.CutPrefix(part, "◆ "); found && model == "" {
				model = strings.TrimSpace(v)
			} else if v, found := strings.CutPrefix(part, "✦ "); found {
				thinking = strings.TrimSpace(v)
			}
		}
		return model, thinking, model != ""
	}
	return "", "", false
}

var (
	claudeEffortRe = regexp.MustCompile(`\b(low|medium|high|xhigh|max) · /effort\b`)
	claudeHeaderRe = regexp.MustCompile(`(?m)^\S*\s+(\S.*?) with (low|medium|high|xhigh|max) effort\b`)
	claudeSetRe    = regexp.MustCompile("(?:Set model to|Kept model as) `([^`]+)`")
	claudeLevelRe  = regexp.MustCompile(`Set effort level to (low|medium|high|xhigh|max)\b`)
)

// claudeCurrent: Claude Code's model and effort, from the freshest place
// that says:
//
//   - model: the transcript (the model of its last answer, or a later
//     /model's "Set model to `Sonnet 5.5`"), the banner of a new session
//     ("Opus 5.5 with high effort"), its --model, its settings' model.
//   - effort: the "● high · /effort" it shows above the input box for a
//     while after a change, the transcript ("Set effort level to low"), the
//     banner, its --effort, its settings for that model.
func (s *server) claudeCurrent(argv []string, screen, transcript string) Current {
	tr := claudeTranscript(transcript)
	headModel, headEffort := claudeHeader(screen)
	var settings struct {
		Model         string
		EffortLevel   string
		ModelSettings map[string]struct{ EffortLevel string }
	}
	if b, err := os.ReadFile(filepath.Join(s.homeDir(), ".claude", "settings.json")); err == nil {
		json.Unmarshal(b, &settings)
	}
	choices := s.modelChoices("claude")
	c := Current{Model: tr.model, ModelLabel: tr.label}
	if c.Model == "" && c.ModelLabel == "" {
		c.ModelLabel = headModel
	}
	if c.Model == "" && c.ModelLabel == "" {
		c.Model = flagValue(argv, "--model")
	}
	if c.Model == "" && c.ModelLabel == "" && choiceRe.MatchString(settings.Model) {
		c.Model = settings.Model
	}
	if c.Model == "" && c.ModelLabel != "" {
		c.Model = choices.byLabel(c.ModelLabel, "")
	}
	if c.ModelLabel == "" && c.Model != "" {
		c.ModelLabel = choices.labelOf(c.Model)
	}
	// The model the effort setting is kept for: "default" is a real one.
	id := c.Model
	if id == "default" || !strings.HasPrefix(id, "claude-") {
		id = choices.byLabel(c.ModelLabel, "")
	}
	for _, e := range []string{claudeEffort(screen), tr.effort, headEffort, flagValue(argv, "--effort"), settings.ModelSettings[id].EffortLevel, settings.EffortLevel} {
		if e != "" && choices.hasLevel(e) {
			c.Thinking = e
			break
		}
	}
	return c
}

// flagValue: the value after a flag in argv ("--model x" or "--model=x").
func flagValue(argv []string, flag string) string {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			return argv[i+1]
		}
		if v, ok := strings.CutPrefix(a, flag+"="); ok {
			return v
		}
	}
	return ""
}

// claudeEffort: the effort Claude Code shows above its input box for a
// while after it starts or changes ("● high · /effort").
func claudeEffort(screen string) string {
	if m := claudeEffortRe.FindAllStringSubmatch(screen, -1); len(m) > 0 {
		return m[len(m)-1][1]
	}
	return ""
}

// claudeHeader: the model and effort in the banner at the top of a new
// session ("Opus 5.5 with high effort · Claude Team"), while it is on screen.
func claudeHeader(screen string) (model, effort string) {
	if m := claudeHeaderRe.FindStringSubmatch(screen); m != nil {
		return strings.TrimSpace(m[1]), m[2]
	}
	return "", ""
}

// What a Claude Code transcript says, until the file changes.
type claudeSaid struct {
	model, label, effort string
}

var claudeSaidCache = struct {
	sync.Mutex
	m map[string]claudeSaidEntry
}{m: map[string]claudeSaidEntry{}}

type claudeSaidEntry struct {
	size  int64
	mtime time.Time
	said  claudeSaid
}

// claudeTranscript: the model and effort of a Claude Code session, from the
// end of its transcript: the latest of an answer's model and a /model's
// "Set model to `Sonnet 5.5`" (a switch to the default model says "`Opus
// 5.5 (default)`": that is "default"; model is "" when only the label is
// known), and the latest /effort's "Set effort level to low".
func claudeTranscript(path string) claudeSaid {
	if path == "" {
		return claudeSaid{}
	}
	st, err := os.Stat(path)
	if err != nil {
		return claudeSaid{}
	}
	claudeSaidCache.Lock()
	e, ok := claudeSaidCache.m[path]
	claudeSaidCache.Unlock()
	if ok && e.size == st.Size() && e.mtime.Equal(st.ModTime()) {
		return e.said
	}
	lines, err := tailLines(path, 512<<10)
	if err != nil {
		return claudeSaid{}
	}
	var said claudeSaid
	for i := len(lines) - 1; i >= 0 && (said.label == "" || said.effort == ""); i-- {
		id, label, effort := claudeLine(lines[i])
		if said.label == "" && label != "" {
			said.model, said.label = id, label
		}
		if said.effort == "" {
			said.effort = effort
		}
	}
	claudeSaidCache.Lock()
	if len(claudeSaidCache.m) > 64 {
		clear(claudeSaidCache.m)
	}
	claudeSaidCache.m[path] = claudeSaidEntry{st.Size(), st.ModTime(), said}
	claudeSaidCache.Unlock()
	return said
}

// claudeLine: the model or effort one transcript line says is in use, if any.
func claudeLine(l []byte) (id, label, effort string) {
	var e struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
		Message *struct {
			Model   string          `json:"model"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		IsSidechain bool `json:"isSidechain"`
	}
	if json.Unmarshal(l, &e) != nil || e.IsSidechain {
		return
	}
	if e.Type == "assistant" && e.Message != nil && strings.HasPrefix(e.Message.Model, "claude-") {
		return e.Message.Model, modelLabel(e.Message.Model), ""
	}
	var text string
	if e.Message != nil && len(e.Message.Content) > 0 && e.Message.Content[0] == '"' {
		json.Unmarshal(e.Message.Content, &text)
	} else if len(e.Content) > 0 && e.Content[0] == '"' {
		json.Unmarshal(e.Content, &text)
	}
	if !strings.Contains(text, "<local-command-stdout>") {
		return
	}
	if m := claudeLevelRe.FindStringSubmatch(text); m != nil {
		effort = m[1]
	}
	if m := claudeSetRe.FindStringSubmatch(text); m != nil {
		label = m[1]
		if l, ok := strings.CutSuffix(label, " (default)"); ok {
			id, label = "default", l
		}
	}
	return
}

// terminalNow is what runs in a terminal and the state it reports, per hc
// (or the herdr CLI).
type terminalNow struct {
	fg    foregroundInfo
	state string // idle, working, blocked, done, error; "" when it says nothing
}

func (s *server) terminalNow(ctx context.Context, terminal string) (terminalNow, error) {
	return s.backend(terminal).now(ctx, terminal)
}

// screenText: what the terminal shows now.
func (s *server) screenText(ctx context.Context, terminal string) (string, error) {
	return s.backend(terminal).screen(ctx, terminal, 3*time.Second)
}

// Variables for the tests.
var (
	switchConfirmWait = 1500 * time.Millisecond // how long a Claude "Switch model?" dialog is looked for
	switchPoll        = 200 * time.Millisecond
	switchSettle      = 400 * time.Millisecond // before reading the new values back
)

// POST /api/session {terminal, agent?, model?, thinking?}: switches the
// model and/or thinking level of the agent running there, with its own
// slash commands. agent: the one the phone expects there.
func (s *server) session(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Terminal string `json:"terminal"`
		Agent    string `json:"agent"`
		Model    string `json:"model"`
		Thinking string `json:"thinking"`
	}
	if err := decode(r, &req); err != nil || !validTerminal(req.Terminal) || (req.Model == "" && req.Thinking == "") {
		writeErr(w, http.StatusBadRequest, errors.New("need {terminal, model and/or thinking}"))
		return
	}
	if m, _ := splitTerminal(req.Terminal); m != "" {
		writeJSON(w, remoteModels) // nothing sent
		return
	}
	now, err := s.terminalNow(r.Context(), req.Terminal)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	agent := agentOf(now.fg)
	if req.Agent != "" && req.Agent != agent {
		writeErr(w, http.StatusConflict, fmt.Errorf("%s no longer runs %s", req.Terminal, agentName(req.Agent)))
		return
	}
	choices := s.modelChoices(agent)
	if choices == nil {
		writeErr(w, http.StatusConflict, fmt.Errorf("%s isn't running an agent whose model can be changed", req.Terminal))
		return
	}
	if !choices.Live {
		writeErr(w, http.StatusConflict, fmt.Errorf("%s can't switch its model while it runs: start a new task with the model you want", agentName(agent)))
		return
	}
	if err := choices.check(req.Model, req.Thinking); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	state := now.state
	if s.events != nil && state == "" {
		s.events.mu.Lock()
		if cur, ok := s.events.current[req.Terminal]; ok {
			state = cur.State
		}
		s.events.mu.Unlock()
	}
	switch state {
	case "working":
		writeErr(w, http.StatusConflict, fmt.Errorf("%s is working; change the model when it's done", agentName(agent)))
		return
	case "blocked":
		writeErr(w, http.StatusConflict, fmt.Errorf("%s is waiting for you; answer it first", agentName(agent)))
		return
	}

	var cmds []string
	switch agent {
	case "tego":
		if req.Model != "" {
			// "/model spec" alone opens tego's thinking picker: the level goes with it.
			level := req.Thinking
			if level == "" {
				screen, _ := s.screenText(r.Context(), req.Terminal)
				_, level, _ = tegoStatus(screen)
			}
			if level == "" {
				writeErr(w, http.StatusConflict, errors.New("couldn't read tego's thinking level from its screen; pick one with the model"))
				return
			}
			cmds = append(cmds, "/model "+req.Model+":"+level)
		} else {
			cmds = append(cmds, "/thinking "+req.Thinking)
		}
	case "claude":
		if req.Model != "" {
			cmds = append(cmds, "/model "+req.Model)
		}
		if req.Thinking != "" {
			cmds = append(cmds, "/effort "+req.Thinking)
		}
	}
	for _, c := range cmds {
		if _, err := s.backend(req.Terminal).send(r.Context(), req.Terminal, c, false); err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		if agent == "claude" && strings.HasPrefix(c, "/model ") {
			s.confirmSwitch(r.Context(), req.Terminal)
		}
	}
	time.Sleep(switchSettle)
	screen, _ := s.screenText(r.Context(), req.Terminal)
	transcript := ""
	if agent == "claude" {
		_, transcript = findTranscript(now.fg)
	}
	writeJSON(w, map[string]any{"agent": agent, "sent": cmds, "current": s.current(agent, now.fg.Argv, screen, transcript)})
}

// confirmSwitch: Claude Code asks "Switch model?" when the conversation is
// cached for the old model; the switch was asked for, so Enter takes the
// selected "Yes, switch to …". Nothing else on the screen is answered.
func (s *server) confirmSwitch(ctx context.Context, terminal string) {
	for end := time.Now().Add(switchConfirmWait); time.Now().Before(end); time.Sleep(switchPoll) {
		screen, err := s.screenText(ctx, terminal)
		if err != nil {
			return
		}
		p := parsePrompt(screen)
		if p == nil || !strings.Contains(p.Question, "Switch model") || p.Selected >= len(p.Options) {
			continue
		}
		if strings.HasPrefix(p.Options[p.Selected], "Yes, switch to") {
			s.backend(terminal).keys(ctx, terminal, []string{"enter"}, 10*time.Second)
		}
		return
	}
}
