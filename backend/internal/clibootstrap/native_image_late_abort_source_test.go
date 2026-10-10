package clibootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/imagegeneration"
)

func TestActualImageLateCallerAbortAfterCompleteProviderJSONPhase(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-fake-key" {
			t.Error("actual physical image credential changed")
			http.Error(w, "unauthenticated", 401)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(nativeImagePNG))
	}))
	defer peer.Close()
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-image-late-abort-phase.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_IMAGE_SIGNAL_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"url": peer.URL + "/image"}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	source, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var expected []map[string]any
	if e = json.Unmarshal(source, &expected); e != nil {
		t.Fatal(e)
	}
	sourcePhases := []any{}
	for _, row := range expected {
		sourcePhases = append(sourcePhases, row["phase"])
		delete(row, "phase")
	}
	m := openAIAuthMainFixture(t)
	actualRows := []any{}
	goPhases := []any{}
	e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		for i, kind := range []string{"normal", "abort-after-complete-provider-json"} {
			started := time.Now()
			phase := []map[string]any{}
			record := func(event string, s context.Context, failure error) {
				row := map[string]any{"event": event, "ms": float64(time.Since(started).Microseconds()) / 1000}
				if s != nil {
					row["aborted"] = s.Err() != nil
				}
				if failure != nil {
					row["error"] = failure.Error()
				}
				phase = append(phase, row)
			}
			parent, abort := context.WithCancel(context.Background())
			var combined context.Context
			service, e := imagegeneration.New(imagegeneration.Options{Files: imageSignalPhaseFiles{Files: nativeOwnedImageFiles{m, scope, imagegeneration.NativeFiles{Check: m.options.Native.check}}, Record: record}, Fetch: func(s context.Context, request imagegeneration.Request) (imagegeneration.Response, error) {
				combined = s
				request.URL = peer.URL + "/image?kind=" + kind
				record("fetch-begin", s, nil)
				response, e := m.nativeImageFetch(s, scope, request)
				record("provider-body-complete", s, e)
				if e == nil && response.ReadError == nil && kind == "abort-after-complete-provider-json" {
					record("caller-abort", s, nil)
					abort()
					// Receive the original owner's actual cancellation before the next phase,
					// matching Source's synchronous abort dispatch before response.json returns.
					_, e = scope.AwaitBytes(cooperative.StreamNext, func(context.Context) ([]byte, error) { <-s.Done(); return nil, nil })
					if e != nil {
						return imagegeneration.Response{}, e
					}
					record("caller-abort-delivered", s, nil)
				}
				return response, e
			}, CombinedSignal: func(s context.Context, delay time.Duration) (context.Context, func() error, error) {
				record("timeout-arm-original-600000", s, nil)
				c, cleanup, e := m.nativeCombinedImageSignal(scope, s, delay)
				return c, func() error { record("cleanup-begin", c, nil); e := cleanup(); record("cleanup-end", c, e); return e }, e
			}, Environment: m.nativeOpenAIEnvironment, ConfigHome: m.options.Native.Environment["CLAUDE_CONFIG_DIR"], OutputDir: filepath.Join(m.options.Native.Home, kind), SessionID: m.Controller.SessionID, NowMillis: func() int64 { return int64(m.options.Native.NowMillis()) }, UUID: uuidMain})
			if e != nil {
				return e
			}
			value, failure := service.Generate(parent, webObject(`{"prompt":"original combined image signal","count":1}`), imagegeneration.Config{Kind: "openai_images", ProviderID: "private", Model: "private-image", APIKey: "private-fake-key", BaseURL: peer.URL})
			record("generate-return", combined, failure)
			files, _ := os.ReadDir(filepath.Join(m.options.Native.Home, kind))
			actual := map[string]any{"kind": kind, "images": float64(len(value.Images)), "aborted": combined != nil && combined.Err() != nil, "files": float64(len(files))}
			if failure != nil {
				actual["error"] = failure.Error()
			}
			actualRows = append(actualRows, actual)
			goPhases = append(goPhases, phase)
			if !reflect.DeepEqual(actual, expected[i]) {
				t.Errorf("late caller phase %s Go%v Source%v", kind, actual, expected[i])
			}
			abort()
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	data, _ := json.MarshalIndent(map[string]any{"sourceRows": expected, "goRows": actualRows, "sourcePhases": sourcePhases, "goPhases": goPhases, "originalTimeoutMs": 600000, "physicalAuthenticatedHTTP": true, "noSleepOrDelayChange": true}, "", "  ")
	if e = os.WriteFile(filepath.Join(t.TempDir(), "image-late-caller-phase-evidence.json"), append(data, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
}

// Observers delegate the exact physical file operation and never add a wait.
type imageSignalPhaseFiles struct {
	imagegeneration.Files
	Record func(string, context.Context, error)
}

func (f imageSignalPhaseFiles) MkdirAll(ctx context.Context, path string, mode fs.FileMode) error {
	f.Record("mkdir-begin", ctx, nil)
	e := f.Files.MkdirAll(ctx, path, mode)
	f.Record("mkdir-end", ctx, e)
	return e
}
func (f imageSignalPhaseFiles) Chmod(ctx context.Context, path string, mode fs.FileMode) error {
	f.Record("chmod-begin", ctx, nil)
	e := f.Files.Chmod(ctx, path, mode)
	f.Record("chmod-end", ctx, e)
	return e
}
func (f imageSignalPhaseFiles) WriteExclusive(ctx context.Context, path string, data []byte, mode fs.FileMode) error {
	f.Record("write-begin", ctx, nil)
	e := f.Files.WriteExclusive(ctx, path, data, mode)
	f.Record("write-end", ctx, e)
	return e
}
