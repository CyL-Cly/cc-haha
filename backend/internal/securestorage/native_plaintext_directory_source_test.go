package securestorage

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativePlaintextDirectoryConsumerAgainstWholeSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-config-directory-runtime.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--feature=TRANSCRIPT_CLASSIFIER", "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_CONFIG_DIRECTORY_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"home": home, "warmProject": false})
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source plaintext path producer %v %.6500s", err, output)
	}
	var source struct {
		Initial, Redirected                             string
		Before, After                                   struct{ Credential json.RawMessage }
		InitialCredential, RedirectedCredential         string
		InitialCredentialMode, RedirectedCredentialMode uint32
		CredentialAsync                                 json.RawMessage
		CredentialUpdate                                Result
		CredentialDelete                                bool
		CredentialAfterDelete                           json.RawMessage
		InitialCredentialAfterDelete                    string
		Caps                                            struct{ Blocked, SecureReads []json.RawMessage }
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 {
		t.Fatalf("invalid Source/private boundary %v %.6500s", err, output)
	}
	if err := os.WriteFile(filepath.Join(source.Redirected, ".credentials.json"), source.After.Credential, 0600); err != nil {
		t.Fatal(err)
	}
	current := source.Initial
	store, err := New(Options{Home: home, ConfigDir: current, Platform: "linux", Environment: map[string]string{}, NativePlaintextLocation: func() (string, string) { return current, filepath.Join(current, ".credentials.json") }})
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.ReadSourceJSON(ctx)
	if err != nil || !bytes.Equal(before, source.Before.Credential) {
		t.Fatal("initial plaintext read", string(before), err)
	}
	current = source.Redirected
	after, err := store.ReadSourceJSON(ctx)
	if err != nil || !bytes.Equal(after, source.After.Credential) {
		t.Fatalf("current plaintext credential got %s Source %s: %v", after, source.After.Credential, err)
	}
	var value protocol.JSONObject
	if err := value.UnmarshalJSON(after); err != nil {
		t.Fatal(err)
	}
	value.Set("fixtureRevision", json.RawMessage("2"))
	raw, _ := value.MarshalJSON()
	result := store.UpdateSourceJSON(ctx, raw)
	if result != source.CredentialUpdate {
		t.Fatal("actual plaintext update result", result, source.CredentialUpdate)
	}
	next, err := store.ReadSourceJSONAsync(ctx)
	if err != nil || !bytes.Equal(next, source.CredentialAsync) {
		t.Fatal("actual asynchronous plaintext read", string(next), string(source.CredentialAsync), err)
	}
	for path, want := range map[string]string{filepath.Join(source.Initial, ".credentials.json"): source.InitialCredential, filepath.Join(source.Redirected, ".credentials.json"): source.RedirectedCredential} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Fatalf("physical plaintext file %q %v\ngot %s\nSource %s", path, err, data, want)
		}
		info, err := os.Stat(path)
		mode := source.RedirectedCredentialMode
		if path == filepath.Join(source.Initial, ".credentials.json") {
			mode = source.InitialCredentialMode
		}
		if err != nil || uint32(info.Mode().Perm()) != mode {
			t.Fatal("plaintext credential Source permissions", path, mode, err)
		}
	}
	if deleted := store.Delete(ctx); deleted != source.CredentialDelete {
		t.Fatal("current plaintext delete differs from Source", deleted, source.CredentialDelete)
	}
	if _, err := os.Stat(filepath.Join(source.Redirected, ".credentials.json")); !os.IsNotExist(err) {
		t.Fatal("current plaintext credential remains", err)
	}
	if data, err := os.ReadFile(filepath.Join(source.Initial, ".credentials.json")); err != nil || string(data) != source.InitialCredentialAfterDelete {
		t.Fatal("current plaintext delete modified initial file", err)
	}
	if next, err := store.ReadSourceJSONAsync(ctx); err != nil || next != nil || string(source.CredentialAfterDelete) != "null" {
		t.Fatal("current plaintext asynchronous read after delete differs from Source", string(next), string(source.CredentialAfterDelete), err)
	}
}
