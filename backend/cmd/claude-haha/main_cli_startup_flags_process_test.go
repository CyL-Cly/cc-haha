package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
)

const cliContinueA = "10101010-1010-4010-8010-101010101011"
const cliContinueB = "20202020-2020-4020-8020-202020202022"
const cliContinueFork = "30303030-3030-4030-8030-303030303033"

func startupFlagHome(t *testing.T, root, name string, seed bool) (string, map[string][]byte) {
	t.Helper()
	home := filepath.Join(root, name)
	for _, path := range []string{home, filepath.Join(home, "tmp"), filepath.Join(home, ".claude")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range map[string]string{"system.txt": "SYSTEM_FILE_PHYSICAL_612\n中文😀\ufeff", "append.txt": "APPEND_FILE_PHYSICAL_612\n", "empty.txt": "", ".claude/settings.json": `{"disableAllHooks":true}`} {
		if err := os.WriteFile(filepath.Join(home, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	originals := map[string][]byte{}
	if !seed {
		return home, originals
	}
	directory := filepath.Join(home, ".claude", "projects", projectpath.Sanitize(home))
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for index, row := range []struct{ sid, marker, extra string }{
		{cliContinueA, "PREVIOUS_ELIGIBLE_A", ""}, {cliContinueB, "PREVIOUS_ELIGIBLE_B", ""},
		{"40404040-4040-4040-8040-404040404044", "SIDECHAIN_MUST_NOT_CONTINUE", `,"isSidechain":true`},
		{"50505050-5050-4050-8050-505050505055", "TEAM_MUST_NOT_CONTINUE", `,"teamName":"private-team"`},
	} {
		data := []byte(fmt.Sprintf("{\"type\":\"user\",\"uuid\":\"old-user-%s\",\"parentUuid\":null,\"sessionId\":\"%s\",\"timestamp\":\"2026-10-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"%s\"}%s}\n{\"type\":\"assistant\",\"uuid\":\"old-answer-%s\",\"parentUuid\":\"old-user-%s\",\"sessionId\":\"%s\",\"timestamp\":\"2026-10-01T00:00:01Z\",\"message\":{\"id\":\"old-message-%s\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[{\"type\":\"text\",\"text\":\"old completed response\"}],\"stop_reason\":\"end_turn\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n{\"type\":\"custom-title\",\"customTitle\":\"old cached title\",\"sessionId\":\"%s\"}\n", row.sid, row.sid, row.marker, row.extra, row.sid, row.sid, row.sid, row.sid, row.sid))
		if index > 0 {
			time.Sleep(2 * time.Millisecond)
		}
		path := filepath.Join(directory, row.sid+".jsonl")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		modified := time.Unix(1700000000+int64(index), 750000)
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
		originals[path] = data
	}
	return home, originals
}

func TestActualPublicCLIContinuePromptFilesAndNameReachOriginalSourceConsumer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	binary := filepath.Join(root, "actual-go-cli")
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	var mu sync.Mutex
	var requests []json.RawMessage
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.Error(w, "unexpected", 404)
			return
		}
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, append(json.RawMessage(nil), data...))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		for _, item := range [][2]string{{"message_start", `{"type":"message_start","message":{"id":"normal-resume-response","type":"message","role":"assistant","model":"fixture-model","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`}, {"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`}, {"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"PUBLIC_RESUME_NORMAL_OK"}}`}, {"content_block_stop", `{"type":"content_block_stop","index":0}`}, {"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`}, {"message_stop", `{"type":"message_stop"}`}} {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", item[0], item[1])
		}
	}))
	defer peer.Close()
	for _, profile := range []struct {
		name       string
		seed, fork bool
		flags      []string
	}{
		{"continue-submillisecond", true, false, []string{"--continue"}},
		{"continue", true, false, []string{"--continue", "--name", "  NEW_CLI_TITLE_612 \ufeff"}},
		{"continue-short-fork", true, true, []string{"-c", "--fork-session", "--session-id", cliContinueFork, "--name", "NEW_CLI_TITLE_612"}},
		{"continue-empty", false, false, []string{"--continue", "--name", "NEW_CLI_TITLE_612"}},
		{"prompt-files", false, false, []string{"--system-prompt-file", "system.txt", "--append-system-prompt-file", "append.txt", "-n", "NEW_CLI_TITLE_612"}},
		{"prompt-file-empty", false, false, []string{"--system-prompt-file", "empty.txt", "--name", "NEW_CLI_TITLE_612"}},
	} {
		t.Run(profile.name, func(t *testing.T) {
			goHome, originals := startupFlagHome(t, root, "go-"+profile.name, profile.seed)
			sourceHome, sourceOriginals := startupFlagHome(t, root, "source-"+profile.name, profile.seed)
			if profile.name == "continue-submillisecond" {
				for _, home := range []string{goHome, sourceHome} {
					for i, sid := range []string{cliContinueA, cliContinueB} {
						stamp := time.Unix(1700000010, int64(900000-i*800000))
						path := filepath.Join(home, ".claude", "projects", projectpath.Sanitize(home), sid+".jsonl")
						if err := os.Chtimes(path, stamp, stamp); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			args := append([]string{"--bare", "--print", "--model", "fixture-model", "--thinking", "disabled", "--no-computer-use", "--tools", "", "--output-format", "json"}, profile.flags...)
			args = append(args, "Continue the current real task without replaying an old tool.")
			fixture, _ := filepath.Abs("../../testdata/native-cli-resume-errors.fixture.ts")
			input, _ := json.Marshal(map[string]any{"homeDir": sourceHome, "args": args, "success": true})
			source := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			source.Dir = sourceHome
			source.Env = append(resumeErrorEnv(sourceHome, root, "http://127.0.0.1:1"), "PATH=/usr/bin:/bin", "CC_HAHA_RESUME_ERROR_FIXTURE=1", "CC_HAHA_RESUME_ERROR_INPUT="+string(input))
			sourceOut, sourceErr := source.CombinedOutput()
			if sourceErr != nil {
				receipt, _ := os.ReadFile(filepath.Join(sourceHome, "source-resume-error-receipt.json"))
				t.Fatal("actual Source public startup", sourceErr, string(sourceOut), string(receipt))
			}
			var sourceReceipt struct {
				Requests     []json.RawMessage
				SessionID    string
				Capabilities struct{ Blocked []string }
			}
			saved, e := os.ReadFile(filepath.Join(sourceHome, "source-resume-error-receipt.json"))
			if e != nil {
				t.Fatal(e, string(sourceOut))
			}
			if e = json.Unmarshal(saved, &sourceReceipt); e != nil || len(sourceReceipt.Requests) != 1 || len(sourceReceipt.Capabilities.Blocked) != 0 {
				t.Fatal("actual Source receipt", e, string(saved))
			}
			mu.Lock()
			requests = nil
			mu.Unlock()
			command := exec.CommandContext(ctx, binary, args...)
			command.Dir = goHome
			command.Env = resumeErrorEnv(goHome, root, peer.URL)
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			if e = command.Run(); e != nil {
				t.Fatal("actual Go public startup", e, stderr.String(), stdout.String())
			}
			var result struct {
				SessionID string `json:"session_id"`
			}
			if e = json.Unmarshal(stdout.Bytes(), &result); e != nil {
				t.Fatal(e, stdout.String(), stderr.String())
			}
			mu.Lock()
			calls := append([]json.RawMessage(nil), requests...)
			mu.Unlock()
			if len(calls) != 1 {
				t.Fatal("extra Go model calls", len(calls))
			}
			for _, request := range []json.RawMessage{sourceReceipt.Requests[0], calls[0]} {
				text := string(request)
				if profile.seed && (!strings.Contains(text, "PREVIOUS_ELIGIBLE_B") || strings.Contains(text, "SIDECHAIN_MUST_NOT_CONTINUE") || strings.Contains(text, "TEAM_MUST_NOT_CONTINUE") || strings.Contains(text, "PREVIOUS_ELIGIBLE_A")) {
					t.Fatal("continue selected wrong physical Source log", text)
				}
				if profile.name == "prompt-file-empty" && !strings.Contains(text, "# currentDate") {
					t.Fatal("empty custom file must retain actual Source default user context", text)
				}
				if profile.name == "prompt-files" && (!strings.Contains(text, "SYSTEM_FILE_PHYSICAL_612") || !strings.Contains(text, "APPEND_FILE_PHYSICAL_612") || !strings.Contains(text, "中文😀")) {
					t.Fatal("file prompt lost at actual provider consumer", text)
				}
			}
			if profile.seed && !profile.fork && (result.SessionID != cliContinueB || sourceReceipt.SessionID != cliContinueB) {
				t.Fatal("continue did not retain Source SID", result, sourceReceipt.SessionID)
			}
			if profile.fork && (result.SessionID != cliContinueFork || sourceReceipt.SessionID != cliContinueFork) {
				t.Fatal("continue fork lost explicit SID", result, sourceReceipt.SessionID)
			}
			for _, side := range []struct {
				home, sid string
				originals map[string][]byte
			}{{goHome, result.SessionID, originals}, {sourceHome, sourceReceipt.SessionID, sourceOriginals}} {
				path := filepath.Join(side.home, ".claude", "projects", projectpath.Sanitize(side.home), side.sid+".jsonl")
				data, e := os.ReadFile(path)
				title := "NEW_CLI_TITLE_612"
				if profile.seed && !profile.fork {
					title = "old cached title"
				}
				if profile.seed && !profile.fork {
					if bytes.Count(data, []byte(`"uuid":"old-user-`+cliContinueB+`"`)) != 1 || bytes.Count(data, []byte(`"uuid":"old-answer-`+cliContinueB+`"`)) != 1 {
						t.Fatal("continued original UUID was duplicated", side.home, string(data))
					}
				}
				if e != nil || !bytes.Contains(data, []byte(`"customTitle":"`+title+`"`)) {
					t.Fatal("name absent from finalized actual transcript", e, string(data))
				}
				if profile.seed && !profile.fork && bytes.Contains(data, []byte(`"customTitle":"NEW_CLI_TITLE_612"`)) {
					t.Fatal("headless resume incorrectly used interactive title adoption", side.home)
				}
				for originalPath, before := range side.originals {
					current, _ := os.ReadFile(originalPath)
					if profile.fork || filepath.Base(originalPath) != cliContinueB+".jsonl" {
						if !bytes.Equal(before, current) {
							t.Fatal("continue changed protected parent/other transcript", originalPath)
						}
					}
				}
			}
		})
	}
}
