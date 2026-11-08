package main

// /api/attach for a herdr CLI pane ("herdr:wF:p2"): herdr has no byte
// stream of a pane, so the live terminal is a repaint stream of snapshots.
//
//   - On connect: {"type":"size","cols":C,"rows":R,"snapshot":true}, then one
//     binary frame with the pane's history (herdr's recent lines above the
//     screen, so they land in the phone terminal's scrollback) and its screen.
//   - Then, whenever the screen changed: one binary frame that repaints it
//     whole: hide the cursor, home, erase the display, the visible rows as
//     ANSI (SGR colors and styles) joined by CRLF, the cursor parked at the end
//     of the last row with text. herdr's API has no cursor position, so the
//     cursor stays hidden.
//   - The screen is read on pane.updated events for the pane, right after
//     the phone typed, and every herdrPoll otherwise (herdr sends no event
//     for new output); a frame goes out only when the screen differs.
//   - Size: rows are the pane's viewport rows; cols the widest row (at least
//     80; herdr doesn't tell a pane's columns), never shrinking. When either
//     grows: {"type":"size",…,"repaint":true} and the history+screen frame
//     again (the phone resets its terminal for a repaint).
//   - Phone → server: binary frames are typed: printable text through
//     pane.send_text; Enter, Tab, Backspace, Esc, arrows, Shift-Tab and
//     Ctrl-letters as herdr's named keys (so herdr encodes them as the
//     program asked); other escape sequences (Home, End, PgUp, F-keys, Alt-x)
//     as their bytes; mouse reports are dropped. "resize" is ignored (the
//     herdr TUI owns the pane's size) and so is "reply".
//   - {"type":"exit","reason":…} when the pane closes or herdr stops answering.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
)

// Variables for the tests.
var (
	herdrPoll     = 300 * time.Millisecond // between screen reads without events
	herdrThrottle = 50 * time.Millisecond  // at least this between two reads
)

// ansiRe: escape sequences (CSI, OSC, single-character), to measure rows.
var ansiRe = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[@-Z\\-_])`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// cellWidth: columns a rune takes (wide East Asian and emoji 2, combining 0).
func cellWidth(r rune) int {
	switch {
	case r == 0 || r < 32 || (r >= 0x7f && r < 0xa0):
		return 0
	case r >= 0x300 && r <= 0x36f, r >= 0x200b && r <= 0x200f, r >= 0xfe00 && r <= 0xfe0f:
		return 0
	case r >= 0x1100 && r <= 0x115f, r >= 0x2e80 && r <= 0xa4cf, r >= 0xac00 && r <= 0xd7a3,
		r >= 0xf900 && r <= 0xfaff, r >= 0xfe30 && r <= 0xfe4f, r >= 0xff00 && r <= 0xff60,
		r >= 0xffe0 && r <= 0xffe6, r >= 0x1f300 && r <= 0x1f64f, r >= 0x1f900 && r <= 0x1f9ff,
		r >= 0x20000 && r <= 0x3fffd:
		return 2
	}
	return 1
}

func textWidth(s string) int {
	n := 0
	for _, r := range s {
		n += cellWidth(r)
	}
	return n
}

// ansiLines: a pane.read text split in rows, without the CRs.
func ansiLines(text string) []string {
	if text == "" {
		return nil
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// herdrPaint: the frame that repaints a screen of rows rows.
func herdrPaint(lines []string, rows, cols int) []byte {
	if len(lines) > rows {
		lines = lines[len(lines)-rows:]
	}
	var b strings.Builder
	b.WriteString("\x1b[?25l\x1b[0m\x1b[H\x1b[2J")
	b.WriteString(strings.Join(lines, "\r\n"))
	b.WriteString("\x1b[0m")
	row, col := 1, 1
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimRight(stripANSI(lines[i]), " "); t != "" {
			row, col = i+1, textWidth(t)+1
			break
		}
	}
	if col > cols {
		col = cols
	}
	b.WriteString("\x1b[" + strconv.Itoa(row) + ";" + strconv.Itoa(col) + "H")
	return []byte(b.String())
}

// herdrHistory: the frame that puts history lines in the phone terminal's
// scrollback: written top-down, then scrolled off the screen.
func herdrHistory(history []string, rows int) []byte {
	if len(history) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("\x1b[0m\x1b[H\x1b[2J")
	for _, l := range history {
		b.WriteString(l)
		b.WriteString("\x1b[0m\r\n")
	}
	b.WriteString(strings.Repeat("\n", rows-1))
	return []byte(b.String())
}

// herdrHistoryOf: the rows of recent above the screen: recent ends with the
// screen's rows (trailing blank rows aside); anything else (a full-screen
// program on the alternate screen) has no history to give.
func herdrHistoryOf(recent, screen []string) []string {
	plain := func(l []string) []string {
		out := make([]string, len(l))
		for i, s := range l {
			out[i] = strings.TrimRight(stripANSI(s), " ")
		}
		return out
	}
	r, s := plain(recent), plain(screen)
	for len(s) > 0 && s[len(s)-1] == "" {
		s = s[:len(s)-1]
	}
	for len(r) > 0 && r[len(r)-1] == "" {
		r = r[:len(r)-1]
	}
	if len(s) == 0 || len(r) < len(s) {
		return nil
	}
	for i := range s {
		if r[len(r)-len(s)+i] != s[i] {
			return nil
		}
	}
	return recent[:len(r)-len(s)]
}

// herdrInputOf turns what the phone's terminal typed into herdr steps.
func herdrInputOf(data []byte) []herdrInput {
	var out []herdrInput
	text := func(s string) {
		if n := len(out); n > 0 && out[n-1].key == "" {
			out[n-1].text += s
			return
		}
		out = append(out, herdrInput{text: s})
	}
	key := func(k string) { out = append(out, herdrInput{key: k}) }
	s := string(data)
	for len(s) > 0 {
		c := s[0]
		switch {
		case c == 0x1b:
			n, k := escLen(s)
			seq := s[:n]
			s = s[n:]
			switch {
			case k == "drop":
			case k != "":
				key(k)
			default:
				text(seq)
			}
			continue
		case c == '\r':
			key("enter")
		case c == '\t':
			key("tab")
		case c == 0x7f:
			key("backspace")
		case c >= 1 && c <= 26 && c != '\n':
			key("ctrl+" + string(rune('a'+c-1)))
		case c < 32:
			text(string(c))
		default:
			r, n := utf8.DecodeRuneInString(s)
			if r == utf8.RuneError && n <= 1 {
				s = s[1:]
				continue
			}
			text(s[:n])
			s = s[n:]
			continue
		}
		s = s[1:]
	}
	return out
}

// escLen: how long the escape sequence at the start of s is, and the herdr
// key it is ("" to type its bytes, "drop" for mouse reports).
func escLen(s string) (int, string) {
	if len(s) == 1 || s[1] == 0x1b {
		return 1, "esc"
	}
	switch s[1] {
	case '[':
		if strings.HasPrefix(s, "\x1b[M") && len(s) >= 6 {
			return 6, "drop" // X10 mouse
		}
		i := 2
		for i < len(s) && s[i] >= 0x30 && s[i] <= 0x3f {
			i++
		}
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
			i++
		}
		if i >= len(s) {
			return len(s), ""
		}
		seq := s[:i+1]
		if strings.HasPrefix(seq, "\x1b[<") {
			return i + 1, "drop" // SGR mouse
		}
		switch seq {
		case "\x1b[A":
			return 3, "up"
		case "\x1b[B":
			return 3, "down"
		case "\x1b[C":
			return 3, "right"
		case "\x1b[D":
			return 3, "left"
		case "\x1b[Z":
			return 3, "shift+tab"
		}
		return i + 1, ""
	case 'O':
		if len(s) < 3 {
			return len(s), ""
		}
		switch s[2] {
		case 'A':
			return 3, "up"
		case 'B':
			return 3, "down"
		case 'C':
			return 3, "right"
		case 'D':
			return 3, "left"
		}
		return 3, ""
	}
	_, n := utf8.DecodeRuneInString(s[1:])
	return 1 + n, "" // Alt-x: ESC x
}

func (s *server) attachHerdr(w http.ResponseWriter, r *http.Request, terminal, pane string) {
	ctx0, cancel0 := context.WithTimeout(r.Context(), 5*time.Second)
	info, err := s.herdrPaneGet(ctx0, pane)
	cancel0()
	if err != nil {
		if herdrCode(err) == "pane_not_found" {
			writeErr(w, http.StatusNotFound, errors.New("that terminal isn't running"))
		} else {
			writeErr(w, http.StatusBadGateway, err)
		}
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go wsKeepAlive(ctx, conn) // through the tunnel too, where an idle socket is closed
	poll, throttle := herdrPoll, herdrThrottle

	var wmu sync.Mutex // one writer at a time: a size message and its frame stay together
	write := func(typ websocket.MessageType, b []byte) {
		if conn.Write(ctx, typ, b) != nil {
			cancel()
		}
	}
	sendJSON := func(v any) {
		b, _ := json.Marshal(v)
		write(websocket.MessageText, b)
	}
	var once sync.Once
	finish := func(reason string) {
		once.Do(func() {
			wmu.Lock()
			sendJSON(map[string]any{"type": "exit", "reason": reason})
			wmu.Unlock()
			cancel()
		})
	}

	var (
		mu    sync.Mutex
		rows  = 24
		cols  = 80
		shown string // the screen last sent
		sized bool
		fails int
	)
	if info.Scroll != nil && info.Scroll.ViewportRows > 0 {
		rows = info.Scroll.ViewportRows
	}

	// refresh reads the screen and sends what changed.
	var reading sync.Mutex
	var lastRead time.Time
	refresh := func() {
		reading.Lock()
		defer reading.Unlock()
		if d := time.Until(lastRead.Add(throttle)); d > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(d):
			}
		}
		lastRead = time.Now()
		rctx, rcancel := context.WithTimeout(ctx, 5*time.Second)
		defer rcancel()
		text, _, err := s.herdrRead(rctx, pane, "visible", "ansi", 0)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if herdrCode(err) == "pane_not_found" {
				finish("the program in this terminal has ended")
				return
			}
			if fails++; fails >= 10 {
				finish("herdr isn't answering: " + err.Error())
			}
			return
		}
		fails = 0
		lines := ansiLines(text)
		mu.Lock()
		if text == shown && sized {
			mu.Unlock()
			return
		}
		shown = text
		wide := cols
		for _, l := range lines {
			if n := textWidth(stripANSI(l)); n > wide {
				wide = n
			}
		}
		if wide > 400 {
			wide = 400
		}
		high := rows
		if len(lines) > high {
			high = len(lines)
		}
		grew := !sized || wide != cols || high != rows
		first := !sized
		cols, rows, sized = wide, high, true
		c, h := cols, rows
		mu.Unlock()

		wmu.Lock()
		defer wmu.Unlock()
		if grew {
			// New size: the phone resets its terminal; history goes in again.
			var hist []string
			if recent, _, err := s.herdrRead(rctx, pane, "recent", "ansi", 1000); err == nil {
				hist = herdrHistoryOf(ansiLines(recent), lines)
			}
			msg := map[string]any{"type": "size", "cols": c, "rows": h, "snapshot": true}
			if !first {
				msg["repaint"] = true
			}
			sendJSON(msg)
			write(websocket.MessageBinary, append(herdrHistory(hist, h), herdrPaint(lines, h, c)...))
			return
		}
		write(websocket.MessageBinary, herdrPaint(lines, h, c))
	}

	kick := make(chan struct{}, 1)
	poke := func() {
		select {
		case kick <- struct{}{}:
		default:
		}
	}
	soon := func() { // after typing: the echo, then the program's answer
		go func() {
			for _, d := range []time.Duration{30, 120, 300, 700} {
				select {
				case <-ctx.Done():
					return
				case <-time.After(d * time.Millisecond):
					poke()
				}
			}
		}()
	}

	refresh()
	log.Printf("attach %s (herdr repaint stream, %dx%d)", terminal, cols, rows)

	// The reader: on a kick, or every herdrPoll; the pane's rows every 2 s.
	go func() {
		t := time.NewTicker(poll)
		defer t.Stop()
		lastGet := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-kick:
			case <-t.C:
			}
			if time.Since(lastGet) > 2*time.Second {
				lastGet = time.Now()
				gctx, gcancel := context.WithTimeout(ctx, 3*time.Second)
				if p, err := s.herdrPaneGet(gctx, pane); err == nil && p.Scroll != nil && p.Scroll.ViewportRows > 0 {
					mu.Lock()
					if p.Scroll.ViewportRows != rows {
						rows, sized = p.Scroll.ViewportRows, false
						shown = ""
					}
					mu.Unlock()
				}
				gcancel()
			}
			refresh()
		}
	}()

	// herdr's events for the pane: changed (title, scroll, size), gone.
	go func() {
		subs := []map[string]any{
			{"type": "pane.updated"}, {"type": "pane.closed"}, {"type": "pane.exited"},
			{"type": "pane.scroll_changed", "pane_id": pane},
		}
		err := s.herdrSubscribe(ctx, subs, nil, func(event string, data json.RawMessage) error {
			var d struct {
				PaneID string         `json:"pane_id"`
				Pane   *herdrPaneInfo `json:"pane"`
			}
			if json.Unmarshal(data, &d) != nil {
				return nil
			}
			if d.Pane != nil {
				d.PaneID = d.Pane.PaneID
			}
			if d.PaneID != pane {
				return nil
			}
			switch strings.ReplaceAll(event, ".", "_") {
			case "pane_closed", "pane_exited":
				finish("the program in this terminal has ended")
			default:
				if d.Pane != nil && d.Pane.Scroll != nil && d.Pane.Scroll.ViewportRows > 0 {
					mu.Lock()
					if d.Pane.Scroll.ViewportRows != rows {
						rows, sized = d.Pane.Scroll.ViewportRows, false
						shown = ""
					}
					mu.Unlock()
				}
				poke()
			}
			return nil
		})
		if err != nil && ctx.Err() == nil {
			log.Printf("attach %s: herdr events: %v (polling only)", terminal, err)
		}
	}()

	// Phone → pane, in order.
	typed := make(chan []herdrInput, 64)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case steps := <-typed:
				tctx, tcancel := context.WithTimeout(ctx, 10*time.Second)
				if err := s.herdrType(tctx, pane, steps); err != nil && ctx.Err() == nil {
					log.Printf("attach %s: typing: %v", terminal, err)
					if herdrCode(err) == "pane_not_found" {
						finish("the program in this terminal has ended")
					}
				}
				tcancel()
				soon()
			}
		}
	}()
	go func() {
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				cancel()
				return
			}
			if typ != websocket.MessageBinary {
				continue // resize (herdr's TUI owns the size), reply: nothing to do
			}
			if steps := herdrInputOf(data); len(steps) > 0 {
				select {
				case typed <- steps:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	<-ctx.Done()
	conn.Close(websocket.StatusNormalClosure, "")
	log.Printf("detach %s", terminal)
}
