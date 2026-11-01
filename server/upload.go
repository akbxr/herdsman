package main

// Attachments from the phone: the bytes arrive here, and `hc put` places
// them beside the terminal (on its machine, under herdr-core's files), so the
// agent there can open them by path. A herdr CLI pane's file goes under
// ~/Library/Application Support/herdr-remote/uploads/<pane>/ (backend.go).

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const maxUpload = 1 << 30 // 1 GiB: long phone videos

// safeName keeps a file name's look (and its extension) without anything
// that could leave the folder or confuse a shell.
func safeName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '_':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune('_')
		}
	}
	s := strings.Trim(b.String(), ".")
	if s == "" {
		s = fmt.Sprintf("file-%d", time.Now().Unix())
	}
	if len(s) > 120 {
		ext := filepath.Ext(s)
		s = s[:120-len(ext)] + ext
	}
	return s
}

// upload: POST /api/upload?terminal=t12&name=IMG_0042.jpg, the file as the
// body. Answers {"path": "...", "bytes": n}.
func (s *server) upload(w http.ResponseWriter, r *http.Request) {
	terminal := r.URL.Query().Get("terminal")
	if !validTerminal(terminal) {
		writeErr(w, http.StatusBadRequest, errors.New("bad terminal"))
		return
	}
	name := safeName(r.URL.Query().Get("name"))
	dir, err := os.MkdirTemp(s.shotDir, "up-")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	n, err := io.Copy(f, http.MaxBytesReader(w, r.Body, maxUpload))
	f.Close()
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("the upload didn't finish: %w", err))
		return
	}
	var put struct {
		Path  string `json:"path"`
		Bytes int64  `json:"bytes"`
	}
	put.Path, put.Bytes, err = s.backend(terminal).put(r.Context(), terminal, path)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if put.Bytes == 0 {
		put.Bytes = n
	}
	writeJSON(w, put)
}
