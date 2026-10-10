package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/access"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestGrokProviderAuthStatusActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source comparison requires pinned Bun")
	}
	o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Fatal("auth status created a model session")
		return nil, nil
	})
	now := int64(1770000000000)
	o.Store = storage.New(storage.Options{})
	o.Environment = func() map[string]string { return map[string]string{"ANTHROPIC_API_KEY": "fake-unrelated-process-key"} }
	parent, err := desktopoauth.New(desktopoauth.Options{Home: o.HomeDir, ConfigDir: o.ConfigDir, Platform: "linux", Store: o.Store, Environment: o.Environment, Now: func() time.Time { return time.UnixMilli(now) }})
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	o.HahaGrokOAuth = parent.Grok()
	o.Authorization = access.Authorization{LocalToken: "fixture-local"}
	rows := []map[string]any{{"absent": true}}
	for _, tokens := range []any{
		nil, false, 0, []any{}, map[string]any{},
		map[string]any{"accessToken": "fake-access", "refreshToken": "fake-refresh", "expiresAt": now + 3600000},
		map[string]any{"accessToken": "fake-access", "refreshToken": "fake-refresh", "expiresAt": nil},
		map[string]any{"accessToken": "fake-access", "expiresAt": nil},
		map[string]any{"refreshToken": "fake-refresh", "expiresAt": nil},
		map[string]any{"accessToken": "fake-access", "refreshToken": "", "expiresAt": nil},
		map[string]any{"accessToken": false, "refreshToken": "fake-refresh", "expiresAt": nil},
		map[string]any{"accessToken": "fake-access", "refreshToken": 0, "expiresAt": nil},
		map[string]any{"accessToken": 12, "refreshToken": []any{}, "expiresAt": nil},
		map[string]any{"accessToken": "fake-access", "expiresAt": now - 3600000},
	} {
		rows = append(rows, map[string]any{"tokens": tokens})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-grok-provider-auth.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = o.HomeDir
	cmd.Env = []string{"HOME=" + o.HomeDir, "USERPROFILE=" + o.HomeDir, "CLAUDE_CONFIG_DIR=" + o.ConfigDir, "TMPDIR=" + o.HomeDir, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(o.HomeDir), "BUN_OPTIONS=--no-env-file", "PATH=" + filepath.Dir(bun) + ":/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"now": now, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source auth status: %v\n%s", err, stderr.String())
	}
	var expected []any
	if json.Unmarshal(output, &expected) != nil || len(expected) != len(rows) {
		t.Fatalf("Source auth output %s", output)
	}
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	for i, row := range rows {
		if row["absent"] == true {
			err = parent.Grok().DeleteTokens(ctx)
		} else {
			data, _ := json.Marshal(row["tokens"])
			err = os.WriteFile(parent.Grok().TokenPath(), data, 0600)
		}
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		request := httptest.NewRequest("GET", "http://localhost:3456/api/providers/auth-status", nil)
		request.Header.Set("Authorization", "Bearer fixture-local")
		s.ServeHTTP(w, request)
		var body any
		if json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatal(w.Body.String())
		}
		actual := map[string]any{"status": float64(w.Code), "body": body}
		if !reflect.DeepEqual(actual, expected[i]) {
			t.Errorf("row %d Source %v Go %v", i, expected[i], actual)
		}
	}
}
