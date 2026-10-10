package connectors

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func seedState(t *testing.T, root, source string) {
	t.Helper()
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "state.json"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLegacySchemaUsesOriginalNumberAndTruthinessSemantics(t *testing.T) {
	for _, version := range []string{"0", "0.0", "-0e9", "1", "1.0", "1e0", "1.0000000000000000001"} {
		t.Run(version, func(t *testing.T) {
			root := t.TempDir()
			seedState(t, root, `{"schemaVersion":`+version+`,"custom":"keep","connectors":{"sample":{"installed":0.0,"enabled":true,"operation":0e0,"custom":{"keep":1}}}}`)
			p, err := readPersistence(root, nil)
			if err != nil {
				t.Fatal(err)
			}
			state := p.get("sample")
			if state.String("status") != "not-installed" || state.String("connection") != "disconnected" || state.Get("error") != nil {
				t.Fatalf("zero is falsy in original state: %s", raw(state))
			}
			if err := p.patch("sample", record(field("enabled", false))); err != nil {
				t.Fatal(err)
			}
			bytes, err := os.ReadFile(p.path)
			if err != nil || !bytesContainsUnknownFields(bytes) {
				t.Fatalf("legacy fields not retained: %s %v", bytes, err)
			}
		})
	}
}

func bytesContainsUnknownFields(source []byte) bool {
	var v protocol.JSONObject
	if v.UnmarshalJSON(source) != nil {
		return false
	}
	item := nested(nested(v.Get("connectors")).Get("sample"))
	return v.String("custom") == "keep" && string(nested(item.Get("custom")).Get("keep")) == "1"
}

func TestInvalidStateNeverChangesOriginalFile(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`{`, "Cannot read connector state; existing state was preserved"},
		{`null`, "Invalid connector state"},
		{`[]`, "Invalid connector state"},
		{`{"schemaVersion":999,"connectors":{}}`, "Unsupported connector state version; update the app before modifying connectors"},
		{`{"schemaVersion":"1","connectors":{}}`, "Unsupported connector state version; update the app before modifying connectors"},
		{`{"schemaVersion":null,"connectors":{}}`, "Unsupported connector state version; update the app before modifying connectors"},
		{`{"connectors":[]}`, "Invalid connector records"},
		{`{"connectors":{"sample":null}}`, "Invalid connector record"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			root := t.TempDir()
			seedState(t, root, tc.input)
			_, err := readPersistence(root, nil)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v; want %s", err, tc.want)
			}
			got, err := os.ReadFile(filepath.Join(root, "state.json"))
			if err != nil || string(got) != tc.input {
				t.Fatalf("invalid original was modified: %s %v", got, err)
			}
		})
	}
}

func TestInterruptedReadinessAndRawUnknownFields(t *testing.T) {
	root := t.TempDir()
	seedState(t, root, `{"custom":{"units":"\ud800","n":9007199254740993,"html":"<keep>","zero":-0},"connectors":{"sample":{"installed":true,"enabled":true,"status":"ready","runtime":"ready","verification":"remote","operation":{"id":"old"},"opaque":{"units":"\udc00"}}}}`)
	p, err := readPersistence(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	state := p.get("sample")
	if state.Get("operation") != nil || state.Get("verification") != nil || state.String("runtime") != "missing" || state.String("status") != "needs-auth" || state.String("error") != "Previous operation was interrupted. Check the connection again." {
		t.Fatalf("interrupted connector cannot advertise ready: %s", raw(state))
	}
	if err := p.patch("sample", record(field("status", "disabled"))); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p.path)
	if err != nil {
		t.Fatal(err)
	}
	var value protocol.JSONObject
	if err := value.UnmarshalJSON(got); err != nil {
		t.Fatal(err)
	}
	want := json.RawMessage(`{"units":"\ud800","n":9007199254740992,"html":"<keep>","zero":0}`)
	canonical, _ := protocol.ParseAndStringifyJavaScriptJSON(value.Get("custom"))
	if !bytes.Equal(canonical, want) {
		t.Fatalf("unknown fields changed differently from Source JSON.parse/stringify: %s", canonical)
	}
	if bytes.Contains(got, []byte("9007199254740993")) || bytes.Contains(got, []byte(`\u003c`)) {
		t.Fatalf("persisted representation did not use original JSON number/string rules: %s", got)
	}
}

func TestTruthyMatchesOriginalPersistedValueDomain(t *testing.T) {
	for _, v := range []string{"null", "false", "0", "-0", "0.0", "0e99", "1e-999", `""`} {
		if truthy(json.RawMessage(v)) {
			t.Errorf("%s must be falsy", v)
		}
	}
	for _, v := range []string{"true", "1", "-1", "1e400", `"0"`, `"false"`, "[]", "{}"} {
		if !truthy(json.RawMessage(v)) {
			t.Errorf("%s must be truthy", v)
		}
	}
}

func TestInstalledDefinitionPreservesNullishInsteadOfEmptyFallback(t *testing.T) {
	def := record(field("id", "sample"), field("version", "2.0.0"))
	state := record(field("installedVersion", ""), field("installation", record(field("directory", "/managed/1.0.0-darwin-arm64"))))
	if _, err := installedDefinition(def, state); err == nil {
		t.Fatal("Source rejects an explicitly empty version instead of falling back")
	}
	state.Set("installedVersion", raw(nil))
	old, err := installedDefinition(def, state)
	if err != nil || old.String("version") != "1.0.0" {
		t.Fatalf("legacy nullish fallback: %s %v", raw(old), err)
	}
}
