package imruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/adapterconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestIMPairingRateBoundaryAndSharedLegacySessionStoreActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	now := int64(1770000000000)
	seed := func() any {
		return map[string]any{"future": map[string]any{"keep": true}, "telegram": map[string]any{"botToken": "fake-not-sent", "allowedUsers": []any{7, "8"}, "pairedUsers": []any{map[string]any{"userId": "9", "displayName": "old", "pairedAt": 0}}}, "pairing": map[string]any{"code": "ABC234", "expiresAt": now + 600000, "createdAt": now, "future": "should-clear-on-pair"}}
	}
	actions := []map[string]any{{"seed": seed(), "check": 7}, {"check": 8}, {"check": 9}, {"check": 10}, {"text": "wrong", "user": 10}, {"text": "wrong", "user": 10}, {"text": "wrong", "user": 10}, {"text": "wrong", "user": 10}, {"text": "wrong", "user": 10}, {"text": "abc234", "user": 10}, {"now": now + 300000, "text": "ABC234", "user": 10}, {"now": now + 300001, "text": " abc234 ", "user": 10, "name": "Fresh"}, {"check": 10}, {"text": "ABC234", "user": 11}, {"seed": seed(), "now": now + 600000, "text": "ABC234", "user": 12}, {"seed": seed(), "now": now + 600001, "text": "ABC234", "user": 13}}
	sessions := []map[string]any{{"seed": map[string]any{"other": map[string]any{"sessionId": "keep", "workDir": "/old", "updatedAt": 1, "future": true}}, "chat": "42"}, {"chat": "42", "set": true, "id": "new", "work": "/private/work"}, {"chat": "43", "set": true, "id": "new", "work": "/private/other"}, {"chat": "42", "deleteSession": "new"}, {"chat": "other", "remove": true}}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-im-common-state.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_IM_COMMON_STATE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"now": now, "actions": actions, "sessions": sessions}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var expected struct{ Pairing, Bindings []json.RawMessage }
	if err = json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err, string(output))
	}
	private := t.TempDir()
	service, err := adapterconfig.NewService(adapterconfig.ServiceOptions{ConfigDir: private, Store: storage.New(storage.Options{}), LogError: func(error) {}})
	if err != nil {
		t.Fatal(err)
	}
	pair := NewPairing(private, service)
	pair.NowMillis = func() int64 { return now }
	actualPair := []any{}
	for _, action := range actions {
		if value, ok := action["seed"]; ok {
			if err = os.WriteFile(filepath.Join(private, "adapters.json"), raw(value), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if value, ok := action["now"]; ok {
			now = value.(int64)
		}
		if user, ok := action["check"]; ok {
			actualPair = append(actualPair, map[string]any{"allowed": pair.Allowed("telegram", raw(user))})
		}
		if text, ok := action["text"]; ok {
			name := "Fixture"
			if value, ok := action["name"]; ok {
				name = value.(string)
			}
			paired, err := pair.TryPair("telegram", text.(string), name, raw(action["user"]))
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(private, "adapters.json"))
			if err != nil {
				t.Fatal(err)
			}
			actualPair = append(actualPair, map[string]any{"paired": paired, "config": json.RawMessage(data)})
		}
	}
	store := NewSessionStore(private)
	store.NowMillis = func() int64 { return now }
	actualBindings := []any{}
	for _, action := range sessions {
		chat := action["chat"].(string)
		if seed, ok := action["seed"]; ok {
			if err = os.WriteFile(store.Path, raw(seed), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if action["set"] == true {
			if err = store.Set(chat, action["id"].(string), action["work"].(string)); err != nil {
				t.Fatal(err)
			}
		}
		if action["remove"] == true {
			if err = store.Delete(chat); err != nil {
				t.Fatal(err)
			}
		}
		result := map[string]any{}
		if id, ok := action["deleteSession"]; ok {
			removed, e := store.DeleteBySessionID(id.(string))
			if e != nil {
				t.Fatal(e)
			}
			result["removed"] = removed
		}
		result["entry"] = store.Get(chat)
		all := []protocol.JSONObject{}
		for _, field := range store.List().OwnProperties() {
			entry := protocol.NewJSONObject(protocol.JSONField{Name: "chatId", Value: field.NameJSON})
			entry.Assign(object(field.Value))
			all = append(all, entry)
		}
		result["all"] = all
		data, err := os.ReadFile(store.Path)
		if err != nil {
			t.Fatal(err)
		}
		result["stored"] = json.RawMessage(data)
		actualBindings = append(actualBindings, result)
	}
	for name, pair := range map[string][2]any{"pairing": {actualPair, expected.Pairing}, "bindings": {actualBindings, expected.Bindings}} {
		var got, want any
		_ = json.Unmarshal(raw(pair[0]), &got)
		_ = json.Unmarshal(raw(pair[1]), &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s actual%s want%s", name, raw(got), raw(want))
		}
	}
}
