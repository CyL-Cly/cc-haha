package clibootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"
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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSDKInputImageReachesModelAndCanonicalHistory(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 12, 8))); err != nil {
		t.Fatal(err)
	}
	data := base64.StdEncoding.EncodeToString(encoded.Bytes())
	var mu sync.Mutex
	var requests []protocol.JSONObject
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request protocol.JSONObject
		if err := request.UnmarshalJSON(body); err != nil {
			t.Error(err)
		}
		mu.Lock()
		requests = append(requests, request)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(`{"type":"text","text":"ACTUAL_SDK_MEDIA_OK"}`)}))
	}))
	defer peer.Close()
	var output bytes.Buffer
	m := newNativeImageHarness(t, peer.URL, peer.Certificate().Raw, func(b []byte) error { output.Write(b); return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	content := rawMain([]any{map[string]any{"type": "text", "text": "Inspect my private test image."}, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "mediaType": "image/png", "data": data}, "future": true}})
	input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "uuid", Value: quoteMain("11111111-1111-4111-8111-111111111119")}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: content}))})
	if err := m.RunTurn(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	sid := m.Controller.SessionID()
	var transcriptPath string
	_ = filepath.WalkDir(filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "projects"), func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasSuffix(path, sid+".jsonl") {
			transcriptPath = path
		}
		return nil
	})
	before, err := os.ReadFile(transcriptPath)
	if err != nil || !bytes.Contains(before, []byte(data)) || !bytes.Contains(before, []byte("ACTUAL_SDK_MEDIA_OK")) {
		t.Fatalf("canonical image turn missing: %v", err)
	}
	var persistedImage bool
	for _, line := range bytes.Split(before, []byte("\n")) {
		var envelope protocol.JSONObject
		_ = envelope.UnmarshalJSON(line)
		message, _ := envelope.Object("message")
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(message.Get("content"), &blocks)
		for _, block := range blocks {
			if block.String("type") == "image" {
				source, _ := block.Object("source")
				if source.String("media_type") != "image/png" || source.Get("mediaType") != nil || block.Get("future") != nil {
					t.Fatalf("unnormalized canonical image %s", line)
				}
				persistedImage = true
			}
		}
	}
	if !persistedImage {
		t.Fatal("canonical contains no actual image")
	}
	options := m.options
	options.Native = m.options.Native.agentScopedBoundary(m.options.Native.CWD)
	options.Arguments.Resume = sid
	options.Arguments.SessionID = ""
	reopened, err := NewMainPrint(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.Close)
	if err := reopened.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	if reopened.Controller.SessionID() != sid {
		t.Fatal("cold resume changed actual session ID")
	}
	if err := reopened.RunTurn(ctx, webObject(`{"type":"user","message":{"role":"user","content":"Continue from the private image."}}`)); err != nil {
		t.Fatal(err)
	}
	if err := reopened.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(transcriptPath)
	if err != nil || !bytes.HasPrefix(after, before) {
		t.Fatal("cold resume rewrote canonical prefix", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatal("actual requests", len(requests))
	}
	var history []protocol.JSONObject
	if err := json.Unmarshal(requests[0].Get("messages"), &history); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range history {
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(message.Get("content"), &blocks)
		for _, block := range blocks {
			if block.String("type") == "image" {
				source, _ := block.Object("source")
				if source.String("media_type") != "image/png" || source.String("data") != data || block.Get("future") != nil {
					t.Fatalf("actual normalized image %s", rawMain(block))
				}
				found = true
			}
		}
	}
	if !bytes.Contains(requests[1].Get("messages"), []byte(data)) || !bytes.Contains(requests[1].Get("messages"), []byte("ACTUAL_SDK_MEDIA_OK")) {
		t.Fatal("same SID resumed model request lost actual image/assistant")
	}
	if !found || !bytes.Contains(output.Bytes(), []byte("ACTUAL_SDK_MEDIA_OK")) {
		t.Fatalf("media never consumed by actual Harness: %s", output.Bytes())
	}
}

func TestActualSDKMediaDocumentSourcesAndResizeMetadataRemainOrdered(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 9001, 3))); err != nil {
		t.Fatal(err)
	}
	media := webObject(`{"type":"image","source":{"type":"base64","media_type":"image/png","data":""}}`)
	source, _ := media.Object("source")
	source.Set("data", quoteMain(base64.StdEncoding.EncodeToString(encoded.Bytes())))
	media.Set("source", rawMain(source))
	documents := []protocol.JSONObject{
		webObject(`{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"JVBERi0xLjQ="},"title":"Private title","context":"Private context","citations":{"enabled":true},"cache_control":{"type":"ephemeral"},"future":{"raw":"\ud800"}}`),
		webObject(`{"type":"document","source":{"type":"text","media_type":"text/plain","data":"Private text"}}`),
		webObject(`{"type":"document","source":{"type":"url","url":"https://private.invalid/not-fetched"}}`),
		webObject(`{"type":"document","source":{"type":"content","content":"Private content"}}`),
		webObject(`{"type":"document","source":{"type":"content","content":[{"type":"text","text":"content source"},{"type":"image","source":{"type":"url","url":"https://private.invalid/nested-not-fetched"}}]}}`),
	}
	urlImage := webObject(`{"type":"image","source":{"type":"url","url":"https://private.invalid/not-fetched","future":"kept"},"future":{"raw":"\ud800"}}`)
	var observed protocol.JSONObject
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = observed.UnmarshalJSON(body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(`{"type":"text","text":"ALL_SDK_MEDIA_OK"}`)}))
	}))
	defer peer.Close()
	m := newNativeImageHarness(t, peer.URL, peer.Certificate().Raw, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	blocks := []protocol.JSONObject{media}
	blocks = append(blocks, documents...)
	blocks = append(blocks, urlImage, webObject(`{"type":"text","text":"Actual last text prompt."}`))
	input := webObject(`{"type":"user","message":{"role":"user","content":[]}}`)
	message, _ := input.Object("message")
	message.Set("content", rawMain(blocks))
	input.Set("message", rawMain(message))
	if err := m.RunTurn(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	var history []protocol.JSONObject
	_ = json.Unmarshal(observed.Get("messages"), &history)
	var got []protocol.JSONObject
	for _, user := range history {
		if user.String("role") == "user" {
			var parts []protocol.JSONObject
			_ = json.Unmarshal(user.Get("content"), &parts)
			got = append(got, parts...)
		}
	}
	// The ordinary shared Harness prepends its actual currentDate context.
	if len(got) > 0 && got[0].String("type") == "text" && strings.HasPrefix(got[0].String("text"), "<system-reminder>\nAs you answer the user's questions") {
		got = got[1:]
	}
	if len(got) != len(blocks)+1 {
		t.Fatalf("media+metadata count %d want%d: %s", len(got), len(blocks)+1, observed.Get("messages"))
	}
	for i := 1; i < len(blocks)-1; i++ {
		a, _ := protocol.ParseAndStringifyJavaScriptJSON(rawMain(got[i]))
		b, _ := protocol.ParseAndStringifyJavaScriptJSON(rawMain(blocks[i]))
		if string(a) != string(b) {
			t.Errorf("raw media%d Go%s want%s", i, a, b)
		}
	}
	codec, _ := imageprocessing.BundledCodec(m.options.Native.ImageLibraryPath)
	normalized, metadata, err := imageprocessing.InputBlocks(context.Background(), codec, rawMain(blocks))
	if err != nil {
		t.Fatal(err)
	}
	var expected []protocol.JSONObject
	_ = json.Unmarshal(normalized, &expected)
	first, _ := got[0].Object("source")
	expectedFirst, _ := expected[0].Object("source")
	if first.String("data") != expectedFirst.String("data") || len(metadata) != 1 {
		t.Fatal("main raw image did not resize")
	}
	text := got[len(got)-1].String("text")
	if text != decodeMediaText(metadata[0]) {
		t.Fatalf("actual metadata text %q expected%s", text, metadata)
	}
	var transcript []byte
	_ = filepath.WalkDir(filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "projects"), func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasSuffix(path, m.Controller.SessionID()+".jsonl") {
			transcript, _ = os.ReadFile(path)
		}
		return nil
	})
	if !bytes.Contains(transcript, metadata[0]) || !bytes.Contains(transcript, []byte(`"isMeta":true`)) {
		t.Fatal("image dimensions meta not canonical", string(transcript))
	}
}

func TestNativeInputMediaRejectsForeignOwnerAndHonorsCancellation(t *testing.T) {
	m := openAIAuthMainFixture(t)
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, _, err := m.nativeInputMedia(context.Background(), scope, json.RawMessage(`[]`))
		if !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("foreign scope admitted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, _, err := m.nativeInputMedia(ctx, scope, json.RawMessage(`[]`))
		if !errors.Is(err, context.Canceled) {
			return errors.New("canceled normalization admitted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func decodeMediaText(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
