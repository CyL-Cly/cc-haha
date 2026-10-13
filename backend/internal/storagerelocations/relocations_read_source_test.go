package storagerelocations

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestReadRelocationMetadataFailuresMatchActualSourceAndRetainProtectedBytes(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	module, _ := filepath.Abs("../../../src/utils/storageRelocations.ts")
	guard, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/fixture-guard.ts")
	moduleJSON, _ := json.Marshal(module)
	guardJSON, _ := json.Marshal(guard)
	script := `import { assertDisposableFixtureHome } from ` + string(guardJSON) + `;
const input=await Bun.stdin.json();assertDisposableFixtureHome(input.homeDir,'relocation reader Source');
const {readStorageRelocations}=await import(` + string(moduleJSON) + `);
try {const value=readStorageRelocations(input.config);process.stdout.write(JSON.stringify({error:null,value}));}
catch(error) {process.stdout.write(JSON.stringify({error:error.message}));}`
	for _, fixture := range []struct{ name, data string }{
		{"malformed-json", `{"version":`},
		{"valid-json-wrong-array-shape", `[]`},
		{"valid-json-null", `null`},
		{"valid-json-string", `"old-config"`},
		{"unsupported-version", `{"version":2,"previousRoots":[]}`},
		{"non-absolute-roots", `{"version":1,"previousRoots":["relative"]}`},
		{"legacy-absent-metadata", ""},
		{"future-fields-retained", `{"version":1,"previousRoots":[],"unknownFuture":{"keep":true}}`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			home := t.TempDir()
			config := filepath.Join(home, ".claude")
			path := filepath.Join(config, "cc-haha", "storage-relocations.json")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if fixture.data != "" {
				if err := os.WriteFile(path, []byte(fixture.data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, bun, "--no-env-file", "-e", script)
			child.Dir = home
			child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
			input, _ := json.Marshal(map[string]string{"homeDir": home, "config": config})
			child.Stdin = bytes.NewReader(input)
			var stderr bytes.Buffer
			child.Stderr = &stderr
			output, err := child.Output()
			if err != nil {
				t.Fatal("actual relocation reader", err, stderr.String())
			}
			var source struct {
				Error *string
				Value map[string]json.RawMessage
			}
			if err = json.Unmarshal(output, &source); err != nil {
				t.Fatal(err, string(output))
			}
			native, nativeErr := Read(config, runtime.GOOS)
			if source.Error == nil {
				if nativeErr != nil || !bytes.Equal(mustJSON(native.Document), mustJSON(source.Value)) {
					t.Fatal("actual valid/default relocation metadata differs", nativeErr, string(mustJSON(native.Document)), string(mustJSON(source.Value)))
				}
			} else if nativeErr == nil || nativeErr.Error() != *source.Error {
				t.Fatal("actual metadata read/shape error boundary differs", nativeErr, *source.Error)
			}
			if fixture.data != "" {
				retained, err := os.ReadFile(path)
				if err != nil || string(retained) != fixture.data {
					t.Fatal("metadata read repaired protected bytes", string(retained), err)
				}
			}
		})
	}
}
