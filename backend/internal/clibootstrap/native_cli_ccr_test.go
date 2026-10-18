package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestCLIRemoteTitleUsesCanonicalAuxiliaryModelSchemaAndLeavesParentHistory(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	var requests atomic.Int32
	var actual protocol.JSONObject
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = actual.UnmarshalJSON(data)
		requests.Add(1)
		emitPlanFixture(w, 1, "", `{"title":"Owned title result","branch":"claude/owned-title"}`)
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-title-fixture"
	f.n.Environment["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = "fixture-small-model"
	f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{Model: "fixture-model", ComputerUse: &disabled, SystemPrompt: "parent must not enter title request", Tools: []string{""}, MaxTurns: 2, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	var before []byte
	if err := m.Owner.Run(ctx, func(*cooperative.Scope) error { before, _ = json.Marshal(m.Controller.History()); return nil }); err != nil {
		t.Fatal(err)
	}
	title, err := m.GenerateCLIRemoteTitle(ctx, "actual remote requested task")
	if err != nil || title.Title != "Owned title result" || title.Branch != "claude/owned-title" || requests.Load() != 1 {
		t.Fatal(title, err, requests.Load())
	}
	format, _ := actual.Object("output_config")
	schema, _ := format.Object("format")
	if actual.String("model") != "fixture-small-model" || schema.String("type") != "json_schema" || !strings.Contains(string(actual.Get("messages")), "actual remote requested task") || strings.Contains(string(actual.Get("system")), "parent must not enter title request") {
		t.Fatal("Source queryHaiku model/schema/prompt differs", actual)
	}
	var after []byte
	if err := m.Owner.Run(ctx, func(*cooperative.Scope) error { after, _ = json.Marshal(m.Controller.History()); return nil }); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("title request polluted canonical user transcript", string(after))
	}
}

func TestCLIKnownRepositoryPickerDropsOnlyStaleSelectedPathFromOldGlobal(t *testing.T) {
	m := openAIAuthMainFixture(t)
	first, second := filepath.Join(m.options.Native.Home, "owned-checkout-one"), filepath.Join(m.options.Native.Home, "owned-checkout-two")
	for _, path := range []string{first, second} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if err := m.Owner.Run(ctx, func(*cooperative.Scope) error {
		return m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			next := current.Clone()
			next.Set("futureRepoFixture", []byte(`{"keep":true}`))
			next.Set("githubRepoPaths", rawMain(map[string][]string{"owner/repo": {first, second}, "other/repo": {second}}))
			return &next, nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	paths, err := m.KnownCLIRepositoryPaths(ctx, "OWNER/REPO")
	if err != nil || len(paths) != 2 {
		t.Fatal(paths, err)
	}
	if err := m.ForgetKnownCLIRepositoryPath(ctx, "OWNER/REPO", first); err != nil {
		t.Fatal(err)
	}
	if err := m.ForgetKnownCLIRepositoryPath(ctx, "OWNER/REPO", first); err != nil {
		t.Fatal(err)
	}
	paths, err = m.KnownCLIRepositoryPaths(ctx, "owner/repo")
	if err != nil || len(paths) != 1 || paths[0] != second {
		t.Fatal(paths, err)
	}
	if err := m.ForgetKnownCLIRepositoryPath(ctx, "owner/repo", second); err != nil {
		t.Fatal(err)
	}
	if err := m.Owner.Run(ctx, func(*cooperative.Scope) error {
		global, err := m.Global.Get()
		if err != nil {
			return err
		}
		mapping, _ := global.Object("githubRepoPaths")
		future, _ := global.Object("futureRepoFixture")
		if mapping.Get("owner/repo") != nil || mapping.Get("other/repo") == nil || string(future.Get("keep")) != "true" {
			return fmt.Errorf("stale repo cleanup mutated unrelated old data: %s", rawMain(global))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
