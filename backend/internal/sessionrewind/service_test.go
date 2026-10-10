package sessionrewind

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func writePrivate(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestNativeBackupSafetyAndMigratedHardLinks(t *testing.T) {
	root := t.TempDir()
	files := NativeFiles{ConfigHome: filepath.Join(root, "config")}
	backup := filepath.Join(files.ConfigHome, "file-history", "session", "backup")
	writePrivate(t, backup, "original\x00bytes")
	for _, name := range []string{"", ".", "..", "../secret", "nested/file", "nested\\file", "nul\x00name"} {
		if _, err := files.ReadBackup(context.Background(), "session", name); err == nil {
			t.Fatalf("unsafe backup %q admitted", name)
		}
	}
	for _, id := range []string{"", "..", "../session", "a/b", "a\\b"} {
		if _, err := files.ReadBackup(context.Background(), id, "backup"); err == nil {
			t.Fatalf("unsafe session %q admitted", id)
		}
	}
	other := filepath.Join(root, "migrated-original")
	if err := os.Link(backup, other); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(other)
	if err != nil {
		t.Fatal(err)
	}
	data, err := files.ReadBackup(context.Background(), "session", "backup")
	if err != nil || string(data) != "original\x00bytes" {
		t.Fatalf("migration read %q %v", data, err)
	}
	after, err := os.Stat(backup)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Fatal("migrated backup still aliases another session")
	}
	writePrivate(t, other, "tampered")
	data, err = files.ReadBackup(context.Background(), "session", "backup")
	if err != nil || string(data) != "original\x00bytes" {
		t.Fatalf("migrated backup bytes changed %q %v", data, err)
	}
	link := filepath.Join(filepath.Dir(backup), "link")
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	if _, err := files.ReadBackup(context.Background(), "session", "link"); err == nil {
		t.Fatal("backup symlink admitted")
	}
	if _, err := files.ReadBackup(context.Background(), "session", "missing"); !os.IsNotExist(err) {
		t.Fatalf("missing backup %v", err)
	}
	moved := filepath.Dir(backup) + "-saved"
	if err := os.Rename(filepath.Dir(backup), moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, filepath.Dir(backup)); err != nil {
		t.Fatal(err)
	}
	if _, err := files.ReadBackup(context.Background(), "session", "backup"); err == nil {
		t.Fatal("session directory symlink admitted")
	}
}
func TestNativeTrackedPathsKeepSystemAliasButRejectWorkspaceLinks(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "project")
	regular := filepath.Join(base, "file")
	writePrivate(t, regular, "text")
	files := NativeFiles{}
	ctx := context.Background()
	if !files.SafeTrackedPath(ctx, base, "file") || !files.SafeTrackedPath(ctx, base, "new/nested") {
		t.Fatal("safe existing/missing paths rejected")
	}
	if files.SafeTrackedPath(ctx, base, "../escape") {
		t.Fatal("relative traversal admitted")
	}
	outside := filepath.Join(root, "absolute-outside")
	writePrivate(t, outside, "outside")
	if !files.SafeTrackedPath(ctx, base, outside) {
		t.Fatal("Source supported absolute external path rejected")
	}
	alias := filepath.Join(root, "system-alias")
	if err := os.Symlink(base, alias); err != nil {
		t.Fatal(err)
	}
	if !files.SafeTrackedPath(ctx, alias, "file") {
		t.Fatal("alias above tracked root rejected")
	}
	if err := os.Symlink(outside, filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	if files.SafeTrackedPath(ctx, base, "link") {
		t.Fatal("leaf symlink admitted")
	}
	if err := os.Symlink(root, filepath.Join(base, "nested-link")); err != nil {
		t.Fatal(err)
	}
	if files.SafeTrackedPath(ctx, base, "nested-link/missing") {
		t.Fatal("nested symlink admitted")
	}
	if err := os.Link(regular, filepath.Join(base, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if files.SafeTrackedPath(ctx, base, "file") {
		t.Fatal("workspace hardlink admitted")
	}
	if _, _, err := files.ReadCurrent(ctx, regular); err == nil {
		t.Fatal("workspace hardlink readable as restorable state")
	}
	data, exists, err := files.ReadCurrent(ctx, filepath.Join(base, "missing"))
	if err != nil || exists || data != nil {
		t.Fatalf("missing file tri-state %q %v %v", data, exists, err)
	}
	if runtime.GOOS != "windows" {
		if _, _, err := files.ReadCurrent(ctx, filepath.Join(base, "link")); err == nil {
			t.Fatal("nofollow read admitted leaf link")
		}
	}
}
func TestDirectoryIdentityReplacementAndCancellation(t *testing.T) {
	root := t.TempDir()
	files := NativeFiles{ConfigHome: root}
	path := filepath.Join(root, "file-history", "session", "backup")
	writePrivate(t, path, "bytes")
	dirs, err := files.backupDirectories("session")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(filepath.Dir(path), filepath.Dir(path)+"-old"); err != nil {
		t.Fatal(err)
	}
	writePrivate(t, path, "new")
	if !errors.Is(unchangedDirectories(dirs), ErrUnsafeCheckpointFile) {
		t.Fatal("changed parent identity admitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = files.ReadBackup(ctx, "session", "backup"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if files.SafeTrackedPath(ctx, root, path) {
		t.Fatal("canceled safe path inspected")
	}
	if _, _, err = files.ReadCurrent(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	input := rewindFixture{Home: root, Messages: []protocol.JSONObject{fixtureMessage("u", "user", root, "prompt", ""), fixtureMessage("a", "assistant", root, "done", "")}, Complete: true}
	service := fixtureService(t, input)
	if _, err = service.List(ctx, "session", false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	service.options.Snapshots = func(context.Context, string) (SnapshotEvidence, error) {
		return SnapshotEvidence{Rows: nil, Complete: false}, nil
	}
	if _, err = service.List(context.Background(), "session", false); !errors.Is(err, ErrSnapshotEvidenceIncomplete) {
		t.Fatalf("truncated snapshot evidence admitted %v", err)
	}
}
func TestSnapshotRawPresenceAliasAndLatestRecord(t *testing.T) {
	rows := []json.RawMessage{fixtureRow("u", map[string]any{"a": fixtureBackup("first")}, nil, false), fixtureRow("u", map[string]any{"a": fixtureBackup("latest")}, nil, true), json.RawMessage(`{"type":"file-history-snapshot","messageId":"bad-fallback","snapshot":{"messageId":"","trackedFileBackups":{}}}`)}
	values := loadSnapshots(rows)
	if len(values) != 1 || !values["u"].completionPresent || text(object(values["u"].before.Get("a")), "backupFileName") != "latest" {
		t.Fatalf("raw snapshot semantics %+v", values)
	}
	for _, pair := range [][2]string{{`"a"`, `"\u0061"`}, {`1`, `1.0`}, {`9007199254740992`, `9007199254740993`}} {
		if primitiveKey(json.RawMessage(pair[0])) != primitiveKey(json.RawMessage(pair[1])) {
			t.Fatalf("JS primitive alias changed %v", pair)
		}
	}
	if reflect.DeepEqual(checkpointText(fileBytes{true, false, nil}, true), checkpointText(fileBytes{true, true, []byte{}}, true)) {
		t.Fatal("empty file and absence collapsed")
	}
}
func TestDiffLineTokensAndExactReplay(t *testing.T) {
	for _, tc := range []struct {
		before, after string
		add, del      int
	}{{"a\r\nb\r\n", "a\nb\n", 2, 2}, {"a\n", "a", 1, 1}, {"", "\n", 1, 0}, {"a\nb\na\n", "a\na\nb\n", 1, 1}, {"中文😀\n", "中文😀\n", 0, 0}} {
		add, del := countDiffLines(context.Background(), tc.before, tc.after)
		if add != tc.add || del != tc.del {
			t.Fatalf("%q→%q %d/%d", tc.before, tc.after, add, del)
		}
	}
	if value := applyLiteralEdit("old\nnext\n", "old", "", false); value != "next\n" {
		t.Fatal(value)
	}
	if value := applyLiteralEdit("old", "old", "$&$1", false); value != "$&$1" {
		t.Fatal(value)
	}
	sources := normalizedSources([]string{"😀", "中文", "a", "b", "c", "d", "e", "f", "g", "a"})
	if len(sources) != 8 || sources[0] != "a" || sources[7] != "中文" {
		t.Fatalf("unverified sort/limit %v", sources)
	}
}
