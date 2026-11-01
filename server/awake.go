package main

// Keeps the Mac from going to sleep on its own while an agent works or waits
// for you, so the phone can still reach it. A sleeping Mac answers nothing:
// no terminal, no notifications.
//
// It runs `caffeinate -i` (no idle sleep; the display may still turn off)
// while any terminal reports working or blocked, and for a while after the
// last one stops, so you can still answer a "finished" notification. Closing
// the lid of a laptop still sleeps it; caffeinate can't stop that.

import (
	"context"
	"log"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// awakeLinger is how long the Mac stays awake after the last agent stops.
const awakeLinger = 10 * time.Minute

type keepAwake struct {
	hub    *eventHub
	bin    string        // caffeinate
	linger time.Duration // awakeLinger; shorter in tests

	mu    sync.Mutex
	cmd   *exec.Cmd
	until time.Time // stay awake until then even when nothing is busy
}

// busy says whether any terminal reports working or blocked.
func (h *eventHub) busy() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range h.current {
		if e.State == "working" || e.State == "blocked" {
			return true
		}
	}
	return false
}

// run follows the hub for the life of the server.
func (k *keepAwake) run(ctx context.Context) {
	defer k.set(false)
	for {
		_, _, _, wake := k.hub.since(0)
		now := time.Now()
		busy := k.hub.busy()
		if busy {
			k.until = now.Add(k.linger)
		}
		k.set(busy || now.Before(k.until))
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-time.After(30 * time.Second): // for the linger to run out
		}
	}
}

// on says whether caffeinate is running now.
func (k *keepAwake) on() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.cmd != nil
}

func (k *keepAwake) set(on bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	switch {
	case on && k.cmd == nil:
		// -w: caffeinate ends with this server, even if it is killed.
		cmd := exec.Command(k.bin, "-i", "-w", strconv.Itoa(os.Getpid()))
		if err := cmd.Start(); err != nil {
			log.Printf("awake: %v", err)
			return
		}
		k.cmd = cmd
		go cmd.Wait()
		log.Printf("awake: keeping the Mac awake while agents work")
	case !on && k.cmd != nil:
		k.cmd.Process.Kill()
		k.cmd = nil
		log.Printf("awake: the Mac may sleep again")
	}
}
