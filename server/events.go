package main

// Agents' status changes for the phone's notifications. One `hc events`
// stream feeds a small ring of events; the phone long-polls /api/events.
// The words are the programs' own (OSC 7501: state, kind, msg), so a
// notification says what tego or Claude Code actually asked.
//
//   GET /api/events             → {seq, current: [...]}   right away
//   GET /api/events?after=41    → {seq, events: [...]}    waits up to 25 s
//                                 {seq, reset: true, current: [...]}  when 41
//                                 is too old (the phone was away for long)

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// AgentEvent is one change: a terminal's program reported a state, or ended.
type AgentEvent struct {
	Seq      int64  `json:"seq"`
	Terminal string `json:"terminal"`
	State    string `json:"state"` // idle, working, blocked, done, error; "" cleared; "ended"
	Kind     string `json:"kind,omitempty"`
	Msg      string `json:"msg,omitempty"`
	App      string `json:"app,omitempty"`
	Title    string `json:"title,omitempty"`
	Where    string `json:"where,omitempty"` // Herdr's label: "~ / 1"
	At       int64  `json:"at"`              // unix ms
}

type eventHub struct {
	hc string

	mu      sync.Mutex
	seq     int64
	ring    []AgentEvent          // the last ringSize events
	current map[string]AgentEvent // latest per terminal
	where   map[string]string
	wake    chan struct{} // closed and replaced on every event
}

const ringSize = 256

func newEventHub(hc string) *eventHub {
	return &eventHub{hc: hc, current: map[string]AgentEvent{}, where: map[string]string{}, wake: make(chan struct{})}
}

// run keeps one `hc events` going for the life of the server.
func (h *eventHub) run(ctx context.Context) {
	h.seed(ctx)
	for ctx.Err() == nil {
		if err := h.stream(ctx); err != nil && ctx.Err() == nil {
			log.Printf("events: %v; again in 3s", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(3 * time.Second):
		}
	}
}

// seed fills in what each live terminal reports now, without events.
func (h *eventHub) seed(ctx context.Context) {
	out, err := run(ctx, 10*time.Second, h.hc, "ls", "--json")
	if err != nil {
		return
	}
	var ls struct {
		Terminals []struct{ Address, State string }
	}
	if json.Unmarshal(out, &ls) != nil {
		return
	}
	for _, t := range ls.Terminals {
		if t.State != "live" {
			continue
		}
		st, where, ok := h.get(ctx, t.Address)
		if !ok || st == nil {
			continue
		}
		h.mu.Lock()
		h.where[t.Address] = where
		h.current[t.Address] = AgentEvent{Terminal: t.Address, State: st.State, Kind: st.Kind, Msg: st.Msg, App: st.App, Title: st.Title, Where: where, At: time.Now().UnixMilli()}
		h.mu.Unlock()
	}
	// Followers of the current states (keeping the Mac awake) look again.
	// Long polls wake too and find no new events: they wait again.
	h.mu.Lock()
	close(h.wake)
	h.wake = make(chan struct{})
	h.mu.Unlock()
}

type status7501 struct {
	State, Kind, Msg, App, Title string
}

func (h *eventHub) get(ctx context.Context, terminal string) (*status7501, string, bool) {
	out, err := run(ctx, 5*time.Second, h.hc, "get", "--json", terminal)
	if err != nil {
		return nil, "", false
	}
	var g struct {
		Status *status7501
		Labels map[string]string
	}
	if json.Unmarshal(out, &g) != nil {
		return nil, "", false
	}
	return g.Status, g.Labels["where"], true
}

func (h *eventHub) stream(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, h.hc, "events", "--json")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var line struct {
			Params struct {
				Type     string
				Terminal string
				Data     json.RawMessage
			}
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		p := line.Params
		switch p.Type {
		case "status":
			var d struct{ Status *status7501 }
			if json.Unmarshal(p.Data, &d) != nil {
				continue
			}
			ev := AgentEvent{Terminal: p.Terminal}
			if d.Status != nil {
				ev.State, ev.Kind, ev.Msg, ev.App, ev.Title = d.Status.State, d.Status.Kind, d.Status.Msg, d.Status.App, d.Status.Title
			}
			h.add(ctx, ev)
		case "finished":
			h.add(ctx, AgentEvent{Terminal: p.Terminal, State: "ended"})
		}
	}
	cmd.Wait()
	if err := sc.Err(); err != nil {
		return err
	}
	return errStreamEnded
}

type streamEnded struct{}

func (streamEnded) Error() string { return "hc events ended" }

var errStreamEnded error = streamEnded{}

func (h *eventHub) add(ctx context.Context, ev AgentEvent) {
	h.mu.Lock()
	where, known := h.where[ev.Terminal]
	h.mu.Unlock()
	if !known && ev.State != "ended" {
		_, where, _ = h.get(ctx, ev.Terminal) // Herdr's label, once per terminal
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.where[ev.Terminal] = where
	ev.Where = where
	ev.At = time.Now().UnixMilli()
	h.seq++
	ev.Seq = h.seq
	h.ring = append(h.ring, ev)
	if len(h.ring) > ringSize {
		h.ring = h.ring[len(h.ring)-ringSize:]
	}
	if ev.State == "" || ev.State == "ended" {
		delete(h.current, ev.Terminal)
		if ev.State == "ended" {
			delete(h.where, ev.Terminal)
		}
	} else {
		h.current[ev.Terminal] = ev
	}
	close(h.wake)
	h.wake = make(chan struct{})
}

// snapshot returns what each terminal reports now.
func (h *eventHub) snapshot() []AgentEvent {
	cur := make([]AgentEvent, 0, len(h.current))
	for _, e := range h.current {
		cur = append(cur, e)
	}
	return cur
}

// since returns the events after seq, whether seq was too old, and a channel
// that closes on the next event.
func (h *eventHub) since(after int64) (events []AgentEvent, reset bool, seq int64, wake <-chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	seq, wake = h.seq, h.wake
	if after > h.seq {
		return nil, true, seq, wake // the server restarted: its counter began again
	}
	if len(h.ring) > 0 && after < h.ring[0].Seq-1 {
		return nil, true, seq, wake
	}
	for _, e := range h.ring {
		if e.Seq > after {
			events = append(events, e)
		}
	}
	return events, false, seq, wake
}

func (s *server) eventsHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	after, err := strconv.ParseInt(q.Get("after"), 10, 64)
	if q.Get("after") == "" || err != nil {
		s.events.mu.Lock()
		resp := map[string]any{"seq": s.events.seq, "current": s.events.snapshot()}
		s.events.mu.Unlock()
		writeJSON(w, resp)
		return
	}
	wait := 25 * time.Second
	if n, err := strconv.Atoi(q.Get("wait")); err == nil && n >= 0 && n <= 50 {
		wait = time.Duration(n) * time.Second
	}
	deadline := time.After(wait)
	for {
		events, reset, seq, wake := s.events.since(after)
		if reset {
			s.events.mu.Lock()
			resp := map[string]any{"seq": s.events.seq, "reset": true, "current": s.events.snapshot()}
			s.events.mu.Unlock()
			writeJSON(w, resp)
			return
		}
		if len(events) > 0 {
			writeJSON(w, map[string]any{"seq": seq, "events": events})
			return
		}
		select {
		case <-wake:
		case <-deadline:
			writeJSON(w, map[string]any{"seq": seq, "events": []AgentEvent{}})
			return
		case <-r.Context().Done():
			return
		}
	}
}
