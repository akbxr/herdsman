package main

// Pairing a phone without typing the token: a QR code holding a
// herdr-remote://pair link, and a short code that trades for the token.
//
// The code lives in a file beside the token, so `herdr-remote pair` can make
// one while the server runs elsewhere (under launchd). It works once, for
// ten minutes, and is gone after five wrong tries.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"rsc.io/qr"
)

const (
	codeLifetime = 10 * time.Minute
	codeTries    = 5
)

type pairing struct {
	Code     string `json:"code"`
	Expires  int64  `json:"expires"` // unix seconds
	Attempts int    `json:"attempts"`
}

var pairMu sync.Mutex

func pairingFile(tokenFile string) string {
	return filepath.Join(filepath.Dir(tokenFile), "pairing.json")
}

// newCode makes a fresh six-digit code, replacing any earlier one.
func newCode(tokenFile string) (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	p := pairing{Code: fmt.Sprintf("%06d", n.Int64()), Expires: time.Now().Add(codeLifetime).Unix()}
	b, _ := json.Marshal(p)
	pairMu.Lock()
	defer pairMu.Unlock()
	if err := os.WriteFile(pairingFile(tokenFile), b, 0o600); err != nil {
		return "", err
	}
	return p.Code, nil
}

var errBadCode = errors.New("that code is wrong or has expired: run herdr-remote pair on the Mac for a new one")

// redeem checks a code and uses it up. Wrong tries count; the fifth kills it.
func redeem(tokenFile, code string) error {
	pairMu.Lock()
	defer pairMu.Unlock()
	path := pairingFile(tokenFile)
	b, err := os.ReadFile(path)
	if err != nil {
		return errBadCode
	}
	var p pairing
	if json.Unmarshal(b, &p) != nil || time.Now().Unix() > p.Expires || p.Attempts >= codeTries {
		os.Remove(path)
		return errBadCode
	}
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if subtle.ConstantTimeCompare([]byte(code), []byte(p.Code)) != 1 {
		p.Attempts++
		if p.Attempts >= codeTries {
			os.Remove(path)
		} else if b, err := json.Marshal(p); err == nil {
			os.WriteFile(path, b, 0o600)
		}
		return errBadCode
	}
	os.Remove(path) // single use
	return nil
}

// pairLink is what the QR code holds; the app opens it.
func pairLink(base, token, host string) string {
	q := url.Values{"url": {base}, "token": {token}, "host": {host}}
	return "herdr-remote://pair?" + q.Encode()
}

// qrText draws a QR code for a terminal: two modules per character cell
// (upper half block), in explicit black and white so it scans on dark and
// light terminal themes alike.
func qrText(text string) (string, error) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	const quiet = 2
	black := func(x, y int) bool {
		x, y = x-quiet, y-quiet
		return x >= 0 && y >= 0 && x < c.Size && y < c.Size && c.Black(x, y)
	}
	n := c.Size + 2*quiet
	var b strings.Builder
	for y := 0; y < n; y += 2 {
		for x := 0; x < n; x++ {
			fg, bg := 97, 107 // white top, white bottom
			if black(x, y) {
				fg = 30
			}
			if black(x, y+1) {
				bg = 40
			}
			fmt.Fprintf(&b, "\x1b[%d;%dm▀", fg, bg)
		}
		b.WriteString("\x1b[0m\n")
	}
	return b.String(), nil
}

// printPairing shows the QR code, the code and the address for pairing.
func printPairing(w io.Writer, base, token, host, tokenFile string) error {
	code, err := newCode(tokenFile)
	if err != nil {
		return err
	}
	art, err := qrText(pairLink(base, token, host))
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "\nPair a phone: open Herdr Remote and tap Scan the code.\n\n%s\n", art)
	fmt.Fprintf(w, "No camera? Type the address and this code (works once, for 10 minutes):\n\n")
	fmt.Fprintf(w, "  address  %s\n  code     %s %s\n\n", strings.TrimPrefix(base, "http://"), code[:3], code[3:])
	return nil
}

// pair trades a code for the token: {"code": "123456"} → {"token", "host"}.
// It needs no token itself, which is why codes are short-lived and few.
func (s *server) pair(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := decode(r, &req); err != nil || req.Code == "" {
		writeErr(w, http.StatusBadRequest, errors.New("need {code}"))
		return
	}
	if err := redeem(s.tokenFile, req.Code); err != nil {
		time.Sleep(time.Second) // slow down guessing
		writeErr(w, http.StatusForbidden, err)
		return
	}
	host, _ := os.Hostname()
	writeJSON(w, map[string]string{"token": string(s.token), "host": host})
}
