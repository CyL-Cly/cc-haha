package clibootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/imagegeneration"
)

func TestActualImageCombinedSignalTimerCallerAbortFinallyAndPhysicalCloseMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	var partialClosed atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		kind := r.URL.Query().Get("kind")
		if kind == "timeout" || kind == "caller_abort" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[`)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			partialClosed.Add(1)
			return
		}
		if kind == "upstream_error" {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, "private-fake-key private image denial")
			return
		}
		if kind == "invalid_output" {
			_, _ = io.WriteString(w, `{"data":[{"b64_json":"bm90LWltYWdl"}]}`)
			return
		}
		_, _ = fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(nativeImagePNG))
	}))
	defer peer.Close()
	rows := []map[string]any{}
	for _, kind := range []string{"success", "upstream_error", "invalid_output", "timeout", "caller_abort", "preaborted"} {
		rows = append(rows, map[string]any{"kind": kind, "url": peer.URL + "/image?kind=" + kind})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-image-signal.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_IMAGE_SIGNAL_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(rows)
	command.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	raw, err := command.Output()
	if err != nil {
		t.Fatal("actual image signal Source", err, stderr.String())
	}
	var expected []map[string]any
	if err = json.Unmarshal(raw, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal("Source signal profiles", err, string(raw))
	}
	m := openAIAuthMainFixture(t)
	err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		for index, row := range rows {
			kind := row["kind"].(string)
			parent, abortParent := context.WithCancelCause(context.Background())
			defer abortParent(context.Canceled)
			if kind == "preaborted" {
				abortParent(errors.New("private custom reason"))
			}
			if kind == "caller_abort" {
				time.AfterFunc(25*time.Millisecond, func() { abortParent(errors.New("private custom reason")) })
			}
			var armed any
			var combined context.Context
			service, err := imagegeneration.New(imagegeneration.Options{Files: nativeOwnedImageFiles{m, scope, imagegeneration.NativeFiles{Check: m.options.Native.check}}, Fetch: func(ctx context.Context, request imagegeneration.Request) (imagegeneration.Response, error) {
				request.URL = row["url"].(string)
				combined = ctx
				return m.nativeImageFetch(ctx, scope, request)
			}, CombinedSignal: func(ctx context.Context, delay time.Duration) (context.Context, func() error, error) {
				if ctx.Err() == nil {
					armed = float64(delay.Milliseconds())
				}
				if kind == "timeout" {
					delay = 25 * time.Millisecond
				}
				return m.nativeCombinedImageSignal(scope, ctx, delay)
			}, Environment: m.nativeOpenAIEnvironment, ConfigHome: m.options.Native.Environment["CLAUDE_CONFIG_DIR"], OutputDir: filepath.Join(m.options.Native.Home, kind), SessionID: m.Controller.SessionID, NowMillis: func() int64 { return int64(m.options.Native.NowMillis()) }, UUID: uuidMain})
			if err != nil {
				return err
			}
			result, failure := service.Generate(parent, webObject(`{"prompt":"original combined image signal","count":1}`), imagegeneration.Config{Kind: "openai_images", ProviderID: "private", Model: "private-image", APIKey: "private-fake-key", BaseURL: peer.URL})
			if kind == "success" || kind == "upstream_error" || kind == "invalid_output" {
				abortParent(context.Canceled)
			}
			if _, err = scope.AwaitBytes(cooperative.StreamNext, func(context.Context) ([]byte, error) { time.Sleep(40 * time.Millisecond); return nil, nil }); err != nil {
				return err
			}
			actual := map[string]any{"kind": kind, "armed": armed, "aborted": combined != nil && combined.Err() != nil, "images": float64(len(result.Images))}
			if failure != nil {
				actual["error"] = failure.Error()
			}
			if !reflect.DeepEqual(actual, expected[index]) {
				t.Errorf("signal row%d Go%v Source%v", index, actual, expected[index])
			}
			abortParent(context.Canceled)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if partialClosed.Load() != 4 {
		t.Fatal("actual Source/native timeout and caller-cancel sockets not joined", partialClosed.Load())
	}
}
