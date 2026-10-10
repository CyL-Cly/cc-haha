package clibootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/computeruse"
	"github.com/NanmiCoder/cc-haha/backend/internal/computerusemcp"
	"github.com/NanmiCoder/cc-haha/backend/internal/curepl"
)

type cuReplayRow struct {
	Command string         `json:"command"`
	Payload map[string]any `json:"payload"`
}
type cuReplayOutput struct {
	IsError bool             `json:"isError"`
	Content []curepl.Content `json:"content"`
}

func TestActualSourceCURecordedHandlesStateScreenshotWindowAndNativeGuards(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		bun = os.Getenv("CC_HAHA_PINNED_BUN")
	}
	if bun == "" {
		t.Skip("actual Source requires pinned Bun")
	}
	root, e := filepath.Abs("../../..")
	if e != nil {
		t.Fatal(e)
	}
	fixture := filepath.Join(root, "backend", "testdata", "native-cu-handle-source.fixture.ts")

	rows := []struct {
		name  string
		cells []string
	}{
		{"recorded_native_rejection", []string{`let app=await cua.getApp('Fixture');`, `nodeRepl.write(await app.getAXState({disableDiffing:true,emit:false}));`, `await app.click('g1:935');`, `nodeRepl.write(await app.getAXState({disableDiffing:true,emit:false}));`, `await app.click('g1:935');`, `nodeRepl.write('binding survives native rejection');`}},
		{"actionable", []string{`let app=await cua.getApp('Fixture');`, `await app.click('g1:935');await app.click(935);nodeRepl.write('handle and integer dispatched');`, `await app.getScreenshot({emit:false});`, `await app.click(935);`, `await app.click('g1:935');nodeRepl.write('opaque retained after screenshot');`}},
		{"window_change_high_dpi", []string{`let app=await cua.getApp('Fixture');`, `await app.click(935);`, `await app.getScreenshot({emit:false});`, `await app.click('g1:935');`, `await app.click(935);`, `await app.getAXState({disableDiffing:true,emit:false});await app.click(935);nodeRepl.write('new window handle');`}},
		{"different_apps", []string{`let app=await cua.getApp('Fixture');let other=await cua.getApp('Other');`, `await app.click(935);await other.click(935);`, `await other.getScreenshot({emit:false});`, `await app.click(935);nodeRepl.write('other app capture does not erase app index');`, `await other.click(935);`}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			f, service, options := computerUseMainFixture(t)
			var pngBytes bytes.Buffer
			if e := png.Encode(&pngBytes, image.NewNRGBA(image.Rect(0, 0, 64, 32))); e != nil {
				t.Fatal(e)
			}
			shot := base64.StdEncoding.EncodeToString(pngBytes.Bytes())
			var mu sync.Mutex
			calls := []cuReplayRow{}
			states := map[int]int{}
			generations := map[int]int{}
			nativeRejections := 0
			nativeAccepted := 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request cuReplayRow
				if e := json.NewDecoder(r.Body).Decode(&request); e != nil {
					http.Error(w, "bad owned replay", 400)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, request)
				reply := map[string]any{"ok": true, "result": map[string]any{}}
				p := request.Payload
				pid := 42
				name := "Fixture"
				bundle := "fixture.app"
				path := "/private/Fixture.app"
				if p["app"] == "Other" || p["app"] == "/private/Other.app" || p["pid"] == float64(43) {
					pid = 43
					name = "Other"
					bundle = "other.app"
					path = "/private/Other.app"
				}
				switch request.Command {
				case "check_permissions":
					reply["result"] = map[string]any{"accessibility": true, "screenRecording": true}
				case "resolve_app_target":
					reply["result"] = map[string]any{"pid": pid, "bundleId": bundle, "path": path, "executablePath": path + "/Contents/MacOS/Fixture", "launchTime": 17}
				case "get_app_state":
					states[pid]++
					gen := 1
					if pid == 43 {
						gen = 3
					}
					if row.name == "window_change_high_dpi" && states[pid] >= 2 {
						gen = 2
					}
					generations[pid] = gen
					reply["result"] = map[string]any{"axText": fmt.Sprintf("App=%s (pid %d)\nWindow: \"Window-%d\", App: %s.\ng%d:935 button Voice Input", bundle, pid, gen, name, gen), "bundleId": bundle, "screenshot": map[string]any{"base64": shot, "mimeType": "image/png", "width": 64, "height": 32, "pixelsPerPoint": 2}}
				case "click":
					if _, has := p["x"]; has {
						reply = map[string]any{"ok": false, "error": "coordinate fallback prohibited", "code": "bad_payload"}
						break
					}
					identity, ok := p["expectedProcessIdentity"].(map[string]any)
					if !ok || p["pid"] != float64(pid) || identity["bundleId"] != bundle || identity["launchTime"] != float64(17) {
						reply = map[string]any{"ok": false, "error": "unproven target", "code": "stale_process"}
						break
					}
					wanted := fmt.Sprintf("g%d:935", generations[pid])
					if p["index"] != wanted {
						reply = map[string]any{"ok": false, "error": "Snapshot handle is stale", "code": "stale_snapshot"}
						break
					}
					if row.name == "recorded_native_rejection" {
						nativeRejections++
						reply = map[string]any{"ok": false, "error": "No snapshot element 935; call get_app_state", "code": "stale_element"}
					} else {
						nativeAccepted++
						marker := filepath.Join(f.cwd, "accepted-handles.jsonl")
						data, _ := json.Marshal(map[string]any{"pid": pid, "index": p["index"]})
						file, e := os.OpenFile(marker, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
						if e != nil {
							t.Error(e)
						} else {
							if _, e := file.Write(append(data, '\n')); e != nil {
								t.Error(e)
							}
							if e := file.Close(); e != nil {
								t.Error(e)
							}
						}
					}
				default:
					reply = map[string]any{"ok": false, "error": "unowned native command " + request.Command, "code": "bad_command"}
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(reply)
			}))
			defer peer.Close()
			sourceHome := t.TempDir()
			for _, name := range []string{".claude", "tmp"} {
				if e := os.MkdirAll(filepath.Join(sourceHome, name), 0700); e != nil {
					t.Fatal(e)
				}
			}
			cmd := exec.Command(bun, "--no-env-file", fixture, root)
			cmd.Dir = sourceHome
			cmd.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + filepath.Join(sourceHome, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_CU_WORKER_FIXTURE=1", "NODE_ENV=test", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
			input, _ := json.Marshal(map[string]any{"peerURL": peer.URL, "cells": row.cells})
			cmd.Stdin = bytes.NewReader(input)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			raw, e := cmd.Output()
			if e != nil {
				t.Fatal("actual Source worker/bridge", e, stderr.String())
			}
			var want []cuReplayOutput
			if e = json.Unmarshal(raw, &want); e != nil {
				t.Fatal(e, string(raw))
			}
			mu.Lock()
			sourceCalls := append([]cuReplayRow{}, calls...)
			sourceRejects := nativeRejections
			sourceAccepted := nativeAccepted
			calls = nil
			states = map[int]int{}
			generations = map[int]int{}
			nativeRejections = 0
			nativeAccepted = 0
			mu.Unlock()
			native, e := computeruse.NewNativeTools(computeruse.NativeToolsOptions{Service: service, SessionID: "fixture-session", NativeCall: func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
				var p map[string]any
				if e := json.Unmarshal(args, &p); e != nil {
					return nil, e
				}
				body, _ := json.Marshal(cuReplayRow{name, p})
				req, e := http.NewRequestWithContext(ctx, "POST", peer.URL, bytes.NewReader(body))
				if e != nil {
					return nil, e
				}
				res, e := peer.Client().Do(req)
				if e != nil {
					return nil, e
				}
				defer res.Body.Close()
				var reply struct {
					OK          bool            `json:"ok"`
					Result      json.RawMessage `json:"result"`
					Error, Code string
				}
				if e := json.NewDecoder(res.Body).Decode(&reply); e != nil {
					return nil, e
				}
				if !reply.OK {
					return nil, &computeruse.NativeCommandError{Message: reply.Error, Code: reply.Code}
				}
				return reply.Result, nil
			}})
			if e != nil {
				t.Fatal(e)
			}
			kernel, e := curepl.NewRuntime(*options.RuntimeOptions)
			if e != nil {
				t.Fatal(e)
			}
			binder, e := computerusemcp.New(native, kernel)
			if e != nil {
				t.Fatal(e)
			}
			defer binder.Close(context.Background())
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			got := []cuReplayOutput{}
			for _, code := range row.cells {
				input, _ := json.Marshal(map[string]any{"code": code})
				result := binder.Call(ctx, "js", input)
				got = append(got, cuReplayOutput{result.IsError, result.Content})
			}
			mu.Lock()
			goCalls := append([]cuReplayRow{}, calls...)
			goRejects := nativeRejections
			goAccepted := nativeAccepted
			mu.Unlock()
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want)
			var gv, wv any
			json.Unmarshal(gotJSON, &gv)
			json.Unmarshal(wantJSON, &wv)
			if !reflect.DeepEqual(gv, wv) {
				t.Fatalf("actual Source outputs Go%s Source%s", gotJSON, wantJSON)
			}
			if !reflect.DeepEqual(goCalls, sourceCalls) {
				g, _ := json.Marshal(goCalls)
				s, _ := json.Marshal(sourceCalls)
				t.Fatalf("actual helper protocol Go%s Source%s", g, s)
			}
			if row.name == "recorded_native_rejection" && (goRejects != 2 || sourceRejects != 2) {
				t.Fatal("recorded failure not reproduced equally", goRejects, sourceRejects)
			}
			marker, markerError := os.ReadFile(filepath.Join(f.cwd, "accepted-handles.jsonl"))
			if row.name == "recorded_native_rejection" {
				if !os.IsNotExist(markerError) {
					t.Fatal("rejected handles must never write an accepted marker", markerError)
				}
			} else {
				// Source and Go independently wrote every accepted opaque handle.
				if sourceAccepted != goAccepted || goAccepted == 0 || markerError != nil || bytes.Count(marker, []byte("\n")) != sourceAccepted+goAccepted {
					t.Fatal("actual helper acceptance marker", markerError, string(marker), sourceAccepted, goAccepted)
				}
			}
			if e := binder.Close(ctx); e != nil {
				t.Fatal("actual kernel/native owner join", e)
			}
			t.Log("actual original JS worker+bindSessionContext+executor and actual isolated Go owner/binder/nativeTools used identical helper payloads/results; no coordinate fallback/GUI/TCC")
		})
	}
}
