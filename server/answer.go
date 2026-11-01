package main

// Answers an agent that waits for you, from a notification: allow, deny, or
// a typed reply, without opening the terminal. An agent that finished (or
// failed) takes a reply too: your next instruction.
//
// Agents (tego, Claude Code, Codex) ask for approval with a picker whose
// default is "yes": Enter allows, Esc declines. A reply is pasted and
// submitted with Enter.
//
// It only types while the terminal is still where the notification was about:
// the same event (seq), still blocked (or done). A prompt answered on the Mac
// meanwhile, or a newer one, is left alone.

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

var errPromptGone = errors.New("That prompt is gone: it was answered or changed on your Mac.")

func (s *server) answer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Terminal string `json:"terminal"`
		Seq      int64  `json:"seq"`    // the event the notification showed; 0 = whatever waits now
		Answer   string `json:"answer"` // allow, deny, reply
		Text     string `json:"text"`
	}
	if err := decode(r, &req); err != nil || !validTerminal(req.Terminal) {
		writeErr(w, http.StatusBadRequest, errors.New("need {terminal, answer}"))
		return
	}
	if req.Answer == "reply" && strings.TrimSpace(req.Text) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("need text"))
		return
	}
	if req.Answer != "allow" && req.Answer != "deny" && req.Answer != "reply" {
		writeErr(w, http.StatusBadRequest, errors.New("answer is allow, deny or reply"))
		return
	}

	s.events.mu.Lock()
	cur, ok := s.events.current[req.Terminal]
	s.events.mu.Unlock()
	if !ok || (req.Seq != 0 && cur.Seq != req.Seq) {
		writeErr(w, http.StatusConflict, errPromptGone)
		return
	}
	var args []string
	switch {
	case cur.State == "blocked" && req.Answer == "allow":
		args = []string{"keys", "--json", req.Terminal, "enter"}
	case cur.State == "blocked" && req.Answer == "deny":
		args = []string{"keys", "--json", req.Terminal, "esc"}
	case cur.State == "blocked" && req.Answer == "reply":
		args = []string{"send", "--json", "--force", req.Terminal, req.Text}
	case (cur.State == "done" || cur.State == "error") && req.Answer == "reply":
		args = []string{"send", "--json", req.Terminal, req.Text}
	default:
		writeErr(w, http.StatusConflict, errPromptGone)
		return
	}

	out, err := run(r.Context(), 40*time.Second, s.hc, args...)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeRaw(w, out)
}
