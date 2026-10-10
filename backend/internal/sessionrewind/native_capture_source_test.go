package sessionrewind

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNativeCheckpointCaptureActualOriginalTrackAndCompletedBackups(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []map[string]any{{"before": "before\n", "after": "after\n"}, {"before": "", "after": "added"}, {"missing": true}, {"before": "protected", "hardlink": true}, {"before": "protected", "symlink": true}, {"before": strings.Repeat("buffer\x00", 180000), "after": "small after", "mode": 0600}}
	home := t.TempDir()
	if e := os.MkdirAll(filepath.Join(home, "tmp"), 0700); e != nil {
		t.Fatal(e)
	}
	input, _ := json.Marshal(map[string]any{"home": home, "cases": rows})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-file-history-capture.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_FILE_HISTORY_CAPTURE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=fake-checkpoint-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1"}
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("actual Source capture: %v %.2000s", e, out)
	}
	var receipt struct {
		Results []struct {
			SessionID, Path, Work string
			Before, After         *struct {
				BackupFileName *string
				Version        int
			}
			Disk []struct {
				Name, Bytes string
				Mode        uint32
			}
			Errors []string
		}
	}
	if e = json.Unmarshal(out, &receipt); e != nil || len(receipt.Results) != len(rows) {
		t.Fatalf("receipt: %v %.2000s", e, out)
	}
	for i, row := range rows {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			x := receipt.Results[i]
			// Reuse the original private path to retain the exact path hash; restore
			// its before bytes, then keep all Go backup writes in a separate config.
			if row["symlink"] != true && row["hardlink"] != true && row["missing"] != true {
				mode := os.FileMode(0640)
				if row["mode"] != nil {
					mode = 0600
				}
				if e = os.WriteFile(x.Path, []byte(row["before"].(string)), mode); e != nil {
					t.Fatal(e)
				}
			}
			n := NativeFiles{ConfigHome: filepath.Join(t.TempDir(), "config")}
			uuid := func() (string, error) { return "physical-source-temp", nil }
			got, e := n.CaptureBackup(context.Background(), x.SessionID, x.Work, x.Path, 1, "", uuid)
			if x.Before == nil {
				if e == nil {
					t.Fatal("Source refused capture but Go accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if x.Before.BackupFileName == nil {
				if got.Name != nil {
					t.Fatal("missing file captured")
				}
				return
			}
			if got.Name == nil || *got.Name != *x.Before.BackupFileName {
				t.Fatal("Source before filename drift", got, x.Before)
			}
			if after, ok := row["after"].(string); ok {
				if e = os.WriteFile(x.Path, []byte(after), 0600); e != nil {
					t.Fatal(e)
				}
				if x.After == nil || x.After.BackupFileName == nil {
					t.Fatal("Source after missing")
				}
				prefix := *x.Before.BackupFileName + "-"
				suffix := strings.TrimPrefix(*x.After.BackupFileName, prefix)
				got, e = n.CaptureBackup(context.Background(), x.SessionID, x.Work, x.Path, x.After.Version, suffix, uuid)
				if e != nil || got.Name == nil || *got.Name != *x.After.BackupFileName {
					t.Fatal("Source after filename drift", e, got)
				}
			}
			for _, disk := range x.Disk {
				p := filepath.Join(n.ConfigHome, "file-history", x.SessionID, disk.Name)
				data, e := os.ReadFile(p)
				if e != nil || hex.EncodeToString(data) != disk.Bytes {
					t.Fatal("Source physical backup bytes drift", disk.Name, e)
				}
				info, e := os.Stat(p)
				if e != nil || uint32(info.Mode().Perm()) != disk.Mode {
					t.Fatal("Source physical backup mode drift", disk.Name, e, info)
				}
			}
		})
	}
}
