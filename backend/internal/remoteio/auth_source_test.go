package remoteio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestActualSourceIngressAuthFDCacheFallbackPersistenceAndRefresh(t *testing.T) {
	for _, row := range []struct {
		name, fdBytes, fdEnv, fallback string
		hasFallback, closed            bool
		env                            map[string]string
		updates                        []map[string]string
	}{
		{name: "fd-trim-prefix-and-cache", fdBytes: "\ufeff fake-fd-token \u00a0", fdEnv: " +{fd}suffix", env: map[string]string{"CLAUDE_CODE_REMOTE": "true"}, updates: []map[string]string{{}, {"CLAUDE_CODE_WEBSOCKET_AUTH_FILE_DESCRIPTOR": "invalid"}, {"CLAUDE_CODE_SESSION_ACCESS_TOKEN": "refreshed-env"}, {"CLAUDE_CODE_SESSION_ACCESS_TOKEN": ""}}},
		{name: "env-dominates-then-fd", fdBytes: "fake-hidden-fd", fdEnv: "{fd}", env: map[string]string{"CLAUDE_CODE_SESSION_ACCESS_TOKEN": "spawn-token"}, updates: []map[string]string{{}, {"CLAUDE_CODE_SESSION_ACCESS_TOKEN": ""}}},
		{name: "empty-fd-does-not-fallback", fdBytes: " \t", fdEnv: "{fd}", fallback: "fake-file", hasFallback: true},
		{name: "invalid-fd-does-not-fallback", fdEnv: "not-a-descriptor", fallback: "fake-file", hasFallback: true},
		{name: "closed-fd-falls-back", fdEnv: "{fd}", closed: true, fallback: " fake-file \n", hasFallback: true},
		{name: "missing-fd-reads-file", fallback: " fake-file \n", hasFallback: true},
		{name: "empty-file-override", fallback: "should-not-use-default", hasFallback: true, env: map[string]string{"CLAUDE_SESSION_INGRESS_TOKEN_FILE": ""}},
		{name: "session-cookie-org", env: map[string]string{"CLAUDE_CODE_SESSION_ACCESS_TOKEN": "sk-ant-sid-private-fixture", "CLAUDE_CODE_ORGANIZATION_UUID": "fixture-org"}},
		{name: "empty-auth"},
	} {
		t.Run(row.name, func(t *testing.T) {
			input := map[string]any{"fdBytes": row.fdBytes, "env": row.env, "updates": row.updates, "fdClosed": row.closed}
			if row.fdEnv != "" {
				input["fdEnv"] = row.fdEnv
			}
			if row.hasFallback {
				input["fallback"] = row.fallback
			}
			if row.updates == nil {
				input["updates"] = []map[string]string{{}}
			}
			want := runSourceFixture(t, "native-remoteio-auth.fixture.ts", input)
			directory := filepath.Join(t.TempDir(), "remote")
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(directory, ".session_ingress_token")
			if row.hasFallback {
				if err := os.WriteFile(file, []byte(row.fallback), 0600); err != nil {
					t.Fatal(err)
				}
			}
			descriptorFile := filepath.Join(filepath.Dir(directory), "fd-token")
			if err := os.WriteFile(descriptorFile, []byte(row.fdBytes), 0600); err != nil {
				t.Fatal(err)
			}
			descriptor, err := os.Open(descriptorFile)
			if err != nil {
				t.Fatal(err)
			}
			defer descriptor.Close()
			fd := descriptor.Fd()
			if row.closed {
				descriptor.Close()
			}
			env := map[string]string{}
			for key, value := range row.env {
				env[key] = value
			}
			if row.fdEnv != "" {
				env["CLAUDE_CODE_WEBSOCKET_AUTH_FILE_DESCRIPTOR"] = strings.ReplaceAll(row.fdEnv, "{fd}", strconv.FormatUint(uint64(fd), 10))
			}
			auth := NewIngressAuthWithDirectory(env, directory)
			service := &Service{options: Options{IngressAuth: auth}, env: env}
			updates := row.updates
			if updates == nil {
				updates = []map[string]string{{}}
			}
			results := []map[string]any{}
			for _, update := range updates {
				service.UpdateEnvironment(update)
				token := auth.Token()
				var value any = token
				if token == "" {
					value = nil
				}
				headers := map[string]string{}
				actualHeaders := service.headers(true)
				for _, key := range []string{"Authorization", "Cookie", "X-Organization-Uuid"} {
					if value := actualHeaders.Get(key); value != "" {
						headers[key] = value
					}
				}
				results = append(results, map[string]any{"token": value, "headers": headers})
			}
			var a, b any
			json.Unmarshal(want.Get("results"), &a)
			json.Unmarshal(raw(results), &b)
			if !reflect.DeepEqual(a, b) {
				t.Errorf("Source auth%s Go%s", want.Get("results"), raw(results))
			}
			data, err := os.ReadFile(file)
			var persisted any
			var mode any
			if err == nil {
				persisted = string(data)
				stat, _ := os.Stat(file)
				mode = uint32(stat.Mode().Perm())
			}
			for name, value := range map[string]any{"persisted": persisted, "fileMode": mode} {
				json.Unmarshal(want.Get(name), &a)
				json.Unmarshal(raw(value), &b)
				if !reflect.DeepEqual(a, b) {
					t.Errorf("Source %s%s Go%s", name, want.Get(name), raw(value))
				}
			}
		})
	}
}
