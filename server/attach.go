package main

// A live terminal for the phone: /api/attach runs `hc attach` in a PTY and
// relays its bytes over a WebSocket, so the phone's terminal emulator shows
// exactly what the Mac's does (colors, cursor, full-screen programs) and
// what you type goes straight to the program.
//
// Size: hc attach paints the core terminal at its own size, so the phone
// mirrors that size (it is sent as {"type":"size"} whenever it changes).
// The PTY carries the phone's own size; herdr-core gives the terminal to
// whoever typed last, so once you type on the phone the program reflows to
// the phone, and when you type on the Mac it goes back.
//
// Frames: binary = terminal bytes (both ways); text = JSON control:
//   phone → server  {"type":"resize","cols":60,"rows":30}
//   server → phone  {"type":"size","cols":165,"rows":36}
//                   {"type":"exit","reason":"…"}

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

const detachKey = 0x1c // ctrl+\, hc attach's detach

type termSize struct{ Cols, Rows int }

// coreSize asks herdr-core how big the terminal is now, and whether it lives.
func (s *server) coreSize(ctx context.Context, terminal string) (termSize, bool, error) {
	out, err := run(ctx, 5*time.Second, s.hc, "get", "--json", terminal)
	if err != nil {
		return termSize{}, false, err
	}
	var g struct {
		Cols, Rows int
		State      string
	}
	if err := json.Unmarshal(out, &g); err != nil {
		return termSize{}, false, err
	}
	return termSize{g.Cols, g.Rows}, g.State == "live", nil
}

func clampSize(v string, def, lo, hi int) int {
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		return def
	}
	return n
}

func (s *server) attach(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	terminal := q.Get("terminal")
	if !validTerminal(terminal) {
		writeErr(w, http.StatusBadRequest, errors.New("bad terminal"))
		return
	}
	size, live, err := s.coreSize(r.Context(), terminal)
	if err != nil || !live {
		writeErr(w, http.StatusNotFound, errors.New("that terminal isn't running"))
		return
	}
	phone := termSize{clampSize(q.Get("cols"), 80, 20, 400), clampSize(q.Get("rows"), 24, 5, 200)}

	// The token rides in the query (a WebView can't set WebSocket headers),
	// and the page's origin is file://, so there is no origin to check.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sendJSON := func(v any) {
		b, _ := json.Marshal(v)
		conn.Write(ctx, websocket.MessageText, b)
	}
	// The phone sizes its terminal before the first paint arrives.
	sendJSON(map[string]any{"type": "size", "cols": size.Cols, "rows": size.Rows})

	cmd := exec.Command(s.hc, "attach", "--history", "1000", terminal)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(phone.Cols), Rows: uint16(phone.Rows)})
	if err != nil {
		sendJSON(map[string]any{"type": "exit", "reason": "hc attach didn't start: " + err.Error()})
		conn.Close(websocket.StatusInternalError, "attach failed")
		return
	}
	log.Printf("attach %s (phone %dx%d, terminal %dx%d)", terminal, phone.Cols, phone.Rows, size.Cols, size.Rows)

	var once sync.Once
	exited := make(chan struct{})
	finish := func(reason string) {
		once.Do(func() {
			sendJSON(map[string]any{"type": "exit", "reason": reason})
			cancel()
		})
	}
	go func() {
		cmd.Wait()
		close(exited)
		finish("detached")
	}()

	// Terminal → phone.
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				if conn.Write(ctx, websocket.MessageBinary, buf[:n]) != nil {
					cancel()
					return
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					finish("the terminal stream ended")
				}
				return
			}
		}
	}()

	// The core terminal's size, as it changes (the Mac or the phone typed).
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		last := size
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				now, live, err := s.coreSize(ctx, terminal)
				if err == nil && !live {
					finish("the program in this terminal has ended")
					return
				}
				if err == nil && now != last {
					last = now
					sendJSON(map[string]any{"type": "size", "cols": now.Cols, "rows": now.Rows})
				}
			}
		}
	}()

	// Phone → terminal.
	go func() {
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				cancel()
				return
			}
			if typ == websocket.MessageBinary {
				ptmx.Write(data)
				continue
			}
			var msg struct {
				Type       string
				Cols, Rows int
			}
			if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" && msg.Cols >= 20 && msg.Rows >= 5 && msg.Cols <= 400 && msg.Rows <= 200 {
				pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(msg.Cols), Rows: uint16(msg.Rows)})
			}
		}
	}()

	<-ctx.Done()
	// Detach politely, then make sure hc attach is gone.
	ptmx.Write([]byte{detachKey})
	select {
	case <-exited:
	case <-time.After(1500 * time.Millisecond):
		cmd.Process.Kill()
		<-exited
	}
	ptmx.Close()
	conn.Close(websocket.StatusNormalClosure, "")
	log.Printf("detach %s", terminal)
}
