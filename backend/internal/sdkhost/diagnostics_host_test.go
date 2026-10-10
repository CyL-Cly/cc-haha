package sdkhost_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

func TestDiagnosticsHostNeedsOneOriginalProcessBeforeAdmission(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "different-process"}[mismatch], func(t *testing.T) {
			stdout, result := originalBoundary(t, nativeControls{AllowFailure: true, Configure: func(options *sdkhost.Options) {
				base := filepath.Join(t.TempDir(), "diagnostic", "events")
				options.OriginalEnv["CLAUDE_CODE_DIAGNOSTICS_FILE"] = base
				if mismatch {
					process, err := diagnostics.NewProcess(diagnostics.Options{Environment: func(string) string { return base }, Now: func() time.Time { return time.UnixMilli(1700000000000) }, NowMillis: func() int64 { return 1700000000000 }, PID: 123, Platform: "darwin", Files: diagnostics.OSFileSystem{}})
					if err != nil {
						t.Fatal(err)
					}
					options.Diagnostics = process
				}
			}})
			want := "diagnostics process sink unavailable"
			if mismatch {
				want = "diagnostics process construction identity"
			}
			requireFailure(t, result, want)
			if len(stdout) != 0 || len(result["requests"].([]string)) != 0 {
				t.Fatal("unbound diagnostics host started output/model")
			}
		})
	}
}
