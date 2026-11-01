package main

// Pictures in your messages, for the phone's chat: pasted screenshots (the
// agent writes them into the transcript as base64 image blocks) and files
// named in the text (an attachment sent from the phone lands beside the
// terminal and its path goes with the message; tego writes a pasted image
// as "[Image #1: /path/x.png]").
//
// A chat item only names its pictures by id; GET /api/image?id= sends the
// bytes. A block's bytes are kept in memory (bounded, least recently used
// dropped: the next read of the transcript adds them again), a file only by
// its path: a regular image file, re-checked when it is served.

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif" // decoders for thumbnails
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	imageMaxBytes   = 25 << 20  // the largest picture served, from a block or a file
	imageBlockBytes = 128 << 20 // blocks' bytes kept in memory
	imageFilesKept  = 4096      // files known by id
	thumbBytes      = 32 << 20  // thumbnails kept
)

var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}

// imageType: the picture's Content-Type, from its first bytes; "" when it isn't one.
func imageType(head []byte) string {
	switch t := http.DetectContentType(head); t {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return t
	}
	return ""
}

type chatImage struct {
	mime  string
	data  []byte // a block's bytes; nil for a file
	path  string // a file
	host  host   // the machine the file is on
	size  int64
	mtime time.Time
}

// lru: values by key, each with a cost, the least recently used dropped
// past max.
type lru struct {
	max, used int64
	ll        *list.List
	m         map[string]*list.Element
}

type lruEntry struct {
	key  string
	val  any
	cost int64
}

func newLRU(max int64) *lru { return &lru{max: max, ll: list.New(), m: map[string]*list.Element{}} }

func (c *lru) get(key string) (any, bool) {
	e, ok := c.m[key]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(e)
	return e.Value.(*lruEntry).val, true
}

func (c *lru) add(key string, val any, cost int64) {
	if e, ok := c.m[key]; ok {
		c.used -= e.Value.(*lruEntry).cost
		c.ll.Remove(e)
	}
	c.m[key] = c.ll.PushFront(&lruEntry{key, val, cost})
	c.used += cost
	for c.used > c.max && c.ll.Len() > 1 {
		old := c.ll.Back()
		c.ll.Remove(old)
		delete(c.m, old.Value.(*lruEntry).key)
		c.used -= old.Value.(*lruEntry).cost
	}
}

type imageStore struct {
	mu     sync.Mutex
	blocks *lru // id → *chatImage with data
	files  *lru // id → *chatImage with a path
	thumbs *lru // id@w → []byte
	// Paths named in messages on other machines, checked there: machine
	// NUL path as written → the picture (nil: not one, until checked again).
	remote map[string]remoteImage
}

type remoteImage struct {
	at  time.Time
	img *chatImage
}

var images = &imageStore{blocks: newLRU(imageBlockBytes), files: newLRU(imageFilesKept), thumbs: newLRU(thumbBytes), remote: map[string]remoteImage{}}

var imageIDRe = regexp.MustCompile(`^[bf][0-9a-f]{24}$`)

func imageID(kind string, parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		io.WriteString(h, p)
		h.Write([]byte{0})
	}
	return kind + hex.EncodeToString(h.Sum(nil))[:24]
}

func (s *imageStore) get(id string) (*chatImage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.blocks
	if strings.HasPrefix(id, "f") {
		src = s.files
	}
	v, ok := src.get(id)
	if !ok {
		return nil, false
	}
	return v.(*chatImage), true
}

func (s *imageStore) has(id string) bool {
	_, ok := s.get(id)
	return ok
}

// addBlock keeps a base64 image block's bytes and returns its id; "" when
// it isn't a picture (or is too big). The id is a hash of the base64 text:
// a block already kept isn't decoded again.
func (s *imageStore) addBlock(mediaType, b64 string) string {
	if b64 == "" || len(b64) > imageMaxBytes*4/3+4 {
		return ""
	}
	id := imageID("b", b64)
	if s.has(id) {
		return id
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(data) == 0 {
		return ""
	}
	mime := imageType(data)
	if mime == "" {
		return ""
	}
	s.mu.Lock()
	s.blocks.add(id, &chatImage{mime: mime, data: data, size: int64(len(data))}, int64(len(data)))
	s.mu.Unlock()
	return id
}

// imageFile checks a path named in a message: an absolute (or ~/) path,
// no "..", a regular file (not a link, not a folder) with a picture's
// extension, size and first bytes.
func imageFile(p string) (*chatImage, error) {
	for _, part := range strings.Split(filepath.ToSlash(p), "/") {
		if part == ".." {
			return nil, errors.New("a path with ..")
		}
	}
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		p = home + p[1:]
	}
	if !filepath.IsAbs(p) {
		return nil, errors.New("not an absolute path")
	}
	p = filepath.Clean(p)
	if !imageExts[strings.ToLower(filepath.Ext(p))] {
		return nil, errors.New("not a picture's extension")
	}
	fi, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if fi.Size() == 0 || fi.Size() > imageMaxBytes {
		return nil, errors.New("empty or too big")
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	mime := imageType(head[:n])
	if mime == "" {
		return nil, errors.New("not a picture")
	}
	return &chatImage{mime: mime, path: p, size: fi.Size(), mtime: fi.ModTime()}, nil
}

// addFile: the id of the picture at a path named in a message; "" when it
// isn't one (gone, a folder, not a picture).
func (s *imageStore) addFile(p string) string {
	img, err := imageFile(p)
	if err != nil {
		return ""
	}
	id := imageID("f", img.path, strconv.FormatInt(img.size, 10), strconv.FormatInt(img.mtime.UnixNano(), 10))
	s.mu.Lock()
	s.files.add(id, img, 1)
	s.mu.Unlock()
	return id
}

// resolver: how pictures named by path are found in a transcript on h: here
// right away; on another machine in what prefetch checked there.
func (s *imageStore) resolver(h host) func(string) string {
	if !h.remote() {
		return s.addFile
	}
	return func(p string) string { return s.remoteFile(h.machine, p) }
}

// The largest picture of another machine's: what one hc exec hands back.
const remoteImageMax = remoteReadMax

// How long a path on another machine that wasn't a picture stays so.
const remoteImageMiss = 2 * time.Minute

// remoteFile: the id of a picture on a machine that prefetch found at p.
func (s *imageStore) remoteFile(machine, p string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.remote[machine+"\x00"+p]
	if !ok || e.img == nil {
		return ""
	}
	id := imageID("f", machine, e.img.path, strconv.FormatInt(e.img.size, 10), strconv.FormatInt(e.img.mtime.UnixNano(), 10))
	if _, ok := s.files.get(id); !ok {
		s.files.add(id, e.img, 1)
	}
	return id
}

// prefetch checks, in one round trip to h, the picture paths that the
// messages you wrote among lines name and that weren't checked yet.
func (s *imageStore) prefetch(ctx context.Context, h host, lines [][]byte) {
	var named []string
	seen := map[string]bool{}
	s.mu.Lock()
	for _, l := range lines {
		if !bytes.Contains(l, []byte(`"user"`)) {
			continue
		}
		var cands []string
		for _, m := range imageMarkRe.FindAllSubmatch(l, -1) {
			if len(m[1]) > 0 {
				cands = append(cands, strings.TrimSpace(string(m[1])))
			}
		}
		for _, m := range imagePathRe.FindAll(l, -1) {
			cands = append(cands, string(m))
		}
		for _, p := range cands {
			key := h.machine + "\x00" + p
			e, known := s.remote[key]
			if seen[p] || known && (e.img != nil || time.Since(e.at) < remoteImageMiss) {
				continue
			}
			seen[p] = true
			if len(named) < 200 {
				named = append(named, p)
			}
		}
	}
	s.mu.Unlock()
	if len(named) == 0 {
		return
	}
	var home string
	var paths, asked []string
	for _, p := range named {
		full, ok := remoteImagePath(ctx, h, p, &home)
		if ok {
			paths, asked = append(paths, full), append(asked, p)
		}
	}
	sts, err := h.stat(ctx, paths...)
	if err != nil {
		return // not reached: checked again next time
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.remote) > 4096 {
		clear(s.remote)
	}
	now := time.Now()
	for _, p := range named {
		s.remote[h.machine+"\x00"+p] = remoteImage{at: now}
	}
	for i, st := range sts {
		if st.isFile() && !st.link && st.size > 0 && st.size <= remoteImageMax {
			mime := "image/" + strings.TrimPrefix(strings.ToLower(path.Ext(paths[i])), ".")
			if mime == "image/jpg" {
				mime = "image/jpeg"
			}
			s.remote[h.machine+"\x00"+asked[i]] = remoteImage{now, &chatImage{mime: mime, path: paths[i], host: h, size: st.size, mtime: st.mtime}}
		}
	}
}

// remoteImagePath: a path named in a message as a full path on h, if it can
// be a picture there (absolute or ~/, no "..", a picture's extension).
func remoteImagePath(ctx context.Context, h host, p string, home *string) (string, bool) {
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return "", false
		}
	}
	if strings.HasPrefix(p, "~/") {
		if *home == "" {
			var err error
			if *home, err = h.homeDir(ctx); err != nil {
				return "", false
			}
		}
		p = *home + p[1:]
	}
	if !strings.HasPrefix(p, "/") || !imageExts[strings.ToLower(path.Ext(p))] {
		return "", false
	}
	return path.Clean(p), true
}

// On another machine: a regular file's mtime (seconds), a newline, and at
// most $2 of its bytes.
const imageScript = `[ -f "$1" ] && [ ! -L "$1" ] || { echo "not a regular file" >&2; exit 3; }
m=$(stat -c %Y -- "$1" 2>/dev/null || stat -f %m -- "$1" 2>/dev/null || echo 0)
printf '%s\n' "$m"; head -c "$2" "$1"`

// bytes: the picture's bytes; a file is read again, if it is still the
// same regular file (not swapped for a link, not rewritten).
func (img *chatImage) bytes() ([]byte, error) {
	if img.path == "" {
		return img.data, nil
	}
	if img.host.remote() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := img.host.run(ctx, "", "sh", "-c", imageScript, "sh", img.path, strconv.Itoa(remoteImageMax+1))
		if err != nil {
			return nil, err
		}
		line, data, _ := bytes.Cut(out, []byte{'\n'})
		sec, _ := strconv.ParseInt(string(line), 10, 64)
		if int64(len(data)) != img.size || (sec > 0 && !img.mtime.IsZero() && sec != img.mtime.Unix()) || imageType(data) == "" {
			return nil, errors.New("the file changed")
		}
		return data, nil
	}
	f, err := os.OpenFile(img.path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() != img.size || !fi.ModTime().Equal(img.mtime) {
		return nil, errors.New("the file changed")
	}
	data, err := io.ReadAll(io.LimitReader(f, imageMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != img.size || imageType(data) == "" {
		return nil, errors.New("the file changed")
	}
	return data, nil
}

var (
	// "[Image #2: /path/x.png]" (tego), "[Image #1]" (Claude Code).
	imageMarkRe = regexp.MustCompile(`\[Image #\d+(?::\s*([^\]\n]+?))?\s*\]`)
	// A path to a picture: /… or ~/…, without spaces.
	imagePathRe = regexp.MustCompile("(?i)~?/[^\\s\"'<>()\\[\\]`]*\\.(?:png|jpe?g|gif|webp)")
	spacesRe    = regexp.MustCompile(`[ \t]{2,}`)
)

// userImagesIn: the pictures of a message you wrote, and its text without
// the markers and paths that stand for them. With image blocks (blockIDs),
// those are the pictures and every "[Image #n…]" goes; without, the paths in
// the text that are pictures on this Mac are.
func userImagesIn(text string, blockIDs []string) (string, []string) {
	return userImagesWith(text, blockIDs, images.addFile)
}

// userImagesWith: userImagesIn, with the pictures at paths found by files.
func userImagesWith(text string, blockIDs []string, files func(string) string) (string, []string) {
	ids := append([]string(nil), blockIDs...)
	if len(ids) > 0 {
		text = imageMarkRe.ReplaceAllString(text, "")
	} else {
		text = imageMarkRe.ReplaceAllStringFunc(text, func(m string) string {
			sub := imageMarkRe.FindStringSubmatch(m)
			if sub[1] == "" {
				return m
			}
			if id := files(strings.TrimSpace(sub[1])); id != "" {
				ids = append(ids, id)
				return ""
			}
			return m
		})
		var b strings.Builder
		at := 0
		for _, loc := range imagePathRe.FindAllStringIndex(text, -1) {
			i, j := loc[0], loc[1]
			if i > 0 && !strings.ContainsRune(" \t\n(<\"'`[", rune(text[i-1])) {
				continue // part of a word or a URL
			}
			if j < len(text) && !strings.ContainsRune(" \t\n)>\"'`],.;:!?", rune(text[j])) {
				continue
			}
			if id := files(text[i:j]); id != "" {
				ids = append(ids, id)
				b.WriteString(text[at:i])
				at = j
			}
		}
		b.WriteString(text[at:])
		text = b.String()
	}
	if len(ids) == len(blockIDs) && len(blockIDs) == 0 {
		return text, nil
	}
	// Tidy what is left: no runs of spaces, no spaces at the ends of lines.
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(spacesRe.ReplaceAllString(l, " "), " \t")
	}
	seen := map[string]bool{}
	out := ids[:0]
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), out
}

// GET /api/image?id=…[&w=480]: a picture of the chat; with w, scaled down
// to fit w pixels on its longer side (when it is larger and can be decoded:
// PNG, JPEG, GIF; WebP is sent as it is).
func (s *server) image(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !imageIDRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, errors.New("bad id"))
		return
	}
	img, ok := images.get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("no such picture (any more)"))
		return
	}
	width, _ := strconv.Atoi(r.URL.Query().Get("w"))
	if width != 0 {
		width = min(max(width, 32), 4096)
	}
	mime, data := img.mime, []byte(nil)
	if width > 0 {
		key := fmt.Sprintf("%s@%d", id, width)
		images.mu.Lock()
		v, ok := images.thumbs.get(key)
		images.mu.Unlock()
		if ok {
			t := v.(thumb)
			mime, data = t.mime, t.data
		}
	}
	if data == nil {
		full, err := img.bytes()
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		data = full
		if img.path != "" {
			mime = imageType(full) // a file's own bytes say
		}
		if width > 0 {
			if t, ok := makeThumb(full, width); ok {
				mime, data = t.mime, t.data
				images.mu.Lock()
				images.thumbs.add(fmt.Sprintf("%s@%d", id, width), t, int64(len(t.data)))
				images.mu.Unlock()
			}
		}
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "private, max-age=86400") // an id names one picture's bytes
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(data)
}

type thumb struct {
	mime string
	data []byte
}

// makeThumb scales a picture down to fit width pixels on its longer side:
// JPEG when it is opaque, PNG when not. false: it already fits, or can't be
// decoded (send it as it is).
func makeThumb(data []byte, width int) (thumb, bool) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || max(cfg.Width, cfg.Height) <= width || cfg.Width*cfg.Height > 50_000_000 {
		return thumb{}, false
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return thumb{}, false
	}
	dst := scaleDown(src, width)
	var out bytes.Buffer
	if dst.Opaque() {
		if jpeg.Encode(&out, dst, &jpeg.Options{Quality: 82}) != nil {
			return thumb{}, false
		}
		return thumb{"image/jpeg", out.Bytes()}, true
	}
	if (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&out, dst) != nil {
		return thumb{}, false
	}
	return thumb{"image/png", out.Bytes()}, true
}

// scaleDown: each pixel of the result is the average of the box of source
// pixels it covers (premultiplied RGBA, so averaging is right).
func scaleDown(src image.Image, maxSide int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dw, dh := maxSide, maxSide
	if sw >= sh {
		dh = max(1, sh*maxSide/sw)
	} else {
		dw = max(1, sw*maxSide/sh)
	}
	s, ok := src.(*image.RGBA)
	if !ok || s.Bounds().Min != (image.Point{}) {
		s = image.NewRGBA(image.Rect(0, 0, sw, sh))
		draw.Draw(s, s.Bounds(), src, b.Min, draw.Src)
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		y0, y1 := y*sh/dh, max((y+1)*sh/dh, y*sh/dh+1)
		for x := 0; x < dw; x++ {
			x0, x1 := x*sw/dw, max((x+1)*sw/dw, x*sw/dw+1)
			var r, g, bl, a, n uint32
			for yy := y0; yy < y1; yy++ {
				row := s.Pix[yy*s.Stride+x0*4 : yy*s.Stride+x1*4]
				for i := 0; i < len(row); i += 4 {
					r += uint32(row[i])
					g += uint32(row[i+1])
					bl += uint32(row[i+2])
					a += uint32(row[i+3])
					n++
				}
			}
			o := dst.PixOffset(x, y)
			dst.Pix[o], dst.Pix[o+1], dst.Pix[o+2], dst.Pix[o+3] = uint8(r/n), uint8(g/n), uint8(bl/n), uint8(a/n)
		}
	}
	return dst
}
