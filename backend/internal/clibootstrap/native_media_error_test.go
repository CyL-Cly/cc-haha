package clibootstrap

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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualMainMediaHTTPFailureIsCanonicalAndNextTurnRecovers(t *testing.T) {
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewNRGBA(image.Rect(0, 0, 8, 6))); err != nil {
		t.Fatal(err)
	}
	imageData := base64.StdEncoding.EncodeToString(imageBytes.Bytes())
	for _, row := range []struct {
		Name                                  string
		Status                                int
		Message, Code, Content, Removed, Kept string
	}{
		{"unsupported-image", 400, "This model does not support image blocks", "image_unsupported", `[{"type":"text","text":"Inspect this image."},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + imageData + `"}}]`, imageData, "Inspect this image."},
		{"unknown-image-wording", 422, "Only text is allowed by the provider", "image_unsupported", `[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + imageData + `"}}]`, imageData, "Recover from the rejected private media."},
		{"invalid-pdf", 400, "The PDF specified was not valid", "pdf_invalid", `[{"type":"text","text":"Inspect this PDF."},{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"JVBERi0xLjQ="}}]`, "JVBERi0xLjQ=", "Inspect this PDF."},
		{"pdf-limit", 400, "maximum of 100 PDF pages", "pdf_too_large", `[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"JVBERi0xLjQ="}}]`, "JVBERi0xLjQ=", "Recover from the rejected private media."},
		{"whole-request", 413, "relay rejected large payload", "request_too_large", `[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + imageData + `"}},{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"JVBERi0xLjQ="}}]`, imageData, "[image]"},
	} {
		t.Run(row.Name, func(t *testing.T) {
			var mu sync.Mutex
			requests := []json.RawMessage{}
			peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				requests = append(requests, body)
				number := len(requests)
				mu.Unlock()
				if number == 1 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(row.Status)
					_, _ = w.Write(rawMain(map[string]any{"type": "error", "error": map[string]any{"type": "invalid_request_error", "message": row.Message}}))
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(`{"type":"text","text":"ACTUAL_MEDIA_RECOVERED"}`)}))
			}))
			defer peer.Close()
			var output bytes.Buffer
			m := newNativeImageHarness(t, peer.URL, peer.Certificate().Raw, func(b []byte) error { output.Write(b); return nil })
			m.options.Native.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			input := webObject(`{"type":"user","message":{"role":"user","content":` + row.Content + `}}`)
			if err := m.RunTurn(ctx, input); err != nil {
				t.Fatal("media rejection escaped actual presentation", err)
			}
			// Original QueryEngine SDK assistant projection exposes error/text;
			// canonical errorDetails/businessErrorCode stay on the real record.
			if !bytes.Contains(output.Bytes(), []byte(`"is_error":true`)) {
				t.Fatal("actual SDK did not report a failed result", output.String())
			}
			if err := m.RunTurn(ctx, webObject(`{"type":"user","message":{"role":"user","content":"Recover from the rejected private media."}}`)); err != nil {
				t.Fatal(err)
			}
			if err := m.ShutdownSession(ctx); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(requests) != 2 {
				t.Fatal("failed media retry/continuation count", len(requests))
			}
			if bytes.Contains(requests[1], []byte(row.Removed)) || !bytes.Contains(requests[1], []byte(row.Kept)) {
				t.Fatalf("real following request did not recover: %s", requests[1])
			}
			if row.Code == "request_too_large" && (!bytes.Contains(requests[1], []byte("[document]")) || bytes.Contains(requests[1], []byte("JVBERi0xLjQ="))) {
				t.Fatal("413 document not replaced", string(requests[1]))
			}
			var transcript []byte
			_ = filepath.WalkDir(filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "projects"), func(path string, entry os.DirEntry, err error) error {
				if err == nil && !entry.IsDir() && strings.HasSuffix(path, m.Controller.SessionID()+".jsonl") {
					transcript, _ = os.ReadFile(path)
				}
				return nil
			})
			if !bytes.Contains(transcript, []byte(row.Removed)) || !bytes.Contains(transcript, []byte(row.Code)) || !bytes.Contains(transcript, []byte("ACTUAL_MEDIA_RECOVERED")) {
				t.Fatal("original media or genuine error/recovery missing canonical")
			}
		})
	}
}

func TestActualMainSSEMediaFailureAndCallerCancellationPreserveOriginalLifetimes(t *testing.T) {
	for _, mode := range []string{"pdf-error", "caller-abort-before-headers", "caller-abort"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			requests := []json.RawMessage{}
			entered := make(chan struct{})
			released := make(chan struct{})
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				requests = append(requests, body)
				number := len(requests)
				mu.Unlock()
				if number == 1 {
					w.Header().Set("Content-Type", "text/event-stream")
					if mode == "pdf-error" {
						_, _ = io.WriteString(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"The PDF specified was not valid\"}}\n\n")
						return
					}
					if mode != "caller-abort-before-headers" {
						w.WriteHeader(200)
						w.(http.Flusher).Flush()
					}
					close(entered)
					<-r.Context().Done()
					close(released)
					return
				}
				emitPlanFixture(w, number, "", "AFTER_MEDIA_SSE_OK")
			}))
			defer peer.Close()
			frames := []protocol.JSONObject{}
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
				n.Environment["CLAUDE_STREAM_TRANSIENT_RETRY_MAX"] = "0"
				n.Environment["CLAUDE_ENABLE_STREAM_WATCHDOG"] = "0"
				n.Environment["CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK"] = "1"
			}, func(o *MainPrintOptions) {
				o.OnOutput = func(b []byte) error {
					var value protocol.JSONObject
					err := value.UnmarshalJSON(b)
					frames = append(frames, value)
					return err
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			input := webObject(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Inspect private document."},{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"JVBERi0xLjQ="}}]}}`)
			if strings.HasPrefix(mode, "caller-abort") {
				turn, stop := context.WithCancel(ctx)
				done := make(chan error, 1)
				go func() { done <- m.RunTurn(turn, input) }()
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("actual stream not entered", ctx.Err())
				}
				stop()
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("actual caller abort did not finish", ctx.Err())
				}
				select {
				case <-released:
				case <-ctx.Done():
					t.Fatal("caller did not close actual response", ctx.Err())
				}
				for _, record := range m.Controller.History() {
					if record.Get("businessErrorCode") != nil {
						t.Fatal("caller abort fabricated media error", string(rawMain(record)))
					}
				}
			} else if err := m.RunTurn(ctx, input); err != nil {
				t.Fatal("actual SSE media error", err)
			}
			if err := m.RunTurn(ctx, webObject(`{"type":"user","message":{"role":"user","content":"Continue normally after stream."}}`)); err != nil {
				t.Fatal(err)
			}
			if err := m.ShutdownSession(ctx); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(requests) != 2 {
				t.Fatal("unexpected actual stream demands", len(requests))
			}
			hasDocument := bytes.Contains(requests[1], []byte("JVBERi0xLjQ="))
			if mode == "pdf-error" && hasDocument {
				t.Fatal("SSE PDF error did not strip rejected doc", string(requests[1]))
			}
			if mode == "caller-abort" && !hasDocument {
				t.Fatal("caller abort incorrectly stripped original doc")
			}
			results := 0
			for _, record := range frames {
				if record.String("type") == "result" {
					results++
					if results == 1 && mode == "pdf-error" && string(record.Get("is_error")) != "true" {
						t.Fatal("SSE rejection reported success")
					}
				}
			}
			if results != 2 {
				t.Fatal("actual final results", results)
			}
		})
	}
}
