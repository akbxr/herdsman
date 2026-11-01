package main

// The herdr CLI (herdr 0.9.x, a terminal workspace manager with its own
// server) beside the Herdr app: its panes are terminals here too, with the
// address "herdr:<pane id>" ("herdr:wF:p2"), on this Mac only.
//
// Its server answers newline-delimited JSON on a unix socket
// (~/.config/herdr/herdr.sock for the default session):
//
//	→ {"id":"1","method":"pane.read","params":{…}}
//	← {"id":"1","result":{…}}  or  {"id":"1","error":{"code":"…","message":"…"}}
//
// One connection per call; events.subscribe keeps its connection and streams
// events on it (herdrevents.go). There is no byte stream of a pane: the live
// terminal is a repaint of its screen (herdrattach.go).

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// herdrPaneRe: a herdr CLI pane's address. Pane ids are "w<id>:p<id>" with
// base-36-ish ids ("wF:p1E").
var herdrPaneRe = regexp.MustCompile(`^herdr:(w[A-Za-z0-9]{1,16}:p[A-Za-z0-9]{1,16})$`)

// herdrPane: the pane id of a herdr CLI terminal address.
func herdrPane(terminal string) (string, bool) {
	m := herdrPaneRe.FindStringSubmatch(terminal)
	if m == nil {
		return "", false
	}
	return m[1], true
}

func herdrAddr(pane string) string { return "herdr:" + pane }

var (
	herdrWorkspaceRe = regexp.MustCompile(`^w[A-Za-z0-9]{1,16}$`)
	herdrTabRe       = regexp.MustCompile(`^w[A-Za-z0-9]{1,16}:t[A-Za-z0-9]{1,16}$`)
	herdrPaneIDRe    = regexp.MustCompile(`^w[A-Za-z0-9]{1,16}:p[A-Za-z0-9]{1,16}$`)
)

// defaultHerdrSock: the default session's API socket.
func defaultHerdrSock() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "herdr", "herdr.sock")
}

// herdrError is an error the herdr server answered with.
type herdrError struct{ Code, Message string }

func (e herdrError) Error() string {
	if e.Message == "" {
		return "herdr: " + e.Code
	}
	return "herdr: " + e.Message
}

// errHerdrOff: no herdr CLI server to talk to (none configured, or its
// socket doesn't answer).
var errHerdrOff = errors.New("the herdr CLI isn't running on this Mac")

func herdrCode(err error) string {
	var he herdrError
	if errors.As(err, &he) {
		return he.Code
	}
	return ""
}

// herdrDial connects to the server's socket within ctx.
func (s *server) herdrDial(ctx context.Context) (net.Conn, error) {
	if s.herdrSock == "" {
		return nil, errHerdrOff
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", s.herdrSock)
	if err != nil {
		return nil, fmt.Errorf("%w (%v)", errHerdrOff, err)
	}
	return c, nil
}

func herdrRequest(id, method string, params any) ([]byte, error) {
	if params == nil {
		params = map[string]any{}
	}
	b, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// herdrReply decodes one reply line: its result, or the error it carries.
func herdrReply(line []byte) (json.RawMessage, error) {
	var r struct {
		Result json.RawMessage `json:"result"`
		Error  *herdrError     `json:"error"`
	}
	if err := json.Unmarshal(line, &r); err != nil {
		return nil, fmt.Errorf("herdr said %q", firstLines(string(line), 1, 200))
	}
	if r.Error != nil {
		return nil, *r.Error
	}
	return r.Result, nil
}

// herdrCall sends one request and returns its result (the "result" object).
// Without a deadline in ctx it gives up after 10 s.
func (s *server) herdrCall(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}
	conn, err := s.herdrDial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if d, ok := ctx.Deadline(); ok {
		conn.SetDeadline(d)
	}
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Now()) })
	defer stop()
	req, err := herdrRequest("1", method, params)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(req); err != nil {
		return nil, fmt.Errorf("herdr %s: %w", method, err)
	}
	rd := bufio.NewReaderSize(conn, 64<<10)
	line, err := rd.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("herdr %s: %w", method, ctx.Err())
		}
		return nil, fmt.Errorf("herdr %s: %w", method, err)
	}
	return herdrReply(line)
}

// herdrDo calls and decodes the result into out.
func (s *server) herdrDo(ctx context.Context, method string, params, out any) error {
	raw, err := s.herdrCall(ctx, method, params)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("herdr %s: %w", method, err)
	}
	return nil
}

// herdrSubscribe opens an events.subscribe stream and calls each with every
// event until the stream ends, each returns an error, or ctx ends. started
// runs once the server has accepted the subscription.
func (s *server) herdrSubscribe(ctx context.Context, subs []map[string]any, started func(), each func(event string, data json.RawMessage) error) error {
	conn, err := s.herdrDial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	req, _ := herdrRequest("sub", "events.subscribe", map[string]any{"subscriptions": subs})
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(req); err != nil {
		return err
	}
	conn.SetWriteDeadline(time.Time{})
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	first := true
	for sc.Scan() {
		if first {
			first = false
			if _, err := herdrReply(sc.Bytes()); err != nil {
				return err
			}
			if started != nil {
				started()
			}
			continue
		}
		var ev struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Event == "" {
			continue
		}
		if err := each(ev.Event, ev.Data); err != nil {
			return err
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return errors.New("herdr closed the event stream")
}

// The parts of herdr's answers used here.

type herdrPaneInfo struct {
	PaneID        string  `json:"pane_id"`
	TabID         string  `json:"tab_id"`
	WorkspaceID   string  `json:"workspace_id"`
	Cwd           *string `json:"cwd"`
	ForegroundCwd *string `json:"foreground_cwd"`
	Agent         *string `json:"agent"`
	AgentStatus   string  `json:"agent_status"`
	Title         *string `json:"terminal_title"`
	Scroll        *struct {
		ViewportRows int `json:"viewport_rows"`
	} `json:"scroll"`
}

type herdrSnapshot struct {
	FocusedWorkspaceID *string         `json:"focused_workspace_id"`
	Workspaces         []herdrSpaceRow `json:"workspaces"`
	Tabs               []struct {
		TabID       string `json:"tab_id"`
		WorkspaceID string `json:"workspace_id"`
		Label       string `json:"label"`
	} `json:"tabs"`
	Panes  []herdrPaneInfo `json:"panes"`
	Agents []struct {
		PaneID      string            `json:"pane_id"`
		Agent       *string           `json:"agent"`
		AgentStatus string            `json:"agent_status"`
		Title       *string           `json:"title"`
		StateLabels map[string]string `json:"state_labels"`
	} `json:"agents"`
}

type herdrSpaceRow struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
	ActiveTabID string `json:"active_tab_id"`
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// herdrSnapshotRaw: session.snapshot's snapshot object, as herdr sent it.
func (s *server) herdrSnapshotRaw(ctx context.Context) (json.RawMessage, error) {
	var r struct {
		Snapshot json.RawMessage `json:"snapshot"`
	}
	if err := s.herdrDo(ctx, "session.snapshot", nil, &r); err != nil {
		return nil, err
	}
	if len(r.Snapshot) == 0 {
		return nil, errors.New("herdr session.snapshot without a snapshot")
	}
	return r.Snapshot, nil
}

func (s *server) herdrSnapshot(ctx context.Context) (herdrSnapshot, error) {
	var snap herdrSnapshot
	raw, err := s.herdrSnapshotRaw(ctx)
	if err != nil {
		return snap, err
	}
	return snap, json.Unmarshal(raw, &snap)
}

// where: "<workspace label> / <tab label>" of each pane, as hc's labels read.
func (snap herdrSnapshot) where() map[string]string {
	spaces := map[string]string{}
	for _, w := range snap.Workspaces {
		spaces[w.WorkspaceID] = w.Label
	}
	tabs := map[string]string{}
	for _, t := range snap.Tabs {
		tabs[t.TabID] = t.Label
	}
	out := map[string]string{}
	for _, p := range snap.Panes {
		out[p.PaneID] = spaces[p.WorkspaceID] + " / " + tabs[p.TabID]
	}
	return out
}

func (s *server) herdrPaneGet(ctx context.Context, pane string) (herdrPaneInfo, error) {
	var r struct {
		Pane herdrPaneInfo `json:"pane"`
	}
	err := s.herdrDo(ctx, "pane.get", map[string]any{"pane_id": pane}, &r)
	return r.Pane, err
}

// herdrRead: a pane's text (format text or ansi) from source.
func (s *server) herdrRead(ctx context.Context, pane, source, format string, lines int) (text string, truncated bool, err error) {
	params := map[string]any{"pane_id": pane, "source": source, "format": format, "strip_ansi": format != "ansi"}
	if lines > 0 {
		params["lines"] = lines
	}
	var r struct {
		Read struct {
			Text      string `json:"text"`
			Truncated bool   `json:"truncated"`
		} `json:"read"`
	}
	if err := s.herdrDo(ctx, "pane.read", params, &r); err != nil {
		return "", false, err
	}
	return r.Read.Text, r.Read.Truncated, nil
}
