package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The title of the session an agent works in, per terminal, for the herd list
// on the phone ("tego · Fix the bug in the nav"). Finding a terminal's
// transcript takes a few programs (hc get, ps, lsof), too many for every
// /api/state poll: the titles are kept and refreshed in the background, at most
// every titleRefresh, only while phones ask.

const (
	titleRefresh   = 5 * time.Second
	titleFirstWait = 1500 * time.Millisecond // the first poll waits this long for them
	titleMax       = 80
)

type titleCache struct {
	mu   sync.Mutex
	at   time.Time         // when the last refresh began
	m    map[string]string // terminal -> title
	busy bool
	done chan struct{} // closed when the refresh under way ends
}

// sessionTitles: the titles known now (terminal -> title); starts a refresh
// when they are old. The very first call waits a little for them.
func (s *server) sessionTitles(ctx context.Context) map[string]string {
	c := &s.titles
	c.mu.Lock()
	if !c.busy && time.Since(c.at) >= titleRefresh {
		c.busy, c.at, c.done = true, time.Now(), make(chan struct{})
		go s.refreshTitles(c.done)
	}
	first, done := c.m == nil, c.done
	c.mu.Unlock()
	if first && done != nil {
		select {
		case <-done:
		case <-time.After(titleFirstWait):
		case <-ctx.Done():
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m
}

func (s *server) refreshTitles(done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m := map[string]string{}
	for terminal, path := range s.agentTranscripts(ctx) {
		if t := transcriptTitle(path); t != "" {
			m[terminal] = t
		}
	}
	c := &s.titles
	c.mu.Lock()
	c.m, c.busy = m, false
	close(done)
	c.mu.Unlock()
}

// agentTranscripts: the transcript open in each live terminal of this Mac
// that runs an agent (hc's terminals and the herdr CLI's agent panes).
func (s *server) agentTranscripts(ctx context.Context) map[string]string {
	var terminals []string
	var list struct {
		Terminals []struct{ Address, State, Agent string }
	}
	if ls, err := run(ctx, 3*time.Second, s.hc, "ls", "--json"); err == nil {
		json.Unmarshal(ls, &list)
	}
	for _, t := range list.Terminals {
		prog := strings.ToLower(filepath.Base(t.Agent))
		if t.State != "live" || prog == "" || shells[prog] || !validTerminal(t.Address) { // a shell: no session to name
			continue
		}
		if m, _ := splitTerminal(t.Address); m != "" {
			continue // another machine's files aren't here
		}
		terminals = append(terminals, t.Address)
	}
	if s.herdrSock != "" {
		hctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		if snap, err := s.herdrSnapshot(hctx); err == nil {
			for _, a := range snap.Agents {
				terminals = append(terminals, herdrAddr(a.PaneID))
			}
		}
		cancel()
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
	out := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, t := range terminals {
		wg.Add(1)
		go func(t string) {
			defer wg.Done()
			if p := transcript(ctx, t); p != "" {
				mu.Lock()
				out[t] = p
				mu.Unlock()
			}
		}(t)
	}
	wg.Wait()
	return out
}

// transcriptTitle: what a session is called: the title the agent gave it,
// else its first message, on one line.
func transcriptTitle(path string) string {
	agent := agentOfTranscript(path)
	if agent == "" {
		return ""
	}
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	h := readSessionHead(sessionFile{agent: agent, path: path, mtime: st.ModTime(), size: st.Size()})
	t := h.title
	if t == "" {
		t = h.first
	}
	t = cleanTitle(t)
	if r := []rune(t); len(r) > titleMax {
		t = strings.TrimSpace(string(r[:titleMax-1])) + "…"
	}
	return t
}

// agentOfTranscript: whose transcript a file is, by where it lies.
func agentOfTranscript(path string) string {
	p := filepath.ToSlash(path)
	switch {
	case strings.Contains(p, "/.tego/sessions/"):
		return "tego"
	case strings.Contains(p, "/.claude/projects/"):
		return "claude"
	}
	return "" // codex and others: their titles aren't read
}
