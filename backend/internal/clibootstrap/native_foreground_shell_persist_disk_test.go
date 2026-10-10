package clibootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Exercise the production persistence through the actual interactive Bash
// helper: process result files are real, Source's disk block is real, and no
// provider request is made for these sparse-file truncation/copy semantics.
func TestForegroundBashPersistenceDiskAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	m := taskMainFixture(t)
	home := m.options.Native.Home
	_ = os.MkdirAll(m.options.Native.Temp, 0700)
	_ = os.MkdirAll(filepath.Join(home, ".git"), 0700)
	rows := []map[string]any{{"id": "b0123abca", "base64": base64.StdEncoding.EncodeToString([]byte("private actual source data"))}, {"id": "b0123abcb", "base64": base64.StdEncoding.EncodeToString([]byte("private copied data")), "existing": true}, {"id": "b0123abcc", "base64": base64.StdEncoding.EncodeToString([]byte("private sparse data")), "size": 64*1024*1024 + 128}}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-foreground-shell.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "TMPDIR=" + m.options.Native.Temp, "CLAUDE_CODE_TMPDIR=" + m.options.Native.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_FOREGROUND_SHELL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "cwd": m.options.Native.CWD, "persists": rows})
	cmd.Stdin = bytes.NewReader(payload)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.4500s", e, out)
	}
	var source struct {
		Persisted    []protocol.JSONObject
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Persisted) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape %v %.4500s", e, out)
	}
	// Source owns the first files; independently construct equivalent files in a
	// second disposable namespace and use the production Go physical helper.
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		directory, e := m.Controller.ToolResultsDirectoryOnOwner(scope)
		if e != nil {
			return e
		}
		if e = os.MkdirAll(directory, 0700); e != nil {
			return e
		}
		for i, row := range rows {
			id := row["id"].(string) + "go"
			path := filepath.Join(m.options.Native.Temp, id+".output")
			value, _ := base64.StdEncoding.DecodeString(row["base64"].(string))
			if e = os.WriteFile(path, value, 0600); e != nil {
				return e
			}
			size := int64(len(value))
			if n, ok := row["size"].(int); ok {
				size = int64(n)
				if e = os.Truncate(path, size); e != nil {
					return e
				}
			}
			if row["existing"] == true {
				if e = os.WriteFile(filepath.Join(directory, id+".txt"), []byte("original prior destination"), 0600); e != nil {
					return e
				}
			}
			// Dispatch through the same exported service operation used by the live
			// foreground process, preserving the owner and actual native binding.
			destination, e := m.nativeTools.PersistForegroundShellOutput(scope, path, id, size)
			if e != nil {
				return e
			}
			st, e := os.Stat(path)
			if e != nil {
				return e
			}
			dest, e := os.Stat(destination)
			if e != nil {
				return e
			}
			expected := source.Persisted[i]
			if int64(diskNumber(expected, "sourceSize")) != st.Size() || int64(diskNumber(expected, "destSize")) != dest.Size() || (string(expected.Get("sameFile")) == "true") != os.SameFile(st, dest) {
				t.Errorf("diskcase%d Go size%d/%d same%v Source%s", i, st.Size(), dest.Size(), os.SameFile(st, dest), rawMain(expected))
			}
			if int64(diskNumber(expected, "persistedOutputSize")) != size {
				t.Fatal("Source original size must precede truncation", rawMain(expected), size)
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}

func diskNumber(row protocol.JSONObject, key string) float64 {
	var value float64
	_ = json.Unmarshal(row.Get(key), &value)
	return value
}
