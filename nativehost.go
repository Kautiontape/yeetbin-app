package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"
)

// Chrome refuses host-to-browser messages over 1 MiB. Browser-to-host messages may be
// far larger, but nothing the extension sends comes close.
const (
	maxOutboundFrame = 1 << 20
	maxInboundFrame  = 1 << 20
	// JSON escaping grows a byte at most 6x (\u0001, <), so this keeps every
	// chunk frame under the cap.
	chunkBytes       = 128 << 10
	maxSocketRequest = 4 << 20
)

// A variable so tests can shorten it.
var hostResultTimeout = 20 * time.Second

// Host-to-extension: a "yeet" header, then exactly Chunks "chunk" frames.
type yeetFrame struct {
	Kind     string `json:"kind"`
	Req      int    `json:"req"`
	Title    string `json:"title"`
	Type     string `json:"type"`
	Language string `json:"language"`
	Open     bool   `json:"open"`
	Chunks   int    `json:"chunks"`
}

type chunkFrame struct {
	Kind string `json:"kind"`
	Req  int    `json:"req"`
	Seq  int    `json:"seq"`
	Data string `json:"data"`
}

// Extension-to-host: "hello" on connect, "result" per yeet.
type inboundFrame struct {
	Kind    string `json:"kind"`
	Req     int    `json:"req"`
	ID      string `json:"id"`
	URL     string `json:"url"`
	Error   string `json:"error"`
	Version string `json:"version"`
}

func writeFrame(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(body) > maxOutboundFrame {
		return fmt.Errorf("frame of %d bytes exceeds the %d byte limit", len(body), maxOutboundFrame)
	}
	buf := make([]byte, 4+len(body))
	binary.NativeEndian.PutUint32(buf, uint32(len(body)))
	copy(buf[4:], body)
	_, err = w.Write(buf)
	return err
}

// Returns io.EOF only on a clean end between frames.
func readFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.NativeEndian.Uint32(hdr[:])
	if n > maxInboundFrame {
		return nil, fmt.Errorf("incoming frame of %d bytes exceeds the %d byte limit", n, maxInboundFrame)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return body, nil
}

// Cuts only on rune boundaries so each chunk is valid UTF-8 and a JS string concat
// reassembles the original exactly. Always returns at least one chunk.
func chunkContent(s string, size int) []string {
	var out []string
	for len(s) > size {
		cut := size
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		if cut == 0 {
			cut = size // not valid UTF-8; cut anywhere rather than loop forever
		}
		out = append(out, s[:cut])
		s = s[cut:]
	}
	return append(out, s)
}

type nativeHost struct {
	out     io.Writer
	log     io.Writer
	timeout time.Duration

	outMu sync.Mutex // keeps one request's frames contiguous on stdout

	mu      sync.Mutex
	nextReq int
	pending map[int]chan localResponse
	closed  bool
}

// Runs when a browser launches yeet as its native messaging host. stdout carries only
// framed messages; everything human-readable goes to stderr.
func runNativeHost(stdin io.Reader, stdout, stderr io.Writer, sockPath string) int {
	h := &nativeHost{
		out:     stdout,
		log:     stderr,
		timeout: hostResultTimeout,
		pending: map[int]chan localResponse{},
	}

	ln, ours, err := listenSocket(sockPath)
	if err != nil {
		h.logf("cannot listen on %s: %v", sockPath, err)
		return exitError
	}
	defer removeIfOurs(sockPath, ours)
	defer ln.Close()

	go h.serve(ln)

	code := h.readBrowser(stdin)
	h.shutdown()
	return code
}

func (h *nativeHost) logf(format string, args ...any) {
	fmt.Fprintf(h.log, "yeet host: "+format+"\n", args...)
}

func (h *nativeHost) readBrowser(stdin io.Reader) int {
	for {
		body, err := readFrame(stdin)
		if errors.Is(err, io.EOF) {
			return exitOK // browser closed the port
		}
		if err != nil {
			h.logf("reading from browser: %v", err)
			return exitError
		}

		var msg inboundFrame
		if err := json.Unmarshal(body, &msg); err != nil {
			h.logf("ignoring malformed message: %v", err)
			continue
		}
		switch msg.Kind {
		case "hello":
			h.logf("extension %s connected", msg.Version)
		case "result":
			h.deliver(msg.Req, localResponse{ID: msg.ID, URL: msg.URL, Error: msg.Error})
		default:
			h.logf("ignoring message of kind %q", msg.Kind)
		}
	}
}

func (h *nativeHost) deliver(req int, resp localResponse) {
	h.mu.Lock()
	ch, ok := h.pending[req]
	delete(h.pending, req)
	h.mu.Unlock()
	if ok {
		ch <- resp
	}
}

func (h *nativeHost) shutdown() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for req, ch := range h.pending {
		ch <- localResponse{Error: "the browser disconnected"}
		delete(h.pending, req)
	}
}

func (h *nativeHost) serve(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return // listener closed
		}
		go h.handle(conn)
	}
}

func (h *nativeHost) handle(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(localRequestTimeout))

	var req localRequest
	resp := func() localResponse {
		if err := json.NewDecoder(io.LimitReader(conn, maxSocketRequest)).Decode(&req); err != nil {
			return localResponse{Error: fmt.Sprintf("bad request: %v", err)}
		}
		if req.Content == "" {
			return localResponse{Error: "content is empty"}
		}
		if len(req.Content) > maxContentBytes {
			return localResponse{Error: fmt.Sprintf("content is larger than %s", mib(maxContentBytes))}
		}
		return h.forward(req)
	}()

	json.NewEncoder(conn).Encode(resp)
}

func (h *nativeHost) forward(req localRequest) localResponse {
	ch := make(chan localResponse, 1)

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return localResponse{Error: "the browser disconnected"}
	}
	h.nextReq++
	id := h.nextReq
	h.pending[id] = ch
	h.mu.Unlock()

	if err := h.send(id, req); err != nil {
		h.mu.Lock()
		delete(h.pending, id)
		h.mu.Unlock()
		return localResponse{Error: fmt.Sprintf("cannot reach the extension: %v", err)}
	}

	timer := time.NewTimer(h.timeout)
	defer timer.Stop()
	select {
	case resp := <-ch:
		return resp
	case <-timer.C:
		h.mu.Lock()
		delete(h.pending, id)
		h.mu.Unlock()
		return localResponse{Error: fmt.Sprintf("the extension did not answer within %s", h.timeout)}
	}
}

func (h *nativeHost) send(id int, req localRequest) error {
	chunks := chunkContent(req.Content, chunkBytes)

	h.outMu.Lock()
	defer h.outMu.Unlock()

	header := yeetFrame{
		Kind:     "yeet",
		Req:      id,
		Title:    req.Title,
		Type:     req.Type,
		Language: req.Language,
		Open:     req.Open,
		Chunks:   len(chunks),
	}
	if err := writeFrame(h.out, header); err != nil {
		return err
	}
	for seq, data := range chunks {
		if err := writeFrame(h.out, chunkFrame{Kind: "chunk", Req: id, Seq: seq, Data: data}); err != nil {
			return err
		}
	}
	return nil
}

// The newest host takes over the path; any previous one keeps running unreachable
// until its browser closes the port.
func listenSocket(path string) (*net.UnixListener, os.FileInfo, error) {
	if err := secureDir(filepath.Dir(path)); err != nil {
		return nil, nil, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}

	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, nil, err
	}
	// Cleanup goes through removeIfOurs so a successor's socket survives us.
	ln.SetUnlinkOnClose(false)

	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		ln.Close()
		return nil, nil, err
	}
	return ln, info, nil
}

// Refuses a symlink or a directory someone else owns (chmod fails if not ours).
func secureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if info.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("securing %s: %w", dir, err)
		}
	}
	return nil
}

// ModTime guards against the inode number being reused by a successor's socket.
func removeIfOurs(path string, ours os.FileInfo) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if os.SameFile(info, ours) && info.ModTime().Equal(ours.ModTime()) {
		os.Remove(path)
	}
}
