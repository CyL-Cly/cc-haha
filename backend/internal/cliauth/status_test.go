package cliauth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const validToken = `{"accessToken":"fixture-access","refreshToken":"fixture-refresh","expiresAt":1700000000123,"email":"fixture@example.invalid","accountId":"fixture-account"}`

func TestOpenAIStatusSourceJSONAndTextContract(t *testing.T) {
	store := NewOpenAIStorage(StorageOptions{Home: t.TempDir(), ReadSecure: func() (json.RawMessage, error) {
		return json.RawMessage(`{"openaiCodexOauth":` + validToken + `}`), nil
	}})
	jsonResult := OpenAIStatus(store, false)
	want := "{\n  \"loggedIn\": true,\n  \"authMethod\": \"openai_oauth\",\n  \"provider\": \"openai\",\n  \"email\": \"fixture@example.invalid\",\n  \"accountId\": \"fixture-account\",\n  \"expiresAt\": 1700000000123\n}\n"
	if jsonResult.Stdout != want || jsonResult.ExitCode != 0 || jsonResult.Stderr != "" || jsonResult.Diagnostic != nil {
		t.Fatalf("JSON output differs from actual TS handler: %#v", jsonResult)
	}
	text := OpenAIStatus(store, true)
	if text.Stdout != "Provider: openai\nEmail: fixture@example.invalid\nAccount ID: fixture-account\nExpires At: 2023-11-14T22:13:20.123Z\n" || text.ExitCode != 0 {
		t.Fatalf("text output differs from actual TS handler: %#v", text)
	}
	missing := OpenAIStatus(NewOpenAIStorage(StorageOptions{Home: t.TempDir()}), true)
	if missing.ExitCode != 1 || missing.Stdout != "Not logged in to OpenAI. Run claude auth login --openai to authenticate.\n" {
		t.Fatalf("missing credentials: %#v", missing)
	}
}

func TestOpenAIStatusNumberSemanticsAndOptionalFields(t *testing.T) {
	for _, test := range []struct {
		expiry, expected string
		code             int
	}{{"-0", "0", 1}, {"-1", "-1", 1}, {"1e-400", "0", 1}, {"1", "1", 0}, {"1e400", "null", 0}} {
		t.Run(test.expiry, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "tokens.json")
			if err := os.WriteFile(file, []byte(`{"accessToken":"","refreshToken":"r","expiresAt":`+test.expiry+`,"email":false,"accountId":7}`), 0600); err != nil {
				t.Fatal(err)
			}
			store := NewOpenAIStorage(StorageOptions{Environment: func(name string) string {
				if name == "OPENAI_CODEX_OAUTH_FILE" {
					return file
				}
				return ""
			}})
			result := OpenAIStatus(store, false)
			if result.ExitCode != test.code || result.Diagnostic != nil || !strings.Contains(result.Stdout, `"expiresAt": `+test.expected) || !strings.Contains(result.Stdout, "\"email\": null,\n  \"accountId\": null") {
				t.Fatalf("numeric/file normalization: %#v", result)
			}
		})
	}
	for _, test := range []struct{ expiry, date string }{{"0.99", "1970-01-01T00:00:00.000Z"}, {"253402300800000", "+010000-01-01T00:00:00.000Z"}, {"8640000000000000", "+275760-09-13T00:00:00.000Z"}} {
		store := NewOpenAIStorage(StorageOptions{Home: t.TempDir(), ReadSecure: func() (json.RawMessage, error) {
			return json.RawMessage(`{"openaiCodexOauth":{"refreshToken":"r","expiresAt":` + test.expiry + `}}`), nil
		}})
		if result := OpenAIStatus(store, true); result.Stdout != "Provider: openai\nExpires At: "+test.date+"\n" || result.Diagnostic != nil {
			t.Fatalf("date %s: %#v", test.expiry, result)
		}
	}
}

func TestOpenAIStoragePriorityNullCacheAndClear(t *testing.T) {
	home := t.TempDir()
	file := filepath.Join(home, "tokens.json")
	env := map[string]string{"OPENAI_CODEX_OAUTH_FILE": " \ufeff" + file + "\u00a0 ", "CLAUDE_CONFIG_DIR": filepath.Join(home, "config")}
	trace := []string{}
	secureCalls := 0
	store := NewOpenAIStorage(StorageOptions{Home: home, Environment: func(name string) string { return env[name] }, Observe: func(op, _ string) { trace = append(trace, op) }, ReadSecure: func() (json.RawMessage, error) {
		secureCalls++
		return json.RawMessage(`{"openaiCodexOauth":` + validToken + `}`), nil
	}})
	if result := OpenAIStatus(store, false); result.ExitCode != 1 || secureCalls != 0 {
		t.Fatalf("selected missing file fell back: %#v", result)
	}
	if err := os.WriteFile(file, []byte(validToken), 0600); err != nil {
		t.Fatal(err)
	}
	if OpenAIStatus(store, false).ExitCode != 1 {
		t.Fatal("null cache must survive file appearance")
	}
	store.Clear()
	if OpenAIStatus(store, false).ExitCode != 0 || secureCalls != 0 {
		t.Fatal("clear must reload selected file without secure fallback")
	}
	delete(env, "OPENAI_CODEX_OAUTH_FILE")
	if OpenAIStatus(store, false).ExitCode != 0 || secureCalls != 1 {
		t.Fatal("secure cache has a different key")
	}
	marker := filepath.Join(env["CLAUDE_CONFIG_DIR"], "cc-haha", "openai-oauth-file-backed")
	if err := os.MkdirAll(filepath.Dir(marker), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if OpenAIStatus(store, false).ExitCode != 0 {
		t.Fatal("memoized secure read must not recheck marker")
	}
	store.Clear()
	if OpenAIStatus(store, false).ExitCode != 1 || secureCalls != 1 {
		t.Fatal("marker must stop secure fallback after clear")
	}
	if !reflect.DeepEqual(trace, []string{"file", "file", "marker", "secure", "marker"}) {
		t.Fatalf("storage calls: %#v", trace)
	}
}

func TestOpenAIFileStrictNormalizationUTF16AndReplacement(t *testing.T) {
	for _, raw := range []string{"null", "[]", "{}", "{broken", "\ufeff" + validToken, `{"accessToken":1,"refreshToken":"r","expiresAt":1}`, `{"accessToken":"a","refreshToken":"r","expiresAt":"1"}`} {
		if tokens := normalizeDesktopFile([]byte(raw)); tokens != nil {
			t.Fatalf("invalid file admitted: %s", raw)
		}
	}
	raw := `{"accessToken":"a","refreshToken":"r","expiresAt":1,"email":"邮箱😀\ud800\u2028<>&","accountId":"\udfff\n\t"}`
	store := NewOpenAIStorage(StorageOptions{Environment: func(string) string { return "tokens" }, ReadFile: func(string) ([]byte, error) { return []byte(raw), nil }})
	result := OpenAIStatus(store, false)
	if !strings.Contains(result.Stdout, `\ud800`) || !strings.Contains(result.Stdout, `\udfff`) || !strings.Contains(result.Stdout, "<>&") || strings.Contains(result.Stdout, `\u003c`) {
		t.Fatalf("JSON UTF16/escaping: %q", result.Stdout)
	}
	if text := OpenAIStatus(store, true); !strings.Contains(text.Stdout, "Email: 邮箱😀�\u2028<>&\nAccount ID: �\n\t\n") {
		t.Fatalf("text replaces lone UTF16 only at byte output: %q", text.Stdout)
	}
	invalid := append([]byte(`{"accessToken":"a","refreshToken":"r","expiresAt":1,"email":"`), 0xe2, 0x82, '"', '}')
	if normalized := normalizeDesktopFile(invalid); !strings.Contains(string(normalized), "�") {
		t.Fatalf("replacement decode: %s", normalized)
	}
	duplicates := normalizeDesktopFile([]byte(`{"accessToken":"a","refreshToken":"x","refreshToken":"r","expiresAt":-1,"expiresAt":1,"idToken":"id","clientId":"client"}`))
	if string(rawField(duplicates, "refreshToken")) != `"r"` || string(rawField(duplicates, "expiresAt")) != "1" || string(rawField(duplicates, "idToken")) != `"id"` {
		t.Fatalf("duplicate/optional fields: %s", duplicates)
	}
}

func TestOpenAIStorageErrorsAndPendingDiagnosticsStayExplicit(t *testing.T) {
	reads := 0
	store := NewOpenAIStorage(StorageOptions{Home: t.TempDir(), ReadSecure: func() (json.RawMessage, error) { reads++; return nil, errors.New("unavailable") }})
	if OpenAIStatus(store, false).ExitCode != 1 || OpenAIStatus(store, false).ExitCode != 1 || reads != 1 {
		t.Fatal("failed read must be cached")
	}
	for _, test := range []struct {
		tokens string
		text   bool
		name   string
	}{{`{}`, false, "UnmigratedAuthCredentialShape"}, {`{"refreshToken":null,"expiresAt":1}`, false, "UnmigratedAuthCredentialShape"}, {`{"refreshToken":"r","expiresAt":1e400}`, true, "RangeError"}, {`{"refreshToken":"r","expiresAt":1,"email":7}`, true, "UnmigratedAuthCredentialShape"}} {
		storage := NewOpenAIStorage(StorageOptions{Home: t.TempDir(), ReadSecure: func() (json.RawMessage, error) {
			return json.RawMessage(`{"openaiCodexOauth":` + test.tokens + `}`), nil
		}})
		result := OpenAIStatus(storage, test.text)
		if result.ExitCode != 1 || result.Diagnostic == nil || result.Diagnostic.Name != test.name || result.Diagnostic.StdioComplete {
			t.Fatalf("pending diagnostic was hidden: %#v", result)
		}
	}
	for _, value := range []string{"null", "false", "0", "-0", "1e-400", `""`} {
		storage := NewOpenAIStorage(StorageOptions{Home: t.TempDir(), ReadSecure: func() (json.RawMessage, error) { return json.RawMessage(`{"openaiCodexOauth":` + value + `}`), nil }})
		if result := OpenAIStatus(storage, false); result.ExitCode != 1 || result.Diagnostic != nil {
			t.Fatalf("falsy external record: %#v", result)
		}
	}
}

func TestOpenAIStorageConcurrentStatusKeepsOneCachedRead(t *testing.T) {
	var reads atomic.Int32
	store := NewOpenAIStorage(StorageOptions{Home: t.TempDir(), ReadSecure: func() (json.RawMessage, error) {
		reads.Add(1)
		return json.RawMessage(`{"openaiCodexOauth":` + validToken + `}`), nil
	}})
	var group sync.WaitGroup
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			if result := OpenAIStatus(store, false); result.ExitCode != 0 {
				t.Errorf("status: %#v", result)
			}
		}()
	}
	group.Wait()
	if reads.Load() != 1 {
		t.Fatalf("secure read count: %d", reads.Load())
	}
}
