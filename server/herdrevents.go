package main

// The herdr CLI's agents feed the same event hub as hc's terminals, in the
// same shape: terminal "herdr:<pane>", app the agent, state idle, working,
// blocked or done ("" when herdr says unknown), where "<workspace> / <tab>".
//
// herdr streams pane.agent_status_changed only per pane, and one connection
// takes one events.subscribe: the subscription names every pane there is,
// and a pane that appears (created, moved, an agent detected in it) means a
// new subscription. Each time, once subscribed, the session's snapshot is
// compared with what the hub holds, so nothing that changed in between is
// lost. herdr gone: its terminals end; it's looked for again with backoff.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"
)

var errResubscribe = errors.New("herdr: new panes, subscribing again")

// herdrWatch is the events loop's memory: the panes the hub was told about
// and their labels.
type herdrWatch struct {
	s      *server
	where  map[string]string // pane → "<workspace> / <tab>"
	seeded bool
}

// Variables for the tests.
var (
	herdrRetryMin = time.Second
	herdrRetryMax = 30 * time.Second
)

// herdrEvents runs until ctx ends.
func (s *server) herdrEvents(ctx context.Context) {
	if s.herdrSock == "" || s.events == nil {
		return
	}
	w := &herdrWatch{s: s, where: map[string]string{}}
	retryMin, retryMax := herdrRetryMin, herdrRetryMax
	wait := retryMin
	logged := ""
	for ctx.Err() == nil {
		err := w.once(ctx)
		if errors.Is(err, errResubscribe) {
			wait = retryMin
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if msg := err.Error(); msg != logged && !errors.Is(err, errHerdrOff) {
			log.Printf("herdr events: %v; trying again", err)
			logged = msg
		}
		if w.seeded {
			// Gone (stopped, restarting): its agents with it, until it's back.
			w.gone(ctx)
			wait = retryMin
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		if wait *= 2; wait > retryMax {
			wait = retryMax
		}
	}
}

// once subscribes and follows the stream until it ends or must be redone.
func (w *herdrWatch) once(ctx context.Context) error {
	s := w.s
	snap, err := s.herdrSnapshot(ctx)
	if err != nil {
		return err
	}
	subs := []map[string]any{
		{"type": "pane.created"}, {"type": "pane.closed"}, {"type": "pane.exited"},
		{"type": "pane.moved"}, {"type": "pane.agent_detected"},
		{"type": "tab.renamed"}, {"type": "workspace.renamed"},
		{"type": "tab.closed"}, {"type": "workspace.closed"}, // their panes close without pane.closed
	}
	subscribed := map[string]bool{}
	for _, p := range snap.Panes {
		subscribed[p.PaneID] = true
		subs = append(subs, map[string]any{"type": "pane.agent_status_changed", "pane_id": p.PaneID})
	}
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	again := false
	started := func() {
		snap, err := s.herdrSnapshot(subCtx)
		if err != nil {
			again = true
			cancel()
			return
		}
		w.reconcile(subCtx, snap)
		for _, p := range snap.Panes {
			if !subscribed[p.PaneID] {
				again = true // appeared meanwhile
				cancel()
				return
			}
		}
	}
	each := func(event string, data json.RawMessage) error {
		var d struct {
			PaneID      string            `json:"pane_id"`
			Pane        *herdrPaneInfo    `json:"pane"`
			Agent       *string           `json:"agent"`
			AgentStatus string            `json:"agent_status"`
			Title       *string           `json:"title"`
			StateLabels map[string]string `json:"state_labels"`
			Released    bool              `json:"released"`
			FinalStatus string            `json:"final_status"`
		}
		if json.Unmarshal(data, &d) != nil {
			return nil
		}
		if d.PaneID == "" && d.Pane != nil {
			d.PaneID = d.Pane.PaneID
		}
		event = strings.ReplaceAll(event, ".", "_")
		switch event {
		case "pane_agent_status_changed":
			if d.PaneID != "" {
				w.status(ctx, d.PaneID, str(d.Agent), d.AgentStatus, str(d.Title), d.StateLabels)
			}
		case "pane_closed", "pane_exited":
			if _, ok := w.where[d.PaneID]; ok {
				delete(w.where, d.PaneID)
				s.events.add(ctx, AgentEvent{Terminal: herdrAddr(d.PaneID), State: "ended"})
			}
		case "pane_created", "pane_moved", "pane_agent_detected":
			if !subscribed[d.PaneID] || event == "pane_moved" {
				return errResubscribe
			}
			if d.Released { // the agent let go of the pane: its last word
				w.status(ctx, d.PaneID, str(d.Agent), d.FinalStatus, "", nil)
			}
		case "tab_closed", "workspace_closed":
			if snap, err := s.herdrSnapshot(ctx); err == nil {
				w.reconcile(ctx, snap) // the panes that went with it end
			}
		case "tab_renamed", "workspace_renamed":
			if snap, err := s.herdrSnapshot(ctx); err == nil {
				for p, where := range snap.where() {
					if _, ok := w.where[p]; ok {
						w.where[p] = where
						s.events.mu.Lock()
						if _, known := s.events.where[herdrAddr(p)]; known {
							s.events.where[herdrAddr(p)] = where
						}
						s.events.mu.Unlock()
					}
				}
			}
		}
		return nil
	}
	err = s.herdrSubscribe(subCtx, subs, started, each)
	if again && ctx.Err() == nil {
		return errResubscribe
	}
	return err
}

// herdrKind: what a blocked agent waits for, when herdr's state labels say
// (permission or question).
func herdrKind(labels map[string]string) string {
	for _, k := range []string{"kind", "blocked", "reason"} {
		v := strings.ToLower(labels[k])
		switch {
		case strings.Contains(v, "permission") || strings.Contains(v, "approval"):
			return "permission"
		case strings.Contains(v, "question") || strings.Contains(v, "input"):
			return "question"
		}
	}
	return ""
}

// status hands a pane's new state to the hub.
func (w *herdrWatch) status(ctx context.Context, pane, agent, status, title string, labels map[string]string) {
	ev := AgentEvent{Terminal: herdrAddr(pane), State: herdrState(status), App: agent, Title: title}
	if ev.State == "blocked" {
		ev.Kind = herdrKind(labels)
	}
	if _, ok := w.where[pane]; !ok {
		if snap, err := w.s.herdrSnapshot(ctx); err == nil {
			for p, where := range snap.where() {
				if _, ok := w.where[p]; !ok {
					w.where[p] = where
				}
			}
		}
	}
	w.add(ctx, ev, w.where[pane])
}

// add tells the hub, with the label it can't ask hc for.
func (w *herdrWatch) add(ctx context.Context, ev AgentEvent, where string) {
	h := w.s.events
	h.mu.Lock()
	cur, had := h.current[ev.Terminal]
	if ev.State != "ended" {
		h.where[ev.Terminal] = where
	}
	h.mu.Unlock()
	if ev.State == "" && !had {
		return // nothing to clear
	}
	if had && cur.State == ev.State && cur.App == ev.App && cur.Kind == ev.Kind {
		return // said already
	}
	h.add(ctx, ev)
}

// reconcile brings the hub in line with a snapshot: the first one quietly
// (like hc's seed), later ones as events for what changed.
func (w *herdrWatch) reconcile(ctx context.Context, snap herdrSnapshot) {
	h := w.s.events
	where := snap.where()
	type st struct{ agent, status, title string }
	now := map[string]st{}
	for _, a := range snap.Agents {
		now[a.PaneID] = st{str(a.Agent), a.AgentStatus, str(a.Title)}
	}
	if !w.seeded {
		h.mu.Lock()
		for p, a := range now {
			if state := herdrState(a.status); state != "" {
				t := herdrAddr(p)
				h.where[t] = where[p]
				h.current[t] = AgentEvent{Terminal: t, State: state, App: a.agent, Title: a.title, Where: where[p], At: time.Now().UnixMilli()}
			}
		}
		close(h.wake)
		h.wake = make(chan struct{})
		h.mu.Unlock()
		w.where = where
		w.seeded = true
		return
	}
	for p := range w.where {
		if _, ok := where[p]; !ok {
			h.add(ctx, AgentEvent{Terminal: herdrAddr(p), State: "ended"})
		}
	}
	w.where = where
	for p := range where {
		a := now[p]
		w.add(ctx, AgentEvent{Terminal: herdrAddr(p), State: herdrState(a.status), App: a.agent, Title: a.title}, where[p])
	}
}

// gone: herdr stopped; its terminals end, and the next snapshot seeds again.
func (w *herdrWatch) gone(ctx context.Context) {
	for p := range w.where {
		w.s.events.add(ctx, AgentEvent{Terminal: herdrAddr(p), State: "ended"})
	}
	w.where = map[string]string{}
	w.seeded = false
}
