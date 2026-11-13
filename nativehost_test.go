package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Unix socket paths are capped near 108 bytes, so keep test sockets short.
func shortTempDir(t *testing.T) string {
	t.Helper()
	base := ""
	if len(os.TempDir()) > 40 {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "yeet")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestFrameRoundTripUsesNativeEndianLength(t *testing.T) {
	var buf bytes.Buffer
	msg := chunkFrame{Kind: "chunk", Req: 7, Seq: 2, Data: "héllo 🚀"}
	if err := writeFrame(&buf, msg); err != nil {
		t.Fatal(err)
	}

	raw := buf.Bytes()
	want, _ := json.Marshal(msg)
	if got := binary.NativeEndian.Uint32(raw[:4]); int(got) != len(want) {
		t.Fatalf("length prefix = %d, want %d", got, len(want))
	}

	body, err := readFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	var back chunkFrame
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatal(err)
	}
	if back != msg {
		t.Errorf("round trip = %+v, want %+v", back, msg)
	}
	if _, err := readFrame(&buf); !errors.Is(err, io.EOF) {
		t.Errorf("reading past the last frame: err = %v, want io.EOF", err)
	}
}

func TestReadFrameRejectsTruncatedInput(t *testing.T) {
	var hdr [4]byte
	binary.NativeEndian.PutUint32(hdr[:], 10)

	cases := map[string][]byte{
		"partial header": {1, 0},
		"partial body":   append(hdr[:], []byte("abc")...),
	}
	for name, input := range cases {
		if _, err := readFrame(bytes.NewReader(input)); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("%s: err = %v, want io.ErrUnexpectedEOF", name, err)
		}
	}
}

func TestReadFrameRejectsOversizeFrame(t *testing.T) {
	var hdr [4]byte
	binary.NativeEndian.PutUint32(hdr[:], maxInboundFrame+1)
	if _, err := readFrame(bytes.NewReader(hdr[:])); err == nil {
		t.Fatal("want an error for a frame over the limit")
	}
}

func TestWriteFrameRejectsOversizeFrame(t *testing.T) {
	err := writeFrame(io.Discard, chunkFrame{Data: strings.Repeat("a", maxOutboundFrame)})
	if err == nil {
		t.Fatal("want an error for a frame over Chrome's 1 MiB limit")
	}
}

func TestChunkContent(t *testing.T) {
	big := strings.Repeat("héllo 🚀 <&> \x01\t\n", 120_000) // > 1 MiB, mixed widths
	cases := map[string]string{
		"empty":            "",
		"short":            "hi",
		"exactly one":      strings.Repeat("a", chunkBytes),
		"one over":         strings.Repeat("a", chunkBytes+1),
		"rune on boundary": strings.Repeat("a", chunkBytes-1) + "🚀" + "tail",
		"over 1 MiB":       big,
	}
	for name, input := range cases {
		chunks := chunkContent(input, chunkBytes)
		if len(chunks) < 1 {
			t.Errorf("%s: got no chunks, want at least one", name)
			continue
		}
		for i, c := range chunks {
			if len(c) > chunkBytes {
				t.Errorf("%s: chunk %d is %d bytes, over %d", name, i, len(c), chunkBytes)
			}
			if !utf8.ValidString(c) {
				t.Errorf("%s: chunk %d splits a rune", name, i)
			}
		}
		if got := strings.Join(chunks, ""); got != input {
			t.Errorf("%s: reassembled content differs from the input", name)
		}
	}
}

// Control characters escape to \u00XX, the worst case for JSON growth.
func TestWorstCaseChunkStillFitsInOneFrame(t *testing.T) {
	chunks := chunkContent(strings.Repeat("\x01", 2*chunkBytes), chunkBytes)
	for i, c := range chunks {
		if err := writeFrame(io.Discard, chunkFrame{Kind: "chunk", Req: 1 << 30, Seq: i, Data: c}); err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
	}
}

// hostRig plays the browser side of a running native host.
type hostRig struct {
	path     string
	toHost   *io.PipeWriter // the host's stdin
	fromHost *io.PipeReader // the host's stdout
	done     chan struct{}
	code     int
}

// Every frame the host can send, flattened.
type outMsg struct {
	Kind     string `json:"kind"`
	Req      int    `json:"req"`
	Seq      int    `json:"seq"`
	Data     string `json:"data"`
	Title    string `json:"title"`
	Type     string `json:"type"`
	Language string `json:"language"`
	Open     bool   `json:"open"`
	Chunks   int    `json:"chunks"`
}

// Starts a host on path and waits until it owns the socket (a different file than prev).
func startHost(t *testing.T, path string, prev os.FileInfo) *hostRig {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	r := &hostRig{path: path, toHost: inW, fromHost: outR, done: make(chan struct{})}

	go func() {
		r.code = runNativeHost(inR, outW, io.Discard, path)
		outW.Close()
		close(r.done)
	}()
	t.Cleanup(func() {
		inW.Close()
		outR.Close()
		r.exit(t)
	})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		info, err := os.Stat(path)
		if err == nil && info.Mode()&os.ModeSocket != 0 && (prev == nil || !os.SameFile(info, prev)) {
			if conn, err := net.Dial("unix", path); err == nil {
				conn.Close() // the host answers this with an error and moves on
				return r
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("host never listened on %s", path)
	return nil
}

// Waits for the host to exit and returns its exit code.
func (r *hostRig) exit(t *testing.T) int {
	t.Helper()
	select {
	case <-r.done:
		return r.code
	case <-time.After(5 * time.Second):
		t.Error("host did not exit")
		return -1
	}
}

func (r *hostRig) read(t *testing.T) (outMsg, int) {
	t.Helper()
	body, err := readFrame(r.fromHost)
	if err != nil {
		t.Fatalf("reading host output: %v", err)
	}
	var m outMsg
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("host sent invalid JSON: %v", err)
	}
	return m, len(body)
}

func (r *hostRig) reply(t *testing.T, v any) {
	t.Helper()
	if err := writeFrame(r.toHost, v); err != nil {
		t.Fatalf("writing to host: %v", err)
	}
}

type sendResult struct {
	url string
	err error
}

func sendAsync(path string, req localRequest) <-chan sendResult {
	ch := make(chan sendResult, 1)
	go func() {
		url, err := sendLocal(path, req)
		ch <- sendResult{url, err}
	}()
	return ch
}

func wait(t *testing.T, ch <-chan sendResult) sendResult {
	t.Helper()
	select {
	case res := <-ch:
		return res
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the CLI side")
		return sendResult{}
	}
}

func TestNativeHostRelaysLargeYeetInChunks(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "yeet", "local.sock")
	rig := startHost(t, path, nil)

	content := strings.Repeat("héllo 🚀 <&> \x01\t\n", 90_000) // ~1.6 MiB
	result := sendAsync(path, localRequest{
		Content: content, Type: "code", Language: "go", Title: "big.go", Open: true,
	})

	rig.reply(t, map[string]any{"kind": "hello", "version": "test"})

	head, _ := rig.read(t)
	if head.Kind != "yeet" || head.Title != "big.go" || head.Type != "code" ||
		head.Language != "go" || !head.Open || head.Chunks < 2 {
		t.Fatalf("header = %+v", head)
	}

	var got strings.Builder
	for i := range head.Chunks {
		m, size := rig.read(t)
		if m.Kind != "chunk" || m.Req != head.Req || m.Seq != i {
			t.Fatalf("frame %d = kind %q req %d seq %d", i, m.Kind, m.Req, m.Seq)
		}
		if size > maxOutboundFrame {
			t.Fatalf("frame %d is %d bytes, over Chrome's limit", i, size)
		}
		got.WriteString(m.Data)
	}
	if got.String() != content {
		t.Fatal("reassembled content differs from what the CLI sent")
	}

	url := "chrome-extension://" + extensionID + "/index.html#/b/abc"
	rig.reply(t, inboundFrame{Kind: "result", Req: head.Req, ID: "abc", URL: url})

	if res := wait(t, result); res.err != nil || res.url != url {
		t.Fatalf("sendLocal = %q, %v; want %q", res.url, res.err, url)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("socket mode = %o, want 600", perm)
	}
	dirInfo, _ := os.Stat(filepath.Dir(path))
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("socket dir mode = %o, want 700", perm)
	}

	rig.toHost.Close()
	if code := rig.exit(t); code != exitOK {
		t.Errorf("host exit = %d, want 0 on stdin EOF", code)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket still present after exit: %v", err)
	}
}

func TestNativeHostPassesExtensionErrorsToCLI(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "local.sock")
	rig := startHost(t, path, nil)

	result := sendAsync(path, localRequest{Content: "# hi\n", Type: "markdown"})
	head, _ := rig.read(t)
	rig.read(t) // the single chunk
	rig.reply(t, inboundFrame{Kind: "result", Req: head.Req, Error: "quota exceeded"})

	res := wait(t, result)
	if res.err == nil || !strings.Contains(res.err.Error(), "quota exceeded") {
		t.Fatalf("err = %v, want the extension's error", res.err)
	}
}

func TestNativeHostTimesOutWhenExtensionIsSilent(t *testing.T) {
	orig := hostResultTimeout
	hostResultTimeout = 50 * time.Millisecond
	t.Cleanup(func() { hostResultTimeout = orig })

	path := filepath.Join(shortTempDir(t), "local.sock")
	rig := startHost(t, path, nil)

	result := sendAsync(path, localRequest{Content: "x", Type: "text"})
	rig.read(t)
	rig.read(t)

	res := wait(t, result)
	if res.err == nil || !strings.Contains(res.err.Error(), "did not answer") {
		t.Fatalf("err = %v, want a timeout error", res.err)
	}
}

func TestNativeHostFailsPendingRequestWhenBrowserCloses(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "local.sock")
	rig := startHost(t, path, nil)

	result := sendAsync(path, localRequest{Content: "x", Type: "text"})
	rig.read(t)
	rig.read(t)
	rig.toHost.Close()

	res := wait(t, result)
	if res.err == nil || !strings.Contains(res.err.Error(), "disconnected") {
		t.Fatalf("err = %v, want a disconnect error", res.err)
	}
}

func TestNativeHostRejectsEmptyContent(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "local.sock")
	startHost(t, path, nil)

	if _, err := sendLocal(path, localRequest{Type: "text"}); err == nil ||
		!strings.Contains(err.Error(), "empty") {
		t.Fatalf("err = %v, want an empty-content error", err)
	}
}

func TestNativeHostExitsOnOversizeBrowserFrame(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "local.sock")
	rig := startHost(t, path, nil)

	var hdr [4]byte
	binary.NativeEndian.PutUint32(hdr[:], maxInboundFrame+1)
	go rig.toHost.Write(hdr[:])

	if code := rig.exit(t); code != exitError {
		t.Errorf("exit = %d, want %d", code, exitError)
	}
}

func TestNativeHostTakesOverStaleSocket(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "local.sock")

	// A socket file nobody listens on, as left by a crashed host.
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	stale.Close()
	staleInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	rig := startHost(t, path, staleInfo)

	result := sendAsync(path, localRequest{Content: "x", Type: "text"})
	head, _ := rig.read(t)
	rig.read(t)
	rig.reply(t, inboundFrame{Kind: "result", Req: head.Req, ID: "a", URL: "u"})
	if res := wait(t, result); res.err != nil {
		t.Fatalf("yeet through the new host failed: %v", res.err)
	}
}

func TestExitingHostLeavesItsSuccessorsSocket(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "local.sock")

	first := startHost(t, path, nil)
	firstInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	second := startHost(t, path, firstInfo)

	first.toHost.Close()
	first.exit(t)

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the first host removed its successor's socket: %v", err)
	}

	result := sendAsync(path, localRequest{Content: "x", Type: "text"})
	head, _ := second.read(t)
	second.read(t)
	second.reply(t, inboundFrame{Kind: "result", Req: head.Req, ID: "b", URL: "u2"})
	if res := wait(t, result); res.err != nil || res.url != "u2" {
		t.Fatalf("sendLocal = %q, %v; want the second host to answer", res.url, res.err)
	}
}

func TestSendLocalReportsUnreachableSocket(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "nobody-home.sock")
	if _, err := sendLocal(path, localRequest{Content: "x"}); !errors.Is(err, errLocalUnreachable) {
		t.Fatalf("err = %v, want errLocalUnreachable", err)
	}
}
