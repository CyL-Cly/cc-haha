package whatsappcandidate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

type authRecoveryDisk struct {
	Kind string
	Mode uint32
	Text string
}

func authRecoverySource(t *testing.T, home string, input any, target any) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual original authentication Source Bun required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, bun, "--no-env-file", nodeSourceFixturePath("node_auth_recovery.fixture.ts"))
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	child.Stdin = bytes.NewReader(nodeJSON(input))
	out, err := child.CombinedOutput()
	if err != nil {
		t.Fatal("actual original authentication helper/consumer", err, string(out))
	}
	if err := json.Unmarshal(out, target); err != nil {
		t.Fatal("authentication Source receipt", err, string(out))
	}
}

func authRecoveryMaterialize(t *testing.T, path string, disk authRecoveryDisk) {
	t.Helper()
	switch disk.Kind {
	case "directory":
		if err := os.Mkdir(path, os.FileMode(disk.Mode)); err != nil {
			t.Fatal(err)
		}
	case "file":
		if err := os.WriteFile(path, []byte(disk.Text), os.FileMode(disk.Mode)); err != nil {
			t.Fatal(err)
		}
	case "absent":
	default:
		t.Fatal("unknown actual Source disk kind", disk.Kind)
	}
}

func authRecoveryRead(t *testing.T, path string) authRecoveryDisk {
	t.Helper()
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return authRecoveryDisk{Kind: "absent"}
	}
	if err != nil {
		t.Fatal(err)
	}
	if info.IsDir() {
		return authRecoveryDisk{Kind: "directory", Mode: uint32(info.Mode().Perm())}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return authRecoveryDisk{Kind: "file", Mode: uint32(info.Mode().Perm()), Text: string(data)}
}

func authRecoveryEquivalentDisk(t *testing.T, actual, source authRecoveryDisk) {
	t.Helper()
	if actual.Kind != source.Kind || actual.Mode != source.Mode {
		t.Fatalf("actual protected auth file boundary Go=%+v original=%+v", actual, source)
	}
	if json.Valid([]byte(actual.Text)) && json.Valid([]byte(source.Text)) {
		var got, want any
		_ = json.Unmarshal([]byte(actual.Text), &got)
		_ = json.Unmarshal([]byte(source.Text), &want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("old credential fields or future data changed: Go=%s original=%s", actual.Text, source.Text)
		}
	} else if actual.Text != source.Text {
		t.Fatal("protected malformed credential bytes changed")
	}
}

func TestActualOriginalAuthBackupColdRecoveryAndQueuedSavePreserveFutureFields(t *testing.T) {
	profiles := []map[string]any{
		{"name": "restore-missing", "current": "absent", "backup": "valid", "save": true},
		{"name": "restore-broken", "current": "broken", "backup": "valid", "save": true},
		{"name": "keep-current", "current": "valid", "backup": "broken", "save": true},
		{"name": "backup-save-failure", "current": "valid", "backup": "directory", "save": true},
		{"name": "fresh-invalid", "current": "broken", "backup": "broken", "save": false},
		{"name": "fresh-absent", "current": "absent", "backup": "absent", "save": false},
		{"name": "protected-directory", "current": "directory", "backup": "valid", "save": false},
	}
	home := t.TempDir()
	var source struct {
		Rows []struct {
			Name          string
			Before, After struct{ Current, Backup authRecoveryDisk }
			Cold          json.RawMessage
			Saved         *struct {
				Current, Backup authRecoveryDisk
				QueueCleared    bool
			}
			Error *string
		}
		Warnings []string
	}
	authRecoverySource(t, home, map[string]any{"home": home, "profiles": profiles}, &source)
	if len(source.Rows) != len(profiles) || len(source.Warnings) != 0 {
		t.Fatal("original recovery receipt incomplete", len(source.Rows), source.Warnings)
	}
	for _, row := range source.Rows {
		t.Run(row.Name, func(t *testing.T) {
			directory := t.TempDir()
			current := filepath.Join(directory, "creds.json")
			authRecoveryMaterialize(t, current, row.Before.Current)
			authRecoveryMaterialize(t, current+".bak", row.Before.Backup)
			device, native, err := OpenNodeDevice(directory, nil)
			if row.Error != nil {
				if *row.Error != "EISDIR" || err == nil || !strings.Contains(err.Error(), syscall.EISDIR.Error()) {
					t.Fatal("original protected-directory failure did not propagate", row.Error, err)
				}
			} else if err != nil {
				t.Fatal("actual cold original credential consumer", err)
			} else if row.Saved != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := native.PutDevice(ctx, device); err != nil {
					t.Fatal(err)
				}
				if err := native.WaitCredsSave(ctx); err != nil {
					t.Fatal(err)
				}
				if _, pending := nodeCredsSaveQueues.Load(directory); pending || !row.Saved.QueueCleared {
					t.Fatal("cold authentication save queue did not drain")
				}
				consumerHome := t.TempDir()
				consumerDir := filepath.Join(consumerHome, "old-consumer")
				if err := os.Mkdir(consumerDir, 0700); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(current)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(consumerDir, "creds.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
				var cold struct{ Creds json.RawMessage }
				authRecoverySource(t, consumerHome, map[string]any{"home": consumerHome, "consume": consumerDir}, &cold)
				var got, expected any
				_ = json.Unmarshal(cold.Creds, &got)
				var old nodeObject
				_ = json.Unmarshal(row.Cold, &old)
				// Source JSON.stringify exposes Buffers as numeric arrays in this
				// reporting branch; compare the protected physical JSON below and
				// the future fields through the actual cold consumer explicitly.
				_ = json.Unmarshal(nodeJSON(map[string]any{"privateFuture": old["privateFuture"], "me": old["me"], "platform": old["platform"]}), &expected)
				gotFields := map[string]any{}
				for _, name := range []string{"privateFuture", "me", "platform"} {
					gotFields[name] = got.(map[string]any)[name]
				}
				if !reflect.DeepEqual(gotFields, expected) {
					t.Fatalf("actual original cold consumer lost account/future fields Go=%v original=%v", gotFields, expected)
				}
			} else if device.ID != nil {
				t.Fatal("malformed or absent auth fabricated a registered device")
			}
			authRecoveryEquivalentDisk(t, authRecoveryRead(t, current), row.After.Current)
			authRecoveryEquivalentDisk(t, authRecoveryRead(t, current+".bak"), row.After.Backup)
		})
	}
}
