package sessionrewind

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeCheckpointCapturePhysicalAtomicBeforeAndImmutableAfter(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	n := NativeFiles{ConfigHome: filepath.Join(root, "config")}
	p := filepath.Join(work, "file.txt")
	if err := os.WriteFile(p, []byte("before\n"), 0640); err != nil {
		t.Fatal(err)
	}
	seq := 0
	uuid := func() (string, error) { seq++; return "physical-temp-" + string(rune('a'+seq)), nil }
	before, e := n.CaptureBackup(context.Background(), "test-session", work, p, 1, "", uuid)
	if e != nil || before.Name == nil {
		t.Fatal(e, before)
	}
	hash := sha256.Sum256([]byte(p))
	if *before.Name != hex.EncodeToString(hash[:8])+"@v1" {
		t.Fatal(*before.Name)
	}
	if err := os.WriteFile(p, []byte("after\n"), 0640); err != nil {
		t.Fatal(err)
	}
	after, e := n.CaptureBackup(context.Background(), "test-session", work, p, 1, "completed-turn-unique", uuid)
	if e != nil || after.Name == nil || *before.Name == *after.Name {
		t.Fatal(e, after)
	}
	for _, sample := range []struct {
		name string
		data []byte
	}{{*before.Name, []byte("before\n")}, {*after.Name, []byte("after\n")}} {
		state, e := n.ReadBackupState(context.Background(), "test-session", sample.name)
		if e != nil || !state.Exists || !bytes.Equal(state.Content, sample.data) || state.Mode.Perm() != 0640 {
			t.Fatal(e, state)
		}
	}
	entries, e := os.ReadDir(filepath.Join(n.ConfigHome, "file-history", "test-session"))
	if e != nil || len(entries) != 2 {
		t.Fatal(e, entries)
	}
	missing, e := n.CaptureBackup(context.Background(), "test-session", work, filepath.Join(work, "missing"), 1, "", uuid)
	if e != nil || missing.Name != nil {
		t.Fatal(e, missing)
	}
}

func TestNativeCheckpointCaptureRefusesLinksEscapedParentAndLinkedDestination(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work")
	outside := filepath.Join(root, "outside")
	for _, dir := range []string{work, outside} {
		if e := os.Mkdir(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	p := filepath.Join(work, "original")
	if e := os.WriteFile(p, []byte("protected"), 0600); e != nil {
		t.Fatal(e)
	}
	n := NativeFiles{ConfigHome: filepath.Join(root, "config")}
	uuid := func() (string, error) { return "test-unique", nil }
	link := filepath.Join(work, "link")
	if e := os.Symlink(p, link); e != nil {
		t.Fatal(e)
	}
	if _, e := n.CaptureBackup(context.Background(), "session", work, link, 1, "", uuid); e == nil {
		t.Fatal("symlink was captured")
	}
	hard := filepath.Join(work, "hard")
	if e := os.Link(p, hard); e != nil {
		t.Fatal(e)
	}
	if _, e := n.CaptureBackup(context.Background(), "session", work, p, 1, "", uuid); e == nil {
		t.Fatal("hard-linked file was captured")
	}
	os.Remove(hard)
	if e := os.WriteFile(filepath.Join(outside, "file"), []byte("outside"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(work, "escape")); e != nil {
		t.Fatal(e)
	}
	if _, e := n.CaptureBackup(context.Background(), "session", work, filepath.Join(work, "escape/file"), 1, "", uuid); e == nil {
		t.Fatal("escaped project parent captured")
	}
	if e := os.MkdirAll(n.ConfigHome, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(n.ConfigHome, "file-history")); e != nil {
		t.Fatal(e)
	}
	if _, e := n.CaptureBackup(context.Background(), "session", work, p, 1, "", uuid); e == nil {
		t.Fatal("linked backup directory captured")
	}
	data, e := os.ReadFile(p)
	if e != nil || string(data) != "protected" {
		t.Fatal(e, string(data))
	}
}
