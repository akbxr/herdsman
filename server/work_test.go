package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

// workOf builds a transcript given as entries and returns its work.
func workOf(t *testing.T, agent string, entries ...any) (*ChatWork, []ChatItem) {
	t.Helper()
	var ls [][]byte
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		ls = append(ls, b)
	}
	sc := newTreeScan(agent)
	sc.add(ls)
	items, _ := sc.build()
	return sc.work, items
}

// tego entries, chained.
type tegoChain struct {
	n  int
	ts int64
}

func (c *tegoChain) msg(role string, content ...map[string]any) any {
	c.n++
	c.ts += 60
	e := map[string]any{"type": "message", "id": "m" + strconv.Itoa(c.n), "ts": c.ts,
		"message": map[string]any{"role": role, "content": content}}
	if c.n > 1 {
		e["parent"] = "m" + strconv.Itoa(c.n-1)
	}
	return e
}

func use(id, name string, input any) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
}

func res(id, text string, isErr bool) map[string]any {
	return map[string]any{"type": "tool_result", "tool_use_id": id, "content": text, "is_error": isErr}
}

func txt(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

func agentNamed(w *ChatWork, name string) *WorkAgent {
	for i := range w.Agents {
		if w.Agents[i].Name == name {
			return &w.Agents[i]
		}
	}
	return nil
}

func TestWorkTego(t *testing.T) {
	c := &tegoChain{ts: time.Now().Unix() - 3600}
	w, items := workOf(t, "tego",
		c.msg("user", txt("fix it")),
		c.msg("assistant", use("a", "todo", map[string]any{"todos": []map[string]string{
			{"content": "Read the code", "status": "completed", "phase": "Look"},
			{"content": "Fix the nav", "status": "in_progress", "phase": "Fix"},
		}})),
		c.msg("user", res("a", "Todo 1/2", false)),
		// A foreground call: one done, one failed.
		c.msg("assistant", use("b", "task", map[string]any{"tasks": []map[string]string{
			{"name": "Finder", "prompt": "find the bug\nin nav.go"}, {"name": "Checker", "prompt": "run the tests", "agent": "reviewer"},
		}})),
		c.msg("user", res("b", "Ran 2 subagents.\n\n## Finder (done)\nThe bug is in nav.go:12.\nmore\n\n## Checker (failed: stopped after 150 turns)\n(no report)", false)),
		// Background tasks: one reports, one still runs.
		c.msg("assistant", use("c", "task", map[string]any{"background": true, "tasks": []map[string]string{
			{"name": "Digger", "prompt": "dig"}, {"name": "Runner", "prompt": "run"},
		}})),
		c.msg("user", res("c", "Started in the background: t1 Digger, t2 Runner. You and the user can go on; each report comes", false)),
		c.msg("user", txt("<system-reminder>Background tasks you started ended. Their reports (from subagents, not the user):\n\n## t1 Digger (done)\nTask: dig\n\nFound it in parse.go.</system-reminder>")),
		c.msg("assistant", txt("ok")),
	)
	if w == nil || len(w.Todos) != 2 || w.Todos[1] != (WorkTodo{Text: "Fix the nav", Status: "in_progress", Phase: "Fix"}) {
		t.Fatalf("todos %+v", w)
	}
	want := map[string][3]string{ // state, report or why, id
		"Finder":  {"done", "The bug is in nav.go:12.", ""},
		"Checker": {"failed", "stopped after 150 turns", ""},
		"Digger":  {"done", "Found it in parse.go.", "t1"},
		"Runner":  {"running", "", "t2"},
	}
	for name, st := range want {
		a := agentNamed(w, name)
		if a == nil {
			t.Fatalf("no %s in %+v", name, w.Agents)
		}
		got := a.Report
		if a.State == "failed" {
			got = a.Why
		}
		if a.State != st[0] || got != st[1] || a.ID != st[2] {
			t.Errorf("%s: %+v", name, *a)
		}
	}
	if a := agentNamed(w, "Finder"); a.Task != "find the bug" || a.Started == 0 || a.Ended <= a.Started {
		t.Errorf("finder %+v", *a)
	}
	if a := agentNamed(w, "Checker"); a.Kind != "reviewer" {
		t.Errorf("checker kind %+v", *a)
	}
	if !agentNamed(w, "Runner").Background || agentNamed(w, "Finder").Background {
		t.Error("background")
	}
	for _, it := range items { // the report stays out of the chat
		if strings.Contains(it.Text, "Background tasks") {
			t.Errorf("report in the chat: %+v", it)
		}
	}
}

// task_status ends a task; a restart reuses an id; an old one goes stale.
func TestWorkTegoStatusAndRestart(t *testing.T) {
	c := &tegoChain{ts: time.Now().Add(-20 * time.Hour).Unix()}
	w, _ := workOf(t, "tego",
		c.msg("user", txt("go")),
		c.msg("assistant", use("a", "task", map[string]any{"background": true, "tasks": []map[string]string{{"name": "Old", "prompt": "x"}, {"name": "Lost", "prompt": "y"}}})),
		c.msg("user", res("a", "Started in the background: t1 Old, t2 Lost. You can go on", false)),
		c.msg("assistant", use("b", "task_status", map[string]any{})),
		c.msg("user", res("b", "t1 Old: failed: stopped after 150 turns · 1h7m11s · 170 tool calls · report already delivered\nt2 Lost: running · 3m · 5 tool calls", false)),
		// tego restarted: t1 again.
		c.msg("assistant", use("c", "task", map[string]any{"background": true, "tasks": []map[string]string{{"name": "New", "prompt": "z"}}})),
		c.msg("user", res("c", "Started in the background: t2 New. You can go on", false)),
	)
	old, lost, nw := agentNamed(w, "Old"), agentNamed(w, "Lost"), agentNamed(w, "New")
	if old.State != "failed" || old.Why != "stopped after 150 turns" {
		t.Errorf("old %+v", *old)
	}
	if lost.State != "unknown" || lost.ID != "" {
		t.Errorf("lost %+v", *lost)
	}
	if nw.State != "unknown" || nw.ID != "t2" { // started 20 h ago in this test: stale
		t.Errorf("new %+v", *nw)
	}
}

func claudeEntryOf(n int, role string, content any) map[string]any {
	e := map[string]any{"type": role, "uuid": "u" + strconv.Itoa(n), "timestamp": time.Now().Add(time.Duration(n-60) * time.Minute).UTC().Format(time.RFC3339Nano),
		"message": map[string]any{"role": role, "content": content}}
	if n > 1 {
		e["parentUuid"] = "u" + strconv.Itoa(n-1)
	}
	return e
}

func TestWorkClaude(t *testing.T) {
	w, items := workOf(t, "claude",
		claudeEntryOf(1, "user", "research pki"),
		claudeEntryOf(2, "assistant", []any{use("a", "TaskCreate", map[string]string{"subject": "Read RFC 5280"})}),
		claudeEntryOf(3, "user", []any{res("a", "Task #1 created successfully: Read RFC 5280", false)}),
		claudeEntryOf(4, "assistant", []any{use("b", "TaskCreate", map[string]string{"subject": "Write notes"})}),
		claudeEntryOf(5, "user", []any{res("b", "Task #2 created successfully: Write notes", false)}),
		claudeEntryOf(6, "assistant", []any{use("c", "TaskUpdate", map[string]string{"taskId": "1", "status": "completed"})}),
		claudeEntryOf(7, "user", []any{res("c", "Updated task #1 status", false)}),
		claudeEntryOf(8, "assistant", []any{use("d", "Agent", map[string]any{"description": "Find CA docs", "prompt": "look", "subagent_type": "Explore"})}),
		claudeEntryOf(9, "user", []any{res("d", "The docs are at x.", false)}),
		claudeEntryOf(10, "assistant", []any{use("e", "Agent", map[string]any{"description": "Chain check", "prompt": "check", "run_in_background": true})}),
		claudeEntryOf(11, "user", []any{res("e", "Async agent launched successfully. (internal)\nagentId: abc (internal ID)", false)}),
		claudeEntryOf(12, "user", "Another Claude session sent a message:\n<agent-message from=\"abc\">\n[Subagent hand-back] The text below is the final report. The report follows:\n  Chains verify.\n  More.\n</agent-message>"),
		claudeEntryOf(13, "user", "<task-notification>\n<task-id>abc</task-id>\n<tool-use-id>e</tool-use-id>\n<status>completed</status>\n<summary>Agent \"Chain check\" finished</summary>\n<result>This agent's report was delivered to you as a message from \"abc\".</result>\n</task-notification>"),
		claudeEntryOf(14, "assistant", []any{use("f", "Agent", map[string]any{"description": "Still going", "prompt": "p"})}),
	)
	if w == nil || len(w.Todos) != 2 || w.Todos[0].Status != "completed" || w.Todos[1].Status != "pending" {
		t.Fatalf("todos %+v", w)
	}
	if a := agentNamed(w, "Find CA docs"); a.State != "done" || a.Report != "The docs are at x." || a.Kind != "Explore" {
		t.Errorf("fg %+v", *a)
	}
	if a := agentNamed(w, "Chain check"); a.State != "done" || !a.Background || a.Report != "Chains verify." {
		t.Errorf("bg %+v", *a)
	}
	if a := agentNamed(w, "Still going"); a.State != "running" {
		t.Errorf("running %+v", *a)
	}
	for _, it := range items {
		if strings.Contains(it.Text, "task-notification") || strings.Contains(it.Text, "hand-back") {
			t.Errorf("notification in the chat: %+v", it)
		}
	}
	// TodoWrite replaces the list; a deleted task leaves it.
	w, _ = workOf(t, "claude",
		claudeEntryOf(1, "assistant", []any{use("a", "TodoWrite", map[string]any{"todos": []map[string]string{{"content": "A", "status": "in_progress", "activeForm": "Doing A"}}})}),
		claudeEntryOf(2, "assistant", []any{use("b", "TaskCreate", map[string]string{"subject": "B"})}),
		claudeEntryOf(3, "user", []any{res("b", "Task #7 created successfully: B", false)}),
		claudeEntryOf(4, "assistant", []any{use("c", "TaskUpdate", map[string]string{"taskId": "7", "status": "deleted"})}),
	)
	if len(w.Todos) != 1 || w.Todos[0].Text != "A" || w.Todos[0].Status != "in_progress" {
		t.Errorf("todowrite %+v", w.Todos)
	}
}

func TestWorkCodexPlan(t *testing.T) {
	args, _ := json.Marshal(map[string]any{"plan": []map[string]string{{"step": "Read", "status": "completed"}, {"step": "Write", "status": "in_progress"}}})
	l, _ := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "name": "update_plan", "arguments": string(args), "call_id": "c1"}})
	b := codexBuilder([][]byte{l}, images.addFile)
	w := b.work.done(time.Now())
	if w == nil || len(w.Todos) != 2 || w.Todos[1] != (WorkTodo{Text: "Write", Status: "in_progress"}) {
		t.Fatalf("plan %+v", w)
	}
}

// A conversation without todos or subagents has no work.
func TestWorkNone(t *testing.T) {
	c := &tegoChain{ts: time.Now().Unix()}
	if w, _ := workOf(t, "tego", c.msg("user", txt("hi")), c.msg("assistant", use("a", "bash", map[string]string{"command": "ls"}))); w != nil {
		t.Errorf("work %+v", w)
	}
}
