package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalPastedImageReachesActualModelCanonicalCacheAndNextTurn(t *testing.T) {
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewNRGBA(image.Rect(0, 0, 12, 8))); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(imageData.Bytes())
	var mu sync.Mutex
	var requests []json.RawMessage
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()
		response(w, "terminal-image", "", nil, "TERMINAL IMAGE OK")
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	input := tui.Input{Text: "Inspect [Image #7]", Mode: "prompt", PastedContents: map[int]tui.PastedContent{7: {ID: 7, Type: "image", Content: encoded, MediaType: "image/png"}}}
	if err = s.Submit(ctx, input, nil); err != nil {
		t.Fatalf("terminal image did not enter shared Harness: %v", err)
	}
	cache := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "image-cache", s.process.Controller.SessionID(), "7.png")
	data, err := os.ReadFile(cache)
	if err != nil || !bytes.Equal(data, imageData.Bytes()) {
		t.Fatal("original pasted bytes missing from session cache", err)
	}
	info, err := os.Stat(cache)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("pasted image cache permissions", info, err)
	}
	if err = s.Submit(ctx, tui.Input{Text: "Continue from the image.", Mode: "prompt"}, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	var transcript []byte
	err = filepath.WalkDir(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects"), func(path string, entry os.DirEntry, failure error) error {
		if failure != nil {
			return failure
		}
		if !entry.IsDir() && strings.HasSuffix(path, s.process.Controller.SessionID()+".jsonl") {
			transcript, failure = os.ReadFile(path)
		}
		return failure
	})
	if err != nil || !bytes.Contains(transcript, []byte(`"imagePasteIds":[7]`)) || !bytes.Contains(transcript, []byte(encoded)) {
		t.Fatal("canonical image/ID missing", err)
	}
	for _, line := range bytes.Split(transcript, []byte("\n")) {
		if bytes.Contains(line, []byte("Continue from the image.")) && bytes.Contains(line, []byte("imagePasteIds")) {
			t.Fatal("paste display IDs leaked into the following submission")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("actual request count %d", len(requests))
	}
	for _, request := range requests {
		if !bytes.Contains(request, []byte(encoded)) || !bytes.Contains(request, []byte("image/png")) || !bytes.Contains(request, []byte(cache)) {
			t.Fatal("actual model request lost image content or source path")
		}
	}
}

func TestTerminalAttachmentUsesActualSharedFileContext(t *testing.T) {
	var captured []byte
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		response(w, "terminal-file", "", nil, "TERMINAL FILE OK")
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	path := filepath.Join(o.WorkDir, "中文 input.txt")
	if err := os.WriteFile(path, []byte("ACTUAL_TERMINAL_FILE_NONCE"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if err = s.Submit(context.Background(), tui.Input{Text: "Inspect the attached file.", Mode: "prompt", Attachments: []json.RawMessage{raw(map[string]any{"type": "file", "path": path, "name": "中文 input.txt"})}}, nil); err != nil {
		t.Fatal("attachment did not enter real input consumer", err)
	}
	if !bytes.Contains(captured, []byte("ACTUAL_TERMINAL_FILE_NONCE")) {
		t.Fatal("file bytes did not reach actual shared attachment consumer")
	}
}

func TestTerminalPastedImageSkillRetainsActualSourceMetadataWithoutPasteIDs(t *testing.T) {
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewNRGBA(image.Rect(0, 0, 12, 8))); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(picture.Bytes())
	var captured []byte
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		response(w, "skill-image", "", nil, "SKILL IMAGE OK")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	path := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "skills", "image-check", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\ndescription: Inspect an actual pasted image\n---\nPRIVATE_IMAGE_SKILL_PROMPT\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	input := tui.Input{Text: "/image-check inspect this image", Mode: "prompt", PastedContents: map[int]tui.PastedContent{7: {ID: 7, Type: "image", Content: encoded, MediaType: "image/png"}}}
	if err = s.Submit(ctx, input, nil); err != nil {
		t.Fatal("actual skill invocation", err)
	}
	cache := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "image-cache", s.process.Controller.SessionID(), "7.png")
	if !bytes.Contains(captured, []byte("PRIVATE_IMAGE_SKILL_PROMPT")) || !bytes.Contains(captured, []byte(encoded)) {
		t.Fatal("actual skill/image did not reach the model")
	}
	if !bytes.Contains(captured, []byte(cache)) {
		t.Fatal("actual skill lost pasted image source metadata")
	}
	if err = s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", "*", "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	disk, err := os.ReadFile(files[0])
	if err != nil || !bytes.Contains(disk, []byte(cache)) || bytes.Contains(disk, []byte("imagePasteIds")) {
		t.Fatal("Source slash image metadata/display identity semantics", err)
	}
}

func TestTerminalImageCancellationDoesNotPoisonNextSubmission(t *testing.T) {
	var calls int
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		response(w, "terminal-recovery", "", nil, "RECOVERED")
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = s.Submit(ctx, tui.Input{Text: "Canceled image", Mode: "prompt", PastedContents: map[int]tui.PastedContent{1: {ID: 1, Type: "image", Content: "canceled"}}}, nil); err == nil {
		t.Fatal("canceled image was sent to provider")
	}
	if calls != 0 {
		t.Fatal("canceled bitmap reached physical model request")
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "Recovery", Mode: "prompt"}, nil); err != nil || calls != 1 {
		t.Fatal("image failure poisoned following turn", err, calls)
	}
}
