package main

// POST /api/herdr {"method": "tab.create", "params": {…}, "confirmed"?: true}:
// a few of the herdr CLI's own methods, on its default session, as they
// are. An allowlist: these methods, with the params herdr's schema gives
// them; closing a tab or a pane takes "confirmed": true. Answers
// {"result": <herdr's result>}.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// herdrMethod: the params a method takes (from `herdr api schema`), the
// ones it needs, and whether it destroys something.
type herdrMethod struct {
	params   []string
	required []string
	confirm  bool
}

var herdrMethods = map[string]herdrMethod{
	"tab.create":      {params: []string{"cwd", "env", "focus", "label", "workspace_id"}},
	"tab.close":       {params: []string{"tab_id"}, required: []string{"tab_id"}, confirm: true},
	"tab.rename":      {params: []string{"tab_id", "label"}, required: []string{"tab_id", "label"}},
	"tab.focus":       {params: []string{"tab_id"}, required: []string{"tab_id"}},
	"pane.close":      {params: []string{"pane_id"}, required: []string{"pane_id"}, confirm: true},
	"pane.split":      {params: []string{"cwd", "direction", "env", "focus", "ratio", "right_click", "target_pane_id", "workspace_id"}},
	"workspace.focus": {params: []string{"workspace_id"}, required: []string{"workspace_id"}},
}

// herdrIDParams: the ids among the params, and what each must look like.
var herdrIDParams = map[string]func(string) bool{
	"tab_id":         herdrTabRe.MatchString,
	"pane_id":        herdrPaneIDRe.MatchString,
	"target_pane_id": herdrPaneIDRe.MatchString,
	"workspace_id":   herdrWorkspaceRe.MatchString,
}

// herdrStatus: the HTTP status for an error herdr answered with.
func herdrStatus(err error) int {
	code := herdrCode(err)
	switch {
	case code == "":
		if errors.Is(err, errHerdrOff) {
			return http.StatusServiceUnavailable
		}
		return http.StatusBadGateway
	case strings.HasSuffix(code, "_not_found"):
		return http.StatusNotFound
	case strings.HasPrefix(code, "invalid") || code == "unknown_method":
		return http.StatusBadRequest
	}
	return http.StatusConflict
}

func writeHerdrErr(w http.ResponseWriter, err error) {
	status := herdrStatus(err)
	reply := map[string]string{"error": err.Error()}
	if code := herdrCode(err); code != "" {
		reply["code"] = code
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(reply)
}

func (s *server) herdrControl(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Method    string                     `json:"method"`
		Params    map[string]json.RawMessage `json:"params"`
		Confirmed bool                       `json:"confirmed"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("need {method, params}"))
		return
	}
	m, ok := herdrMethods[req.Method]
	if !ok {
		names := make([]string, 0, len(herdrMethods))
		for n := range herdrMethods {
			names = append(names, n)
		}
		sort.Strings(names)
		writeErr(w, http.StatusBadRequest, fmt.Errorf("method is one of %s", strings.Join(names, ", ")))
		return
	}
	if m.confirm && !req.Confirmed {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("%s closes it for good: send \"confirmed\": true", req.Method))
		return
	}
	params := map[string]any{}
	for k, v := range req.Params {
		if !contains(m.params, k) {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("%s takes %s, not %q", req.Method, strings.Join(m.params, ", "), k))
			return
		}
		if valid, isID := herdrIDParams[k]; isID {
			var id string
			if json.Unmarshal(v, &id) != nil {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("%s must be a string", k))
				return
			}
			if p, ok := herdrPane(id); ok {
				id = p // "herdr:wF:p2", as the phone addresses panes
			}
			if !valid(id) {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("bad %s %q", k, id))
				return
			}
			params[k] = id
			continue
		}
		params[k] = v
	}
	for _, k := range m.required {
		if _, ok := params[k]; !ok {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("%s needs %s", req.Method, k))
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	result, err := s.herdrCall(ctx, req.Method, params)
	if err != nil {
		writeHerdrErr(w, err)
		return
	}
	writeJSON(w, map[string]json.RawMessage{"result": result})
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
