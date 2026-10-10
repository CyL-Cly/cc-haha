package imruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"testing"
)

func TestWhatsAppActualSourceOldAuthBufferSchemaKeysBackupAndSave(t *testing.T) {
	seed := `{"noiseKey":{"private":{"type":"Buffer","data":"AQID"},"public":{"type":"Buffer","data":[4,5,6]}},"registered":true,"future":{"preserve":true},"me":{"id":"1:2@s.whatsapp.net"}}`
	for _, mode := range []string{"valid", "invalid-main-backup", "missing-main-backup"} {
		t.Run(mode, func(t *testing.T) {
			files := map[string]string{"creds.json": seed}
			if mode != "valid" {
				files["creds.json.bak"] = seed
				if mode == "invalid-main-backup" {
					files["creds.json"] = "{broken"
				} else {
					delete(files, "creds.json")
				}
			}
			actions := []map[string]any{{"op": "get", "category": "session", "ids": []string{"missing"}}, {"op": "set", "value": map[string]any{"session": map[string]any{"user/1:2": map[string]any{"future": true, "key": map[string]any{"type": "Buffer", "data": []int{1, 258, -1}}}, "delete": map[string]any{"a": 1}}, "pre-key": map[string]any{"1": map[string]any{"private": map[string]int{"0": 4, "1": 5}, "public": map[string]any{"type": "Buffer", "data": "Bwg="}}}}}, {"op": "get", "category": "session", "ids": []string{"user/1:2", "missing", "delete"}}, {"op": "set", "value": map[string]any{"session": map[string]any{"delete": nil}}}, {"op": "get", "category": "pre-key", "ids": []string{"1"}}, {"op": "set", "value": map[string]any{"app-state-sync-key": map[string]any{"id": map[string]any{"keyData": map[string]any{"type": "Buffer", "data": "AQID"}, "fingerprint": map[string]any{"rawId": -1, "currentIndex": "5", "deviceIndexes": []any{1, -2, "3"}, "future": true}, "timestamp": "9223372036854775807", "future": true}}}}, {"op": "get", "category": "app-state-sync-key", "ids": []string{"id", "missing"}}, {"op": "update", "value": map[string]any{"future2": map[string]any{"key": map[string]any{"0": 7, "1": 256}}, "counter": 1}}, {"op": "save"}, {"op": "update", "value": map[string]any{"counter": 2}}, {"op": "save"}, {"op": "buffer", "value": map[string]any{"0": 256, "1": -1, "2": 1.9}}}
			want := object(whatsAppOriginal(t, "native-whatsapp-auth.fixture.ts", map[string]any{"files": files, "actions": actions}))
			dir := filepath.Join(t.TempDir(), "auth")
			if e := os.MkdirAll(dir, 0700); e != nil {
				t.Fatal(e)
			}
			for name, data := range files {
				if e := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); e != nil {
					t.Fatal(e)
				}
			}
			store, e := OpenWhatsAppAuthStore(dir, WhatsAppAuthOptions{})
			if e != nil {
				t.Fatal(e)
			}
			defer store.Close()
			values := []protocol.JSONObject{}
			for _, a := range actions {
				switch a["op"] {
				case "get":
					v, e := store.GetKeys(a["category"].(string), a["ids"].([]string))
					out := protocol.NewJSONObject(protocol.JSONField{Name: "value", Value: raw(v)})
					if e != nil {
						out = protocol.NewJSONObject(protocol.JSONField{Name: "error", Value: raw(e.Error())})
					}
					values = append(values, out)
				case "set":
					if e := store.SetKeys(object(raw(a["value"]))); e != nil {
						t.Fatal(e)
					}
				case "update":
					if e := store.UpdateCredentials(object(raw(a["value"]))); e != nil {
						t.Fatal(e)
					}
				case "save":
					if _, e := store.QueueSaveCreds(); e != nil {
						t.Fatal(e)
					}
					if e := store.Wait(context.Background()); e != nil {
						t.Fatal(e)
					}
				case "buffer":
					v, e := WhatsAppBufferJSON(raw(a["value"]))
					if e != nil {
						t.Fatal(e)
					}
					values = append(values, protocol.NewJSONObject(protocol.JSONField{Name: "value", Value: v}))
				}
			}
			gotFiles := protocol.NewJSONObject()
			entries, e := os.ReadDir(dir)
			if e != nil {
				t.Fatal(e)
			}
			for _, entry := range entries {
				p := filepath.Join(dir, entry.Name())
				data, e := os.ReadFile(p)
				if e != nil {
					t.Fatal(e)
				}
				stat, e := os.Stat(p)
				if e != nil {
					t.Fatal(e)
				}
				gotFiles.Set(entry.Name(), raw(map[string]any{"data": string(data), "mode": int(stat.Mode().Perm())}))
			}
			creds, e := WhatsAppBufferJSON(raw(store.Credentials()))
			if e != nil {
				t.Fatal(e)
			}
			got := protocol.NewJSONObject(protocol.JSONField{Name: "creds", Value: creds}, protocol.JSONField{Name: "values", Value: raw(values)}, protocol.JSONField{Name: "files", Value: raw(gotFiles)})
			// Compare structural order-independent objects except physical auth file data,
			// where exact serialized bytes and unknown fields must remain unchanged.
			var goValue, wantValue any
			_ = json.Unmarshal(raw(got), &goValue)
			_ = json.Unmarshal(raw(want), &wantValue)
			if !bytes.Equal(raw(goValue), raw(wantValue)) {
				t.Fatalf("Go %s\nSource %s", raw(got), raw(want))
			}
		})
	}
}
