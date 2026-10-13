package application

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/access"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func migrationTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		if entry.IsDir() {
			out[relative+"/"] = "directory"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[relative] = fmt.Sprintf("%x", sha256.Sum256(data))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMigrationValidationFactoryDoesNotCreateBusinessStateAndActivatesRealGoSettings(t *testing.T) {
	var providers, natives atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providers.Add(1)
		http.Error(w, "migration must not invoke a model", 500)
	}))
	defer provider.Close()
	native := nativeSessionOptions(t, provider.URL)
	home, config := native.Environment["HOME"], native.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(filepath.Join(config, "projects", "saved"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"unknownOwner":"keep","model":"fixture-model"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".config.json"), []byte(`{"unknownGlobal":"keep"}`), 0600); err != nil {
		t.Fatal(err)
	}
	id := "22222222-2222-4222-8222-222222222222"
	transcript := `{"type":"user","uuid":"saved","sessionId":"` + id + `","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"Saved conversation"}}` + "\n"
	if err := os.WriteFile(filepath.Join(config, "projects", "saved", id+".jsonl"), []byte(transcript), 0600); err != nil {
		t.Fatal(err)
	}
	native.Environment["CC_HAHA_MIGRATION_VALIDATION"] = "1"
	native.Environment[access.LocalAccessTokenEnv] = "factory-migration-token"
	before := migrationTree(t, home)
	s, err := NewDesktopServer(ServerOptions{
		Environment: native.Environment, HomeDir: home, ConfigDir: config,
		WorkDir: native.WorkDir, Version: native.Version, Arguments: native.Arguments, Stderr: io.Discard,
		RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission },
		NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
			natives.Add(1)
			if _, present := env["CC_HAHA_MIGRATION_VALIDATION"]; present {
				t.Error("validation flag leaked into activated business runtime")
			}
			return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: native.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	host := httptest.NewServer(s)
	defer host.Close()
	request := func(method, path, body string, status int) []byte {
		t.Helper()
		r, err := http.NewRequest(method, host.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer factory-migration-token")
		r.Header.Set("Content-Type", "application/json")
		res, err := host.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != status {
			t.Fatalf("%s %s: %d %s %v", method, path, res.StatusCode, data, err)
		}
		return data
	}
	request("GET", "/health", "", 200)
	request("GET", "/api/settings", "", 503)
	request("GET", "/api/sessions", "", 503)
	request("GET", "/api/runtime/migration/validate", "", 200)
	if natives.Load() != 0 || providers.Load() != 0 {
		t.Fatal("validation booted business or model", natives.Load(), providers.Load())
	}
	if after := migrationTree(t, home); !reflect.DeepEqual(before, after) {
		t.Fatal("validation mutated old fixture", before, after)
	}
	request("POST", "/api/runtime/migration/activate", "", 200)
	if natives.Load() == 0 {
		t.Fatal("activation acknowledged without actual factory")
	}
	settings := request("GET", "/api/settings", "", 200)
	if !strings.Contains(string(settings), "unknownOwner") {
		t.Fatal("activated settings lost old fixture", string(settings))
	}
	request("POST", "/api/runtime/migration/quiesce", "", 200)
	request("GET", "/api/settings", "", 503)
	if providers.Load() != 0 {
		t.Fatal("migration called actual provider")
	}
	if native.Environment["CC_HAHA_MIGRATION_VALIDATION"] != "1" {
		t.Fatal("activation mutated caller environment")
	}
}
