package securestorage

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func record(t *testing.T, source string) protocol.JSONObject {
	t.Helper()
	var v protocol.JSONObject
	if err := v.UnmarshalJSON([]byte(source)); err != nil {
		t.Fatal(err)
	}
	return v
}
func TestPlaintextUsesOriginalFilePermissionsAndPreservesOtherAccounts(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	s, err := New(Options{Home: home, ConfigDir: config, Platform: "linux", Environment: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if value, err := s.Read(context.Background()); value != nil || err != nil {
		t.Fatal(value, err)
	}
	if result := s.Update(context.Background(), record(t, `{"claudeAiOauth":{"accessToken":"owned-fixture"},"future":{"keep":true}}`)); !result.Success || result.Warning == "" {
		t.Fatal(result)
	}
	info, err := os.Stat(filepath.Join(config, ".credentials.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	var group sync.WaitGroup
	for _, key := range []string{"first", "second"} {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := s.Change(context.Background(), func(o protocol.JSONObject) error { o.Set(key, []byte(`"saved"`)); return nil })
			if err != nil || !result.Success {
				t.Error(result, err)
			}
		}()
	}
	group.Wait()
	value, err := s.Read(context.Background())
	if err != nil || value.Get("first") == nil || value.Get("second") == nil || value.Get("claudeAiOauth") == nil || value.Get("future") == nil {
		t.Fatal(value, err)
	}
	if !s.Delete(context.Background()) || !s.Delete(context.Background()) {
		t.Fatal("delete missing must succeed")
	}
	if err := os.WriteFile(s.path, []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	value, err = s.Read(context.Background())
	if err != nil || value != nil {
		t.Fatal("malformed file should be unreadable", value, err)
	}
}

func TestKeychainTTLStaleReadFallbackAndPayloadBoundary(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	now := time.Unix(1700000000, 0)
	primary := []byte(`{"token":"old"}`)
	failRead, failWrite := false, false
	calls := []Command{}
	s, err := New(Options{Home: home, ConfigDir: config, Platform: "darwin", ConfigDirExplicit: true, Environment: map[string]string{"USER": "owned-test-user"}, Now: func() time.Time { return now }, RunSecurity: func(_ context.Context, c Command) ([]byte, error) {
		calls = append(calls, c)
		switch c.Args[0] {
		case "find-generic-password":
			if failRead {
				return nil, errors.New("private unavailable")
			}
			return primary, nil
		case "delete-generic-password":
			primary = nil
			return nil, nil
		case "-i", "add-generic-password":
			if failWrite {
				return nil, errors.New("private write failed")
			}
			payload := ""
			if c.Args[0] == "-i" {
				parts := strings.Split(c.Input, ` -X "`)
				payload = strings.TrimSuffix(parts[1], "\"\n")
			} else {
				payload = c.Args[len(c.Args)-1]
			}
			data, e := hex.DecodeString(payload)
			if e != nil {
				return nil, e
			}
			primary = data
			return nil, nil
		}
		return nil, errors.New("unexpected private security command")
	}})
	if err != nil {
		t.Fatal(err)
	}
	value, err := s.Read(context.Background())
	if err != nil || value.String("token") != "old" {
		t.Fatal(value, err)
	}
	value.Set("token", []byte(`"mutated"`))
	value, _ = s.Read(context.Background())
	if value.String("token") != "old" || len(calls) != 1 {
		t.Fatal("cache alias or TTL", value, calls)
	}
	now = now.Add(31 * time.Second)
	failRead = true
	value, _ = s.Read(context.Background())
	if value.String("token") != "old" {
		t.Fatal("transient read lost known credentials")
	}
	failWrite = true
	result := s.Update(context.Background(), record(t, `{"token":"fresh","other":{"keep":true}}`))
	if !result.Success || result.Warning == "" || primary != nil {
		t.Fatal("fallback must detach old primary", result, string(primary))
	}
	value, _ = s.Read(context.Background())
	if value.String("token") != "fresh" {
		t.Fatal("stale keychain shadowed fresh fallback")
	}
	failRead, failWrite = false, false
	result = s.Update(context.Background(), record(t, `{"token":"new"}`))
	if !result.Success || result.Warning != "" {
		t.Fatal(result)
	}
	if _, err := os.Stat(s.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("migration retained fallback", err)
	}
	short := calls[len(calls)-1]
	if short.Args[0] != "-i" || short.Input == "" {
		t.Fatal("short payload should use stdin")
	}
	large := protocol.NewJSONObject()
	large.Set("large", protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(strings.Repeat("a", 3000)))))
	if !s.Update(context.Background(), large).Success {
		t.Fatal("large keychain write failed")
	}
	if calls[len(calls)-1].Args[0] != "add-generic-password" || calls[len(calls)-1].Input != "" {
		t.Fatal("large payload was truncated through stdin")
	}
	if !s.Delete(context.Background()) {
		t.Fatal("keychain delete failed")
	}
}

func TestOriginalKeychainIdentity(t *testing.T) {
	if got := ServiceName("/owned/.claude", false, map[string]string{}); got != "Claude Code-credentials" {
		t.Fatal(got)
	}
	if got := ServiceName("/owned/.claude", true, map[string]string{}); got != "Claude Code-credentials-6ab70e8e" {
		t.Fatal(got)
	}
	if got := ServiceName("/owned/.claude", false, map[string]string{"USER_TYPE": "external", "USE_LOCAL_OAUTH": "1"}); got != "Claude Code-credentials" {
		t.Fatal(got)
	}
	if got := ServiceName("/owned/.claude", false, map[string]string{"USER_TYPE": "ant", "USE_LOCAL_OAUTH": "1"}); got != "Claude Code-local-oauth-credentials" {
		t.Fatal(got)
	}
	if got := ServiceName("/owned/.claude", false, map[string]string{"CLAUDE_CODE_CUSTOM_OAUTH_URL": "https://owned.invalid"}); got != "Claude Code-custom-oauth-credentials" {
		t.Fatal(got)
	}
}
