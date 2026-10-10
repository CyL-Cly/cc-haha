package sdkhost_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
)

func TestActualSDKBoundaryLongProjectPathUsesOriginalHash(t *testing.T) {
	longTemp := filepath.Join(t.TempDir(), strings.Repeat("private-sdk-path-", 12))
	if err := os.MkdirAll(longTemp, 0700); err != nil {
		t.Fatal(err)
	}
	stdout, result := originalBoundaryInHome(t, nativeControls{}, longTemp)
	home, err := os.Getwd()
	if err != nil || len(home) <= projectpath.MaxSanitizedLength {
		t.Fatalf("physical long-path fixture missing: %q %v", home, err)
	}
	if result["runError"] != nil || len(result["requests"].([]string)) != 2 || len(stdout) == 0 {
		t.Fatalf("original SDK resume and two physical requests failed: %v", result["runError"])
	}
	prefix := filepath.Join(".claude", "projects", projectpath.Sanitize(home)) + string(filepath.Separator)
	transcripts := result["transcripts"].(map[string]string)
	if len(transcripts) == 0 {
		t.Fatal("actual transcript writer produced no files")
	}
	for path := range transcripts {
		if !strings.HasPrefix(path, prefix) {
			t.Fatalf("resume and writer used different project paths: %q", path)
		}
	}
}
