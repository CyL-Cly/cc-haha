package server

import (
	"context"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryLaunchSamePhysicalTranscriptDoesNotDeleteOrCopyTitle(t *testing.T) {
	o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	sid := "11111111-1111-4111-8111-111111111111"
	cwd := filepath.Join(o.HomeDir, "relocated")
	_ = os.MkdirAll(cwd, 0700)
	old := filepath.Join(o.ConfigDir, "projects", "old", sid+".jsonl")
	target := filepath.Join(o.ConfigDir, "projects", projectpath.Sanitize(cwd), sid+".jsonl")
	for _, path := range []string{old, target} {
		_ = os.MkdirAll(filepath.Dir(path), 0700)
	}
	content := fmt.Sprintf("{\"type\":\"session-meta\",\"isMeta\":true,\"workDir\":%q}\n{\"type\":\"custom-title\",\"customTitle\":\"preserved title\"}\n", cwd)
	if err = os.WriteFile(old, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Link(old, target); err != nil {
		t.Skipf("filesystem has no hardlink support: %v", err)
	}
	info, err := s.scanSession(context.Background(), old)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.finalizeRepositorySessionLaunch(context.Background(), info, cwd, nil); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{old, target} {
		body, err := os.ReadFile(path)
		if err != nil || strings.Count(string(body), "preserved title") != 1 {
			t.Fatal("same-file placeholder was deleted or title duplicated", path, string(body), err)
		}
	}
	other := filepath.Join(o.HomeDir, "other")
	_ = os.WriteFile(other, []byte("x"), 0600)
	if !sameTranscriptFile(old, target) || sameTranscriptFile(old, other) || sameTranscriptFile(old, other+"-missing") {
		t.Fatal("physical identity conflates files")
	}
}
