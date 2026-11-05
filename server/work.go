package main

// What an agent is working through, read from its transcript beside the
// chat: its todo list and the subagents it started (tego's task tool and
// background tasks, Claude Code's Agent tool, Codex's plan).

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// ChatWork is the latest todo list and the subagents of the branch in play.
type ChatWork struct {
	Todos  []WorkTodo  `json:"todos,omitempty"`
	Agents []WorkAgent `json:"agents,omitempty"`
}

// WorkTodo is one item of the list.
type WorkTodo struct {
	Text   string `json:"text"`
	Status string `json:"status"`          // pending, in_progress, completed
	Phase  string `json:"phase,omitempty"` // tego's grouping
	id     string // Claude Code's task number
}

// WorkAgent is one subagent.
type WorkAgent struct {
	Name       string `json:"name"`
	ID         string `json:"id,omitempty"`   // a tego background task's ("t3")
	Kind       string `json:"kind,omitempty"` // the agent definition it runs (Claude Code's subagent_type, tego's agent)
	Task       string `json:"task,omitempty"` // the first line of what it was asked
	State      string `json:"state"`          // running, done, failed, stopped, unknown
	Why        string `json:"why,omitempty"`  // why it failed or stopped
	Report     string `json:"report,omitempty"`
	Background bool   `json:"background,omitempty"`
	Started    int64  `json:"started,omitempty"` // unix ms, when known
	Ended      int64  `json:"ended,omitempty"`
	call       string // the tool call that started it
	agentID    string // Claude Code's id of a background agent
}

// Subagents kept: the newest.
const workAgentsKept = 40

// A background task still "running" this long after it started lost its
// session (tego was quit or restarted): no report will come.
const workStale = 12 * time.Hour

// Tools whose input the work needs (blocks() keeps it for them).
var workTools = map[string]bool{
	"todo": true, "task": true, // tego
	"TodoWrite": true, "TaskCreate": true, "TaskUpdate": true, "Agent": true, "Task": true, // Claude Code
	"update_plan": true, // Codex
}

type workTracker struct {
	todos  []WorkTodo
	agents []*WorkAgent
	calls  map[string]string // tool call id -> tool name, for the result
	create map[string]int    // a TaskCreate call -> its todo, numbered by the result
	now    time.Time         // the entry being read
}

func newWorkTracker() *workTracker {
	return &workTracker{calls: map[string]string{}, create: map[string]int{}}
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// call: a tool call with its whole input.
func (w *workTracker) call(id, name string, input json.RawMessage) {
	if !workTools[name] || len(input) == 0 {
		return
	}
	w.calls[id] = name
	switch name {
	case "todo", "TodoWrite":
		var in struct {
			Todos []struct {
				Content, Status, Phase string
			} `json:"todos"`
		}
		if json.Unmarshal(input, &in) != nil {
			return
		}
		w.todos = w.todos[:0:0]
		for _, t := range in.Todos {
			w.todos = append(w.todos, WorkTodo{Text: t.Content, Status: todoStatus(t.Status), Phase: t.Phase})
		}
	case "update_plan":
		var in struct {
			Plan []struct{ Step, Status string } `json:"plan"`
		}
		if json.Unmarshal(input, &in) != nil {
			return
		}
		w.todos = w.todos[:0:0]
		for _, t := range in.Plan {
			w.todos = append(w.todos, WorkTodo{Text: t.Step, Status: todoStatus(t.Status)})
		}
	case "TaskCreate":
		var in struct{ Subject string }
		if json.Unmarshal(input, &in) != nil || in.Subject == "" {
			return
		}
		w.todos = append(w.todos, WorkTodo{Text: in.Subject, Status: "pending"})
		w.create[id] = len(w.todos) - 1
	case "TaskUpdate":
		var in struct {
			TaskID  string `json:"taskId"`
			Status  string `json:"status"`
			Subject string `json:"subject"`
		}
		if json.Unmarshal(input, &in) != nil {
			return
		}
		for i := range w.todos {
			if w.todos[i].id != in.TaskID || in.TaskID == "" {
				continue
			}
			if in.Status == "deleted" {
				w.todos = append(w.todos[:i], w.todos[i+1:]...)
				break
			}
			if in.Status != "" {
				w.todos[i].Status = todoStatus(in.Status)
			}
			if in.Subject != "" {
				w.todos[i].Text = in.Subject
			}
			break
		}
	case "task":
		var in struct {
			Tasks []struct {
				Name, Prompt, Agent string
			} `json:"tasks"`
			Background bool `json:"background"`
		}
		if json.Unmarshal(input, &in) != nil {
			return
		}
		for _, t := range in.Tasks {
			w.agents = append(w.agents, &WorkAgent{Name: t.Name, Kind: t.Agent, Task: oneLine(t.Prompt, 160),
				State: "running", Background: in.Background, Started: ms(w.now), call: id})
		}
	case "Agent", "Task":
		var in struct {
			Description     string `json:"description"`
			Prompt          string `json:"prompt"`
			SubagentType    string `json:"subagent_type"`
			RunInBackground bool   `json:"run_in_background"`
		}
		if json.Unmarshal(input, &in) != nil || (in.Description == "" && in.Prompt == "") {
			return
		}
		name := in.Description
		if name == "" {
			name = oneLine(in.Prompt, 60)
		}
		w.agents = append(w.agents, &WorkAgent{Name: name, Kind: in.SubagentType, Task: oneLine(in.Prompt, 160),
			State: "running", Background: in.RunInBackground, Started: ms(w.now), call: id})
	}
}

// oneLine: the first line with text, at most max runes.
func oneLine(s string, max int) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return firstLines(l, 1, max)
		}
	}
	return ""
}

func todoStatus(s string) string {
	switch s {
	case "in_progress", "completed":
		return s
	}
	return "pending"
}

var (
	// tego: "## Name (done)" in a task call's result; "## t3 Name (failed: why)" in a background report.
	sectionRe = regexp.MustCompile(`(?m)^## (?:(t\d+) )?(\S+) \(([^)\n]*)\)\s*$`)
	// tego: "Started in the background: t1 A, t2 B."
	startedRe = regexp.MustCompile(`\b(t\d+) ([^\s,.]+)`)
	// tego's task_status: "t1 Name: running · 1m12s · …"
	statusRowRe = regexp.MustCompile(`(?m)^(t\d+) (\S+): ([a-z]+)(?::? ([^·\n]*))?`)
	// Claude Code: "Task #3 created successfully: …"
	createdRe = regexp.MustCompile(`Task #(\d+) created`)
)

// state turns tego's "done", "failed: why", "cancelled" into a state and why.
func workState(s string) (state, why string) {
	word, rest, _ := strings.Cut(s, ":")
	word, rest = strings.TrimSpace(word), strings.TrimSpace(rest)
	switch word {
	case "done", "completed":
		return "done", rest
	case "failed", "error":
		return "failed", rest
	case "running", "queued":
		return "running", ""
	case "cancelled", "canceled", "killed", "stopped":
		return "stopped", rest
	}
	return "failed", strings.TrimSpace(s)
}

// result: a tool call's whole result text.
func (w *workTracker) result(id, text string, isErr bool) {
	name, ok := w.calls[id]
	if !ok {
		if strings.Contains(text, " · ") && statusRowRe.MatchString(text) { // tego's task_status
			w.statusRows(text)
		}
		return
	}
	delete(w.calls, id)
	switch name {
	case "TaskCreate":
		if i, ok := w.create[id]; ok && i < len(w.todos) {
			if m := createdRe.FindStringSubmatch(text); m != nil {
				w.todos[i].id = m[1]
			}
		}
		delete(w.create, id)
	case "task":
		mine := w.agentsOf(id)
		if isErr {
			for _, a := range mine {
				a.State, a.Why, a.Ended = "failed", oneLine(text, 160), ms(w.now)
			}
			return
		}
		if rest, ok := strings.CutPrefix(text, "Started in the background: "); ok {
			rest, _, _ = strings.Cut(rest, ". ")
			for _, m := range startedRe.FindAllStringSubmatch(rest, -1) {
				for _, a := range mine {
					if a.Name == m[2] && a.ID == "" {
						w.reuse(m[1])
						a.ID = m[1]
						break
					}
				}
			}
			return
		}
		w.sections(text, func(_, name string) *WorkAgent {
			for _, a := range mine {
				if a.Name == name && a.State == "running" {
					return a
				}
			}
			return nil
		})
		for _, a := range mine { // no section of its own: ended with the call
			if a.State == "running" && !a.Background {
				a.State, a.Ended = "done", ms(w.now)
			}
		}
	case "Agent", "Task":
		for _, a := range w.agentsOf(id) {
			switch {
			case isErr:
				a.State, a.Why, a.Ended = "failed", oneLine(text, 160), ms(w.now)
			case strings.HasPrefix(text, "Async agent launched"):
				a.Background = true
				if m := agentIDRe.FindStringSubmatch(text); m != nil {
					a.agentID = m[1]
				}
			default:
				a.State, a.Report, a.Ended = "done", oneLine(text, 200), ms(w.now)
			}
		}
	}
}

func (w *workTracker) agentsOf(call string) []*WorkAgent {
	var out []*WorkAgent
	for _, a := range w.agents {
		if a.call == call {
			out = append(out, a)
		}
	}
	return out
}

// reuse: tego numbers background tasks afresh after a restart. An older task
// of that id still "running" never reported: it went with the old session.
func (w *workTracker) reuse(id string) {
	for _, a := range w.agents {
		if a.ID == id && a.State == "running" {
			a.State, a.Why, a.ID = "unknown", "its session ended before it reported", ""
		}
	}
}

// sections reads "## [tN] Name (state)" sections and the report under each.
func (w *workTracker) sections(text string, find func(id, name string) *WorkAgent) {
	ms := sectionRe.FindAllStringSubmatchIndex(text, -1)
	for i, m := range ms {
		id, name, st := "", text[m[4]:m[5]], text[m[6]:m[7]]
		if m[2] >= 0 {
			id = text[m[2]:m[3]]
		}
		a := find(id, name)
		if a == nil {
			continue
		}
		end := len(text)
		if i+1 < len(ms) {
			end = ms[i+1][0]
		}
		body := text[m[1]:end]
		if strings.HasPrefix(strings.TrimSpace(body), "Task: ") { // a background report repeats the task first
			_, body, _ = strings.Cut(strings.TrimSpace(body), "\n")
		}
		a.State, a.Why = workState(st)
		a.Report, a.Ended = oneLine(strings.ReplaceAll(body, "</system-reminder>", ""), 200), w.msNow()
	}
}

func (w *workTracker) msNow() int64 { return ms(w.now) }

// byID: the newest agent with a background id.
func (w *workTracker) byID(id string) *WorkAgent {
	for i := len(w.agents) - 1; i >= 0; i-- {
		if w.agents[i].ID == id {
			return w.agents[i]
		}
	}
	return nil
}

func (w *workTracker) statusRows(text string) {
	for _, m := range statusRowRe.FindAllStringSubmatch(text, -1) {
		a := w.byID(m[1])
		if a == nil || a.Name != m[2] || a.State != "running" {
			continue
		}
		if st, why := workState(m[3] + ": " + m[4]); st != "running" {
			a.State, a.Why, a.Ended = st, strings.TrimSpace(why), w.msNow()
		}
	}
}

var (
	notifToolRe   = regexp.MustCompile(`<tool-use-id>([^<]+)</tool-use-id>`)
	notifStatusRe = regexp.MustCompile(`<status>([^<]+)</status>`)
	notifResultRe = regexp.MustCompile(`(?s)<result>(.*?)</result>`)
	agentIDRe     = regexp.MustCompile(`agentId: (\w+)`)
	handbackRe    = regexp.MustCompile(`<agent-message from="(\w+)">`)
)

// handback: Claude Code's message carrying a background agent's report
// ("Another Claude session sent a message: … [Subagent hand-back] …").
func handback(t string) bool {
	return strings.HasPrefix(t, "Another Claude session sent a message:") && strings.Contains(t, "[Subagent hand-back]")
}

// text: a message's text, hidden from the chat or not; background reports
// come in it.
func (w *workTracker) text(t string) {
	switch {
	case strings.Contains(t, "Background tasks you started ended."): // tego
		w.sections(t, func(id, name string) *WorkAgent {
			if a := w.byID(id); a != nil && a.Name == name {
				return a
			}
			return nil
		})
	case handback(t): // Claude Code: the report, before its notification
		m := handbackRe.FindStringSubmatch(t)
		if m == nil {
			return
		}
		_, report, ok := strings.Cut(t, "The report follows:")
		if !ok {
			return
		}
		for _, a := range w.agents {
			if a.agentID == m[1] {
				a.Report = oneLine(report, 200)
			}
		}
	case strings.HasPrefix(t, "<task-notification>"): // Claude Code
		m := notifToolRe.FindStringSubmatch(t)
		if m == nil {
			return
		}
		for _, a := range w.agents {
			if a.call != m[1] || a.State != "running" {
				continue
			}
			st := "done"
			if s := notifStatusRe.FindStringSubmatch(t); s != nil {
				st, _ = workState(s[1])
			}
			a.State, a.Ended = st, w.msNow()
			if r := notifResultRe.FindStringSubmatch(t); r != nil && !strings.Contains(r[1], "report was delivered to you") {
				a.Report = oneLine(r[1], 200)
			}
		}
	}
}

// workLines keeps of a tool's result what tells how subagents went: the
// "## Name (state)" lines and the line under each, tego's "Started in the
// background" and task_status rows. "" when it has none.
func workLines(full string) string {
	if !strings.Contains(full, "## ") && !strings.Contains(full, "Started in the background") && !strings.Contains(full, " · ") {
		return ""
	}
	var out []string
	lines := strings.Split(full, "\n")
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		switch {
		case sectionRe.MatchString(l):
			out = append(out, l)
			for j := i + 1; j < len(lines); j++ { // its report's first line
				if t := strings.TrimSpace(lines[j]); t != "" {
					if !sectionRe.MatchString(lines[j]) {
						out = append(out, oneLine(t, 200))
					}
					break
				}
			}
		case strings.HasPrefix(l, "Started in the background: "), statusRowRe.MatchString(l):
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// done: what was found, nil when nothing.
func (w *workTracker) done(now time.Time) *ChatWork {
	if len(w.todos) == 0 && len(w.agents) == 0 {
		return nil
	}
	out := &ChatWork{Todos: w.todos}
	agents := w.agents
	if len(agents) > workAgentsKept {
		agents = agents[len(agents)-workAgentsKept:]
	}
	for _, a := range agents {
		c := *a
		if c.State == "running" && c.Started > 0 && now.Sub(time.UnixMilli(c.Started)) > workStale {
			c.State, c.Why = "unknown", "no report since it started"
		}
		out.Agents = append(out.Agents, c)
	}
	return out
}
