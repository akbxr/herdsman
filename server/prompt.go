package main

// The menu an agent shows when it asks you something, read off its screen,
// so the phone can offer the real choices ("Yes", "Yes, and don't ask
// again", "No, tell it what to do") instead of just Enter and Esc.
//
// Agents draw these menus alike: one option per line, the selected one
// behind a marker (❯ › ▸ ▶ ➜ >), the others indented to the same column,
// often numbered, sometimes inside a box. tego:
//
//	Allow bash: go test ./...?
//	›
//	❯ Yes
//	  Yes, always for this session
//	  No
//	  3/3 · ↑↓ move · enter select · esc cancel
//
// Claude Code:
//
//	│ Do you want to proceed?                   │
//	│ ❯ 1. Yes                                  │
//	│   2. Yes, and don't ask again             │
//	│   3. No, and tell Claude what to do (esc) │
//
// An option is picked with ↑/↓ from the selected one, then Enter: every one
// of them takes arrows.

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

type prompt struct {
	Question string   `json:"question,omitempty"`
	Options  []string `json:"options"`
	Selected int      `json:"selected"`
}

var (
	markers  = []string{"❯", "›", "▸", "▶", "➜", "→", ">"}
	boxChars = "│┃║|"
	numbered = regexp.MustCompile(`^\(?\d{1,2}[.)]\s+`)
	// Lines under a menu that say how to use it.
	hintLine = regexp.MustCompile(`(?i)(↑|↓|enter to|enter select|esc to|esc cancel|tab to|^\d+/\d+\s*·|^·)`)
)

// cleanLine drops a box's sides and trailing blanks, keeping the indent.
func cleanLine(l string) string {
	l = strings.TrimRight(l, " \t")
	if t := strings.TrimLeft(l, " "); t != "" && strings.ContainsRune(boxChars, []rune(t)[0]) {
		l = strings.Replace(l, string([]rune(t)[0]), " ", 1)
	}
	l = strings.TrimRight(l, " \t")
	if l != "" {
		if r, n := utf8.DecodeLastRuneInString(l); strings.ContainsRune(boxChars, r) {
			l = strings.TrimRight(l[:len(l)-n], " ")
		}
	}
	return l
}

// markerAt: the option text's column when the line starts (after its
// indent) with a marker and a space; -1 otherwise.
func markerAt(l string) int {
	indent := len(l) - len(strings.TrimLeft(l, " "))
	rest := l[indent:]
	for _, m := range markers {
		if strings.HasPrefix(rest, m+" ") {
			col := runeCol(l, indent) + utf8.RuneCountInString(m) + 1
			if strings.TrimSpace(rest[len(m)+1:]) == "" {
				return -1 // an empty input line ("› ")
			}
			return col
		}
	}
	return -1
}

func runeCol(s string, byteIdx int) int { return utf8.RuneCountInString(s[:byteIdx]) }

// atColumn: the line's text starts exactly at column col (in runes), with
// only spaces before it.
func atColumn(l string, col int) bool {
	r := []rune(l)
	if len(r) <= col || r[col] == ' ' {
		return false
	}
	for _, c := range r[:col] {
		if c != ' ' {
			return false
		}
	}
	return true
}

func optionText(l string, col int) string {
	t := strings.TrimSpace(string([]rune(l)[col:]))
	t = numbered.ReplaceAllString(t, "")
	// tego puts details after two spaces: the label is what comes before.
	if i := strings.Index(t, "   "); i > 0 {
		t = t[:i]
	} else if i := strings.Index(t, "  "); i > 0 {
		t = t[:i]
	}
	return strings.TrimSpace(t)
}

// parsePrompt finds the lowest menu on the screen; nil when there is none.
func parsePrompt(screen string) *prompt {
	raw := strings.Split(strings.ReplaceAll(screen, "\r", ""), "\n")
	lines := make([]string, len(raw))
	for i, l := range raw {
		lines[i] = cleanLine(l)
	}
	for i := len(lines) - 1; i >= 0; i-- {
		col := markerAt(lines[i])
		if col < 0 {
			continue
		}
		first, last := i, i
		for first > 0 && atColumn(lines[first-1], col) && !hintLine.MatchString(strings.TrimSpace(lines[first-1])) && markerAt(lines[first-1]) < 0 {
			first--
		}
		for last < len(lines)-1 && atColumn(lines[last+1], col) && !hintLine.MatchString(strings.TrimSpace(lines[last+1])) {
			last++
		}
		if last == first {
			continue // one line with a marker isn't a menu
		}
		p := &prompt{Selected: i - first}
		for j := first; j <= last; j++ {
			p.Options = append(p.Options, optionText(lines[j], col))
		}
		// The question: the closest line above that asks something ("?"),
		// else the closest text, past an input line ("› ") and rules.
		for j := first - 1; j >= 0 && j >= first-8; j-- {
			t := strings.TrimSpace(lines[j])
			if t == "" || isInputLine(t) || strings.Trim(t, "─━═-╭╮╰╯┌┐└┘ ") == "" {
				continue
			}
			if p.Question == "" {
				p.Question = t
			}
			if strings.Contains(t, "?") {
				p.Question = t
				break
			}
		}
		return p
	}
	return nil
}

func isInputLine(t string) bool {
	for _, m := range markers {
		if t == m || strings.HasPrefix(t, m+" ") && markerAt(t) < 0 {
			return true
		}
	}
	return strings.HasPrefix(t, "› ")
}

// screenPrompt reads a terminal's screen and the menu on it.
func (s *server) screenPrompt(ctx context.Context, terminal string) (*prompt, error) {
	text, err := s.backend(terminal).screen(ctx, terminal, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return parsePrompt(text), nil
}

// GET /api/prompt?terminal=t12: the menu a waiting agent shows, if any.
func (s *server) promptHandler(w http.ResponseWriter, r *http.Request) {
	t := r.URL.Query().Get("terminal")
	if !validTerminal(t) {
		writeErr(w, http.StatusBadRequest, errors.New("need terminal"))
		return
	}
	p, err := s.screenPrompt(r.Context(), t)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, map[string]any{"prompt": p})
}

// pickKeys: the keys that move from the selected option to the one labelled
// option, then press Enter; nil when the menu doesn't have it.
func pickKeys(p *prompt, option string) []string {
	if p == nil {
		return nil
	}
	for i, o := range p.Options {
		if o != option {
			continue
		}
		var keys []string
		for ; i > p.Selected; i-- {
			keys = append(keys, "down")
		}
		for ; i < p.Selected; i++ {
			keys = append(keys, "up")
		}
		return append(keys, "enter")
	}
	return nil
}
