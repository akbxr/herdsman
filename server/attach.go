package main

// A live terminal for the phone: /api/attach runs `hc attach` in a PTY and
// relays its bytes over a WebSocket, so the phone's terminal emulator shows
// exactly what the Mac's does (colors, cursor, full-screen programs) and
// what you type goes straight to the program.
//
// Size: hc attach paints the core terminal at its own size, so the phone
// mirrors that size. The PTY carries the phone's own size; herdr-core gives
// the terminal to whoever typed last, so once you type on the phone the
// program reflows to the phone, and when you type on the Mac it goes back.
//
// When the core terminal changes size, herdr-core reflows its screen but
// hc attach sends no new paint, only what the program redraws; the phone's
// copy would drift (blank rows until the program redraws everything). So on
// every size change the server attaches again and sends that fresh, full
// paint, preceded by {"type":"size","repaint":true} so the phone resets
// its terminal to the new size first.
//
// Two attaches per phone, because attaching counts as being active: a
// re-attach for a fresh paint right after you typed on the Mac would take
// the terminal straight back to the phone's size.
//   display  `hc attach --observe`: what the phone shows; never types or
//            resizes; this one is replaced for each repaint.
//   control  a plain `hc attach`, made once: the phone's keys go through it,
//            so only typing on the phone gives the terminal the phone's size.
//
// Frames: binary = terminal bytes (both ways); text = JSON control:
//   phone → server  {"type":"resize","cols":60,"rows":30}
//                   {"type":"reply","data":"…"}  the phone terminal's answers
//                   to the display attach's queries (colors, device attributes)
//   server → phone  {"type":"size","cols":165,"rows":36,"repaint":bool}
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

// attachProc is one `hc attach` in its PTY.
type attachProc struct {
	cmd    *exec.Cmd
	ptmx   *os.File
	exited chan struct{}
}

func (s *server) startAttach(terminal string, phone termSize, display bool) (*attachProc, error) {
	args := []string{"attach", terminal}
	if display {
		args = []string{"attach", "--observe", "--history", "1000", terminal}
	}
	cmd := exec.Command(s.hc, args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(phone.Cols), Rows: uint16(phone.Rows)})
	if err != nil {
		return nil, err
	}
	p := &attachProc{cmd: cmd, ptmx: ptmx, exited: make(chan struct{})}
	go func() { cmd.Wait(); close(p.exited) }()
	return p, nil
}

// stop detaches politely, then makes sure hc attach is gone.
func (p *attachProc) stop() {
	p.ptmx.Write([]byte{detachKey})
	select {
	case <-p.exited:
	case <-time.After(1500 * time.Millisecond):
		p.cmd.Process.Kill()
		<-p.exited
	}
	p.ptmx.Close()
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
	var once sync.Once
	finish := func(reason string) {
		once.Do(func() {
			sendJSON(map[string]any{"type": "exit", "reason": reason})
			cancel()
		})
	}

	// The current hc attach. swap replaces it; mu guards cur, gen, phone, last.
	var (
		mu    sync.Mutex
		cur   *attachProc
		gen   int
		last  = size
		swaps []time.Time // recent re-attaches, to stop a size ping-pong
	)

	// pump relays one process's output while it is the current one.
	pump := func(p *attachProc, myGen int) {
		buf := make([]byte, 32<<10)
		for {
			n, err := p.ptmx.Read(buf)
			mu.Lock()
			current := myGen == gen
			mu.Unlock()
			if !current {
				return // replaced: its bytes would land on the new paint
			}
			if n > 0 && conn.Write(ctx, websocket.MessageBinary, buf[:n]) != nil {
				cancel()
				return
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					finish("the terminal stream ended")
				} else {
					finish("detached")
				}
				return
			}
		}
	}

	// swap attaches again for a fresh paint at the core's new size. The size
	// message goes out before the new process can write a byte.
	swap := func(now termSize) {
		mu.Lock()
		cutoff := time.Now().Add(-5 * time.Second)
		for len(swaps) > 0 && swaps[0].Before(cutoff) {
			swaps = swaps[1:]
		}
		if len(swaps) >= 6 { // something keeps resizing it: just follow the size
			last = now
			mu.Unlock()
			sendJSON(map[string]any{"type": "size", "cols": now.Cols, "rows": now.Rows})
			return
		}
		swaps = append(swaps, time.Now())
		old := cur
		gen++ // the old pump stops forwarding from here
		myGen := gen
		last = now
		ph := phone
		mu.Unlock()

		log.Printf("repaint %s at %dx%d", terminal, now.Cols, now.Rows)
		sendJSON(map[string]any{"type": "size", "cols": now.Cols, "rows": now.Rows, "repaint": true})
		p, err := s.startAttach(terminal, ph, true)
		if err != nil {
			finish("hc attach didn't start: " + err.Error())
			return
		}
		mu.Lock()
		cur = p
		mu.Unlock()
		go pump(p, myGen)
		if old != nil {
			go old.stop() // after the new one is on: the terminal never loses its viewer
		}
	}

	// checkSize re-attaches when the core terminal's size changed.
	var checking sync.Mutex
	checkSize := func() {
		if !checking.TryLock() {
			return
		}
		defer checking.Unlock()
		now, live, err := s.coreSize(ctx, terminal)
		if err != nil {
			return
		}
		if !live {
			finish("the program in this terminal has ended")
			return
		}
		mu.Lock()
		changed := now != last
		mu.Unlock()
		if changed {
			swap(now)
		}
	}

	// The control attach: the phone's keys, nothing shown from it.
	control, err := s.startAttach(terminal, phone, false)
	if err != nil {
		finish("hc attach didn't start: " + err.Error())
		conn.Close(websocket.StatusInternalError, "attach failed")
		return
	}
	go func() {
		io.Copy(io.Discard, control.ptmx) // drained; ends when it exits
		finish("detached")
	}()

	// First display attach. The phone sizes its terminal before its paint arrives.
	sendJSON(map[string]any{"type": "size", "cols": size.Cols, "rows": size.Rows})
	p, err := s.startAttach(terminal, phone, true)
	if err != nil {
		finish("hc attach didn't start: " + err.Error())
		control.stop()
		conn.Close(websocket.StatusInternalError, "attach failed")
		return
	}
	mu.Lock()
	cur = p
	mu.Unlock()
	go pump(p, 0)
	log.Printf("attach %s (phone %dx%d, terminal %dx%d)", terminal, phone.Cols, phone.Rows, size.Cols, size.Rows)

	// The core terminal's size, as it changes (the Mac or the phone typed).
	go func() {
		t := time.NewTicker(700 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				checkSize()
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
				control.ptmx.Write(data) // keys: to the program
				continue
			}
			mu.Lock()
			display := cur
			mu.Unlock()
			var msg struct {
				Type       string
				Cols, Rows int
				Data       string
			}
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			if msg.Type == "reply" && len(msg.Data) <= 4096 {
				display.ptmx.Write([]byte(msg.Data)) // answers to the display attach's own queries
				continue
			}
			if msg.Type == "resize" && msg.Cols >= 20 && msg.Rows >= 5 && msg.Cols <= 400 && msg.Rows <= 200 {
				mu.Lock()
				phone = termSize{msg.Cols, msg.Rows}
				mu.Unlock()
				ws := &pty.Winsize{Cols: uint16(msg.Cols), Rows: uint16(msg.Rows)}
				pty.Setsize(control.ptmx, ws)
				pty.Setsize(display.ptmx, ws)
				// If the phone has the terminal, the core follows soon: catch it
				// quickly instead of at the next tick.
				go func() {
					for _, d := range []time.Duration{120, 350} {
						select {
						case <-ctx.Done():
							return
						case <-time.After(d * time.Millisecond):
							checkSize()
						}
					}
				}()
			}
		}
	}()

	<-ctx.Done()
	mu.Lock()
	gen++
	p = cur
	mu.Unlock()
	var wg sync.WaitGroup
	for _, a := range []*attachProc{p, control} {
		wg.Add(1)
		go func(a *attachProc) { defer wg.Done(); a.stop() }(a)
	}
	wg.Wait()
	conn.Close(websocket.StatusNormalClosure, "")
	log.Printf("detach %s", terminal)
}
