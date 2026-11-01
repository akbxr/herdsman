package main

// What an agent is writing right now, read off its screen, for the chat
// while it works. Agents write their transcript one finished message at a
// time, so a long answer or a long-running command shows in the chat only
// when it ends; the screen shows it as it happens.
//
// The bottom of an agent's screen is its own chrome: the input box between
// rules (tego: ──── ❯ ────, Claude Code: ╭─╮ │ > │ ╰─╯), a status line, and a
// spinner line ("Working… esc to interrupt"). What is above it is the
// conversation as it's being written: the last lines of that are live.

import (
	"strings"
	"unicode/utf8"
)

const liveLines = 12

// isRule: a line drawn with box characters (a rule, a box's top or bottom),
// maybe with a title in it.
func isRule(t string) bool {
	t = strings.TrimSpace(t)
	if utf8.RuneCountInString(t) < 3 {
		return false
	}
	for _, p := range []string{"──", "━━", "══", "╭─", "┌─", "╰─", "└─"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// isChrome: part of an input box: a rule, a prompt line, a box's side.
func isChrome(l string) bool {
	t := strings.TrimSpace(l)
	if isRule(t) {
		return true
	}
	for _, p := range []string{"❯", "›", ">", "│", "┃"} {
		if t == p || strings.HasPrefix(t, p+" ") {
			return true
		}
	}
	return false
}

// isSpinner: the agent's "still working" line, not content.
func isSpinner(t string) bool {
	t = strings.TrimSpace(t)
	if t == "" {
		return false
	}
	l := strings.ToLower(t)
	if strings.Contains(l, "esc to interrupt") || strings.Contains(l, "ctrl+c to interrupt") {
		return true
	}
	r, _ := utf8.DecodeRuneInString(t)
	return r >= 0x2800 && r <= 0x28FF // braille spinner frames
}

// liveTail: the last lines of content above the agent's chrome.
func liveTail(screen string) []string {
	lines := strings.Split(strings.ReplaceAll(screen, "\r", ""), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	// The chrome: from the lowest rule near the bottom, up through the input
	// box it closes (rules, prompt lines, a box's sides), in one piece.
	end := len(lines)
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-6; i-- {
		if isRule(lines[i]) {
			end = i
			break
		}
	}
	for end < len(lines) && end > 0 && isChrome(lines[end-1]) {
		end--
	}
	content := lines[:end]
	for len(content) > 0 && (strings.TrimSpace(content[len(content)-1]) == "" || isSpinner(content[len(content)-1])) {
		content = content[:len(content)-1]
	}
	var out []string
	for i := len(content) - 1; i >= 0 && len(out) < liveLines; i-- {
		out = append(out, content[i])
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	// Leading blank lines go; the common indent goes.
	for len(out) > 0 && strings.TrimSpace(out[0]) == "" {
		out = out[1:]
	}
	indent := -1
	for _, l := range out {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " "))
		if indent < 0 || n < indent {
			indent = n
		}
	}
	for i, l := range out {
		if len(l) >= indent && indent > 0 {
			out[i] = l[indent:]
		}
	}
	return out
}
