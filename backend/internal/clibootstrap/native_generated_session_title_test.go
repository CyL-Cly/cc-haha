package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestNativeGeneratedSessionTitleUsesActualCanonicalSchemaSystemPromptAndKeepsParent(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	var requests atomic.Int32
	var actual protocol.JSONObject
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = actual.UnmarshalJSON(data)
		requests.Add(1)
		emitPlanFixture(w, 1, "", `{"title":"  Fix \t owned\n session  "}`)
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-generated-title-fixture"
	f.n.Environment["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = "fixture-small-model"
	f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{Model: "fixture-model", ComputerUse: &disabled, SystemPrompt: "parent prompt excluded from title", Tools: []string{""}, MaxTurns: 2, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}})
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
	title, err := m.GenerateCLISessionTitle(ctx, []byte(`"<command-name>/remote</command-name><command-args>  owned \ud800 first human </command-args>"`))
	if err != nil || string(title) != `"Fix owned session"` || requests.Load() != 1 {
		t.Fatalf("title=%s requests=%d error=%v", title, requests.Load(), err)
	}
	config, _ := actual.Object("output_config")
	format, _ := config.Object("format")
	schema, _ := format.Object("schema")
	properties, _ := schema.Object("properties")
	if actual.String("model") != "fixture-small-model" || format.String("type") != "json_schema" || len(properties.OwnProperties()) != 1 || string(schema.Get("required")) != `["title"]` || !strings.Contains(string(actual.Get("messages")), `/remote owned \ud800 first human`) || strings.Contains(string(actual.Get("system")), "parent prompt excluded") || !strings.Contains(string(actual.Get("system")), "Generate a concise, sentence-case title (3-7 words)") {
		t.Fatalf("wrong original title consumer %s", rawMain(actual))
	}
	var after []byte
	if err := m.Owner.Run(ctx, func(*cooperative.Scope) error { after, _ = json.Marshal(m.Controller.History()); return nil }); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("isolated title entered parent history %s", after)
	}
	if empty, err := m.GenerateCLISessionTitle(ctx, []byte(`"<system-reminder>private content</system-reminder>"`)); err != nil || empty != nil || requests.Load() != 1 {
		t.Fatal("empty cleaned title opened model", string(empty), err, requests.Load())
	}
}
