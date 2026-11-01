package main

// What the handlers ask of a terminal, answered by hc (the Herdr app's
// terminals: "t12", "box/t12") or by the herdr CLI's server (its panes:
// "herdr:wF:p2"). The hc side runs exactly the hc commands the handlers
// ran before; the herdr side speaks herdr's socket API (herdr.go).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type terminalBackend interface {
	// read: /api/read's answer, JSON with the text of the screen and up to
	// lines of history above it.
	read(ctx context.Context, terminal string, lines int) ([]byte, error)
	// screen: what the terminal shows now, as plain text.
	screen(ctx context.Context, terminal string, timeout time.Duration) (string, error)
	// send pastes text and presses Enter; force types even into a dialog.
	send(ctx context.Context, terminal, text string, force bool) ([]byte, error)
	// keys presses named keys (hc's names: enter, esc, up, ctrl+c, …).
	keys(ctx context.Context, terminal string, keys []string, timeout time.Duration) ([]byte, error)
	// interrupt presses the stop key.
	interrupt(ctx context.Context, terminal string) ([]byte, error)
	// now: what runs in the terminal and the state it reports.
	now(ctx context.Context, terminal string) (terminalNow, error)
	// put places a local file where the program in the terminal can open it.
	put(ctx context.Context, terminal, file string) (path string, n int64, err error)
}

// backend: who answers for a terminal.
func (s *server) backend(terminal string) terminalBackend {
	if _, ok := herdrPane(terminal); ok {
		return herdrBackend{s}
	}
	return hcBackend{s}
}

// hcBackend: hc's commands.
type hcBackend struct{ s *server }

func (b hcBackend) read(ctx context.Context, t string, lines int) ([]byte, error) {
	return run(ctx, 10*time.Second, b.s.hc, "read", "--json", "--lines", strconv.Itoa(lines), t)
}

func (b hcBackend) screen(ctx context.Context, t string, timeout time.Duration) (string, error) {
	out, err := run(ctx, timeout, b.s.hc, "read", "--json", t)
	if err != nil {
		return "", err
	}
	var r struct{ Text string }
	if err := json.Unmarshal(out, &r); err != nil {
		return "", err
	}
	return r.Text, nil
}

func (b hcBackend) send(ctx context.Context, t, text string, force bool) ([]byte, error) {
	args := []string{"send", "--json"}
	if force {
		args = append(args, "--force")
	}
	return run(ctx, 40*time.Second, b.s.hc, append(args, t, text)...)
}

func (b hcBackend) keys(ctx context.Context, t string, keys []string, timeout time.Duration) ([]byte, error) {
	return run(ctx, timeout, b.s.hc, append([]string{"keys", "--json", t}, keys...)...)
}

func (b hcBackend) interrupt(ctx context.Context, t string) ([]byte, error) {
	return run(ctx, 15*time.Second, b.s.hc, "interrupt", "--json", t)
}

func (b hcBackend) now(ctx context.Context, t string) (terminalNow, error) {
	out, err := run(ctx, 5*time.Second, b.s.hc, "get", "--json", t)
	if err != nil {
		return terminalNow{}, err
	}
	var g struct {
		Cwd        string
		Foreground *foregroundInfo
		Status     *struct{ App, State string }
	}
	if err := json.Unmarshal(out, &g); err != nil {
		return terminalNow{}, err
	}
	var n terminalNow
	n.fg.Cwd = g.Cwd
	if g.Foreground != nil {
		n.fg = *g.Foreground
		if n.fg.Cwd == "" {
			n.fg.Cwd = g.Cwd
		}
	}
	if g.Status != nil {
		if n.fg.Name == "" {
			n.fg.Name = g.Status.App
		}
		n.state = g.Status.State
	}
	return n, nil
}

func (b hcBackend) put(ctx context.Context, t, file string) (string, int64, error) {
	out, err := run(ctx, 10*time.Minute, b.s.hc, "put", "--json", t, file)
	if err != nil {
		return "", 0, fmt.Errorf("hc put: %w", err)
	}
	var put struct {
		Path  string `json:"path"`
		Bytes int64  `json:"bytes"`
	}
	if json.Unmarshal(out, &put) != nil || put.Path == "" {
		return "", 0, errors.New("hc put didn't say where the file landed")
	}
	return put.Path, put.Bytes, nil
}

// herdrBackend: the herdr CLI's socket API.
type herdrBackend struct{ s *server }

func (b herdrBackend) pane(t string) (string, error) {
	p, ok := herdrPane(t)
	if !ok {
		return "", fmt.Errorf("%q isn't a herdr pane", t)
	}
	return p, nil
}

func herdrJSON(v any) []byte {
	out, _ := json.Marshal(v)
	return out
}

// read: plain text like hc read's: the screen alone (lines 0), else source
// recent with lines more than the screen's rows.
func (b herdrBackend) read(ctx context.Context, t string, lines int) ([]byte, error) {
	pane, err := b.pane(t)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	source, n := "visible", 0
	if lines > 0 {
		info, err := b.s.herdrPaneGet(ctx, pane)
		if err != nil {
			return nil, err
		}
		rows := 50
		if info.Scroll != nil && info.Scroll.ViewportRows > 0 {
			rows = info.Scroll.ViewportRows
		}
		source, n = "recent", lines+rows
	}
	text, truncated, err := b.s.herdrRead(ctx, pane, source, "text", n)
	if err != nil {
		return nil, err
	}
	return herdrJSON(map[string]any{"text": text, "complete": !truncated, "how": "herdr " + source}), nil
}

func (b herdrBackend) screen(ctx context.Context, t string, timeout time.Duration) (string, error) {
	pane, err := b.pane(t)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	text, _, err := b.s.herdrRead(ctx, pane, "visible", "text", 0)
	return text, err
}

// send: an agent herdr recognizes takes the text through agent.prompt
// (bracketed paste when the agent asked for it, then Enter, refused while
// it shows a dialog); anything else, and a forced reply into a dialog, gets
// the text typed and Enter pressed.
func (b herdrBackend) send(ctx context.Context, t, text string, force bool) ([]byte, error) {
	pane, err := b.pane(t)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	if !force {
		err := b.s.herdrDo(ctx, "agent.prompt", map[string]any{"target": pane, "text": text}, nil)
		switch code := herdrCode(err); {
		case err == nil:
			return herdrJSON(map[string]any{"ok": true, "terminal": t, "how": "prompt"}), nil
		case code == "agent_blocked":
			return nil, errors.New("the agent is waiting for an answer; nothing was typed")
		case code == "":
			return nil, err // herdr didn't answer
		}
		// Not an agent (or not one in the foreground): type it.
	}
	if err := b.s.herdrDo(ctx, "pane.send_input", map[string]any{"pane_id": pane, "text": text, "keys": []string{"enter"}}, nil); err != nil {
		return nil, err
	}
	return herdrJSON(map[string]any{"ok": true, "terminal": t, "how": "typed"}), nil
}

func (b herdrBackend) keys(ctx context.Context, t string, keys []string, timeout time.Duration) ([]byte, error) {
	pane, err := b.pane(t)
	if err != nil {
		return nil, err
	}
	steps := make([]herdrInput, 0, len(keys))
	sent := make([]string, 0, len(keys))
	for _, k := range keys {
		in := herdrKey(k)
		steps = append(steps, in)
		if in.key != "" {
			sent = append(sent, in.key)
		} else {
			sent = append(sent, strconv.Quote(in.text))
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := b.s.herdrType(ctx, pane, steps); err != nil {
		return nil, err
	}
	return herdrJSON(map[string]any{"ok": true, "terminal": t, "keys": sent}), nil
}

// interrupt: Esc stops an agent's turn (and Ctrl-C would quit some of them
// when idle); anything else gets Ctrl-C.
func (b herdrBackend) interrupt(ctx context.Context, t string) ([]byte, error) {
	pane, err := b.pane(t)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	info, err := b.s.herdrPaneGet(ctx, pane)
	if err != nil {
		return nil, err
	}
	key := "ctrl+c"
	if str(info.Agent) != "" {
		key = "esc"
	}
	if err := b.s.herdrDo(ctx, "pane.send_keys", map[string]any{"pane_id": pane, "keys": []string{key}}, nil); err != nil {
		return nil, err
	}
	return herdrJSON(map[string]any{"ok": true, "terminal": t, "key": key}), nil
}

// herdrProcess is one of pane.process_info's foreground processes.
type herdrProcess struct {
	Pid  int      `json:"pid"`
	Name string   `json:"name"`
	Argv []string `json:"argv"`
	Cwd  *string  `json:"cwd"`
}

// herdrForeground picks the program in front: the one an agent is (a
// runtime like node running claude's script), else the group's leader, else
// the first.
func herdrForeground(procs []herdrProcess, group int) (herdrProcess, bool) {
	if len(procs) == 0 {
		return herdrProcess{}, false
	}
	for _, p := range procs {
		if agentOf(foregroundInfo{Name: p.Name, Argv: p.Argv}) != "" {
			return p, true
		}
	}
	for _, p := range procs {
		if p.Pid == group {
			return p, true
		}
	}
	return procs[0], true
}

func (b herdrBackend) now(ctx context.Context, t string) (terminalNow, error) {
	pane, err := b.pane(t)
	if err != nil {
		return terminalNow{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	info, err := b.s.herdrPaneGet(ctx, pane)
	if err != nil {
		return terminalNow{}, err
	}
	var pi struct {
		ProcessInfo struct {
			Group *int           `json:"foreground_process_group_id"`
			Procs []herdrProcess `json:"foreground_processes"`
		} `json:"process_info"`
	}
	if err := b.s.herdrDo(ctx, "pane.process_info", map[string]any{"pane_id": pane}, &pi); err != nil {
		return terminalNow{}, err
	}
	group := 0
	if pi.ProcessInfo.Group != nil {
		group = *pi.ProcessInfo.Group
	}
	var n terminalNow
	if p, ok := herdrForeground(pi.ProcessInfo.Procs, group); ok {
		n.fg = foregroundInfo{Name: p.Name, Argv: p.Argv, Cwd: str(p.Cwd), Pid: p.Pid}
	}
	if n.fg.Name == "" {
		n.fg.Name = str(info.Agent)
	}
	if c := str(info.ForegroundCwd); c != "" {
		n.fg.Cwd = c
	} else if n.fg.Cwd == "" {
		n.fg.Cwd = str(info.Cwd)
	}
	n.state = herdrState(info.AgentStatus)
	return n, nil
}

// put: herdr has no file transfer, and its panes are on this Mac: the file
// goes under herdr-remote's uploads folder, a folder per pane.
func (b herdrBackend) put(ctx context.Context, t, file string) (string, int64, error) {
	pane, err := b.pane(t)
	if err != nil {
		return "", 0, err
	}
	base := b.s.uploadDir
	if base == "" {
		cfg, err := os.UserConfigDir()
		if err != nil {
			return "", 0, err
		}
		base = filepath.Join(cfg, "herdr-remote", "uploads")
	}
	dir := filepath.Join(base, strings.ReplaceAll(pane, ":", "-"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, err
	}
	name := filepath.Base(file)
	dst := filepath.Join(dir, name)
	if _, err := os.Lstat(dst); err == nil {
		dst = filepath.Join(dir, fmt.Sprintf("%d-%s", time.Now().UnixMilli(), name))
	}
	n, err := copyFile(file, dst)
	if err != nil {
		os.Remove(dst)
		return "", 0, err
	}
	return dst, n, nil
}

func copyFile(src, dst string) (int64, error) {
	if err := os.Rename(src, dst); err == nil {
		st, err := os.Stat(dst)
		if err != nil {
			return 0, err
		}
		return st.Size(), nil
	}
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return n, err
}

// herdrState: herdr's agent status as the events' state ("" for unknown:
// nothing to report).
func herdrState(st string) string {
	switch st {
	case "idle", "working", "blocked", "done":
		return st
	}
	return ""
}

// herdrInput is one step of typing: a named key herdr knows, or text
// (bytes) for the keys it has no name for.
type herdrInput struct{ key, text string }

// herdrKey translates a key name as the phone sends it (hc's names: enter,
// esc, tab, shifttab, backspace, up, pgup, ctrl+c, C-c, alt+x, f5, a, …)
// into herdr's send_keys names; keys herdr has no name for (home, end,
// pgup, pgdn, del, insert) become their xterm escape sequences.
func herdrKey(k string) herdrInput {
	if len(k) == 1 {
		return herdrInput{key: k}
	}
	l := strings.ToLower(k)
	switch l {
	case "enter", "return", "ret", "cr":
		return herdrInput{key: "enter"}
	case "esc", "escape":
		return herdrInput{key: "esc"}
	case "tab":
		return herdrInput{key: "tab"}
	case "shifttab", "shift+tab", "shift-tab", "s-tab", "btab", "backtab":
		return herdrInput{key: "shift+tab"}
	case "backspace", "bs", "bspace":
		return herdrInput{key: "backspace"}
	case "space", "spc":
		return herdrInput{key: "space"}
	case "up", "down", "left", "right":
		return herdrInput{key: l}
	case "home":
		return herdrInput{text: "\x1b[H"}
	case "end":
		return herdrInput{text: "\x1b[F"}
	case "pgup", "pageup", "page_up", "ppage":
		return herdrInput{text: "\x1b[5~"}
	case "pgdn", "pagedown", "page_down", "npage":
		return herdrInput{text: "\x1b[6~"}
	case "del", "delete", "dc":
		return herdrInput{text: "\x1b[3~"}
	case "insert", "ins", "ic":
		return herdrInput{text: "\x1b[2~"}
	}
	for _, p := range []string{"ctrl+", "ctrl-", "c-", "^"} {
		if rest, ok := strings.CutPrefix(l, p); ok && rest != "" {
			return herdrInput{key: "ctrl+" + rest}
		}
	}
	for _, p := range []string{"alt+", "alt-", "meta+", "m-"} {
		if rest, ok := strings.CutPrefix(l, p); ok && rest != "" {
			return herdrInput{key: "alt+" + rest}
		}
	}
	return herdrInput{key: l} // f5, …: herdr says if it doesn't know it
}

// herdrType sends steps in order: runs of keys through pane.send_keys, text
// through pane.send_text, and text followed by keys as one pane.send_input
// (herdr writes its text first).
func (s *server) herdrType(ctx context.Context, pane string, steps []herdrInput) error {
	for i := 0; i < len(steps); {
		var text strings.Builder
		for i < len(steps) && steps[i].key == "" {
			text.WriteString(steps[i].text)
			i++
		}
		var keys []string
		for i < len(steps) && steps[i].key != "" {
			keys = append(keys, steps[i].key)
			i++
		}
		var method string
		params := map[string]any{"pane_id": pane}
		switch {
		case text.Len() > 0 && len(keys) > 0:
			method, params["text"], params["keys"] = "pane.send_input", text.String(), keys
		case text.Len() > 0:
			method, params["text"] = "pane.send_text", text.String()
		case len(keys) > 0:
			method, params["keys"] = "pane.send_keys", keys
		default:
			continue
		}
		if err := s.herdrDo(ctx, method, params, nil); err != nil {
			return err
		}
	}
	return nil
}
