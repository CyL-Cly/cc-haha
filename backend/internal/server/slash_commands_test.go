package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/slashcommandsapi"
)

func TestSlashCommandsColdHTTPNoFactoryAndRealSDKCacheReloadLifecycle(t *testing.T) {
	encode := func(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
	var factories atomic.Int32
	var includeHistory []bool
	o := testOptions(t, func(ctx context.Context, options SessionOptions, emit Emit) (LiveSession, error) {
		factories.Add(1)
		options.OnSDK(json.RawMessage(`{"type":"system","subtype":"init","slash_commands":["real-cli",{"name":"skill","description":"live preferred"},{"name":"real-cli","argumentHint":"<real>"}]}`))
		return &transportSession{emit: emit, received: make(chan json.RawMessage, 4)}, nil
	})
	o.SkillSlashCommands = func(ctx context.Context, cwd string, include bool) ([]slashcommandsapi.Command, error) {
		includeHistory = append(includeHistory, include)
		if cwd != o.WorkDir {
			t.Error("wrong original workdir", cwd)
		}
		out := []slashcommandsapi.Command{{Name: "skill", Description: json.RawMessage(`"disk"`), Kind: "skill", Source: "project"}, {Name: "extra-disk", Description: json.RawMessage(`"extra"`), Kind: "command"}}
		if include {
			out = append(out, slashcommandsapi.Command{Name: "cold-only", Description: json.RawMessage(`"compiled"`)})
		}
		return out, nil
	}
	s, host := testHost(t, o)
	created := requestJSON(t, host, http.MethodPost, "/api/sessions", `{"workDir":`+string(encode(o.WorkDir))+`}`, 201)
	var value struct{ SessionID string }
	if e := json.Unmarshal(created, &value); e != nil {
		t.Fatal(e)
	}
	path := "/api/sessions/" + value.SessionID + "/slash-commands"
	var response struct{ Commands []slashcommandsapi.Command }
	if e := json.Unmarshal(requestJSON(t, host, http.MethodGet, path, "", 200), &response); e != nil {
		t.Fatal(e)
	}
	if factories.Load() != 0 || len(response.Commands) != 3 || !includeHistory[0] {
		t.Fatal("cold read started harness or missed compiled", factories.Load(), response)
	}
	live, e := s.live(value.SessionID)
	if e != nil {
		t.Fatal(e)
	}
	if live == nil {
		t.Fatal("real cache emitter missing")
	}
	if e = json.Unmarshal(requestJSON(t, host, http.MethodGet, path, "", 200), &response); e != nil {
		t.Fatal(e)
	}
	if len(response.Commands) != 3 || response.Commands[0].Name != "real-cli" || string(response.Commands[0].ArgumentHint) != `"<real>"` || response.Commands[1].Kind != "skill" || string(response.Commands[1].Description) != `"live preferred"` || includeHistory[1] {
		t.Fatal("real cache was replaced by guessed compiled data", response)
	}
	snapshot := s.SessionSlashCommands(value.SessionID)
	snapshot[0].Set("name", encode("foreign mutation"))
	if s.SessionSlashCommands(value.SessionID)[0].String("name") != "real-cli" {
		t.Fatal("caller mutated source cache")
	}
	s.broadcast(value.SessionID, json.RawMessage(`{"type":"system_notification","subtype":"slash_commands","data":[{"name":"after-reload","description":"actual changed list"}]}`))
	if e = json.Unmarshal(requestJSON(t, host, http.MethodGet, path, "", 200), &response); e != nil {
		t.Fatal(e)
	}
	if response.Commands[0].Name != "after-reload" || includeHistory[2] {
		t.Fatal("reload notification did not update actual cache", response)
	}
	s.resetSlashCommands(value.SessionID)
	requestJSON(t, host, http.MethodGet, path, "", 200)
	if !includeHistory[3] {
		t.Fatal("empty real list suppressed Source cold fallback")
	}
	requestJSON(t, host, http.MethodPost, "/api/sessions/missing/slash-commands", "", 405)
	requestJSON(t, host, http.MethodGet, "/api/sessions/missing/slash-commands", "", 404)
	s.mu.Lock()
	runtime := s.runtimes[value.SessionID]
	s.mu.Unlock()
	if e = closeRuntime(context.Background(), runtime); e != nil {
		t.Fatal(e)
	}
	s.setSessionSlashCommands(value.SessionID, json.RawMessage(`["late-closed"]`))
	if len(s.SessionSlashCommands(value.SessionID)) != 0 {
		t.Fatal("closed runtime retained command metadata")
	}
	if factories.Load() != 1 {
		t.Fatal("HTTP lookup manufactured another CLI", factories.Load())
	}
}

func TestSlashCacheDoesNotAcceptOtherSDKSubtypesAndEvictedRuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := &runtimeSession{ctx: ctx, live: &transportSession{}}
	s := &Server{runtimes: map[string]*runtimeSession{"actual": runtime}}
	s.captureSlashCommandsSDK("actual", json.RawMessage(`{"type":"assistant","subtype":"init","slash_commands":["false"]}`))
	if len(s.SessionSlashCommands("actual")) != 0 {
		t.Fatal("non-original init advertised metadata")
	}
	s.captureSlashCommandsSDK("actual", json.RawMessage(`{"type":"system","subtype":"init","slash_commands":["actual"]}`))
	if len(s.SessionSlashCommands("actual")) != 1 {
		t.Fatal("actual SDK init not captured")
	}
	s.captureSlashCommandsSDK("actual", json.RawMessage(`{"type":"system","subtype":"init","slash_commands":{}}`))
	if len(s.SessionSlashCommands("actual")) != 1 {
		t.Fatal("non-array SDK init replaced original cache")
	}
	runtime.mu.Lock()
	runtime.live = nil
	runtime.mu.Unlock()
	if len(s.SessionSlashCommands("actual")) != 0 {
		t.Fatal("evicted runtime advertised stale CLI list")
	}
}
