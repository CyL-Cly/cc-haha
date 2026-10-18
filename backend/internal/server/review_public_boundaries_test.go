package server

// Public HTTP regressions with disposable Git repositories and real filesystem
// failures. The shared fixture aborts if Review tries to open an Agent. None of
// these tests substitutes Git output, calls a private Review parser, or uses a
// provider. Expected responses follow the original TypeScript Review service.
import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGitReviewPublicRejectsMalformedWritesWithoutMutation(t *testing.T) {
	f := newReviewFixture(t, true)
	f.put("tracked.txt", "owned pending change\n")
	status := f.status("unstaged")
	index := f.git("ls-files", "--stage")
	snapshot, err := json.Marshal(status.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, example := range []struct {
		name, resource, body, message string
	}{
		{"malformed-json", "stage", `{`, "Invalid JSON body"},
		{"null-body", "stage", `null`, "Request body must be an object"},
		{"numeric-body", "stage", `1`, "Request body must be an object"},
		{"missing-snapshot", "stage", `{"paths":["tracked.txt"]}`, "snapshot is required"},
		{"numeric-snapshot", "stage", `{"snapshot":1,"paths":["tracked.txt"]}`, "snapshot is required"},
		{"null-source", "stage", fmt.Sprintf(`{"snapshot":%s,"source":null,"paths":["tracked.txt"]}`, snapshot), "source is required"},
		{"array-source", "stage", fmt.Sprintf(`{"snapshot":%s,"source":[],"paths":["tracked.txt"]}`, snapshot), "source is required"},
		{"numeric-path", "stage", fmt.Sprintf(`{"snapshot":%s,"paths":["tracked.txt",1]}`, snapshot), "paths must contain non-empty strings"},
		{"blank-path", "revert", fmt.Sprintf(`{"snapshot":%s,"paths":["tracked.txt"," \t"]}`, snapshot), "paths must contain non-empty strings"},
		{"missing-paths", "stage", fmt.Sprintf(`{"snapshot":%s}`, snapshot), "paths must be a non-empty array"},
		{"blank-hunk", "stage-hunk", fmt.Sprintf(`{"snapshot":%s,"patch":" \n"}`, snapshot), "patch is required"},
	} {
		t.Run(example.name, func(t *testing.T) {
			raw := requestJSON(t, f.host, "POST", f.base+"/"+example.resource, example.body, 400)
			var response map[string]json.RawMessage
			if err := json.Unmarshal(raw, &response); err != nil || stringField(response, "message") != example.message {
				t.Fatalf("expected %q: %s (%v)", example.message, raw, err)
			}
			if f.git("ls-files", "--stage") != index || reviewMustRead(t, filepath.Join(f.options.WorkDir, "tracked.txt")) != "owned pending change\n" {
				t.Fatal("invalid request changed index or working tree")
			}
		})
	}
	if after := f.status("unstaged"); after.State != "ok" || after.Snapshot != status.Snapshot {
		t.Fatalf("invalid requests changed the review snapshot: %+v", after)
	}
	refused := f.write("unstage", map[string]any{"paths": []string{"tracked.txt"}, "snapshot": status.Snapshot, "source": reviewTestSource{Kind: "unstaged"}})
	if refused.State != "error" || refused.Error != "Unstaged comparison only supports staging or discarding" || len(refused.Results) != 0 || f.git("ls-files", "--stage") != index {
		t.Fatalf("operation did not match its displayed comparison: %+v", refused)
	}
}

func TestGitReviewPublicIndexLockRefusesStageAndUnstage(t *testing.T) {
	for _, action := range []string{"stage", "unstage"} {
		t.Run(action, func(t *testing.T) {
			f := newReviewFixture(t, true)
			f.put("tracked.txt", "owned staged candidate\n")
			source := "unstaged"
			if action == "unstage" {
				f.git("add", "--", "tracked.txt")
				f.put("tracked.txt", "owned unstaged tail\n")
				source = "staged"
			}
			status := f.status(source)
			indexPath := strings.TrimSpace(f.git("rev-parse", "--git-path", "index"))
			if !filepath.IsAbs(indexPath) {
				indexPath = filepath.Join(f.options.WorkDir, indexPath)
			}
			before := reviewMustRead(t, indexPath)
			working := reviewMustRead(t, filepath.Join(f.options.WorkDir, "tracked.txt"))
			lockPath := indexPath + ".lock"
			if err := os.WriteFile(lockPath, []byte("owned lock: another Git operation\n"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
					t.Errorf("remove owned index lock: %v", err)
				}
			})
			result := f.write(action, map[string]any{"paths": []string{"tracked.txt"}, "snapshot": status.Snapshot, "source": reviewTestSource{Kind: source}})
			prefix := "Failed to " + action + " path"
			if result.State != "error" || len(result.Results) != 1 || result.Results[0].OK || !strings.Contains(result.Results[0].Error, prefix) || !strings.Contains(result.Results[0].Error, "index.lock") {
				t.Fatalf("real Git lock failure was not exposed: %+v", result)
			}
			if reviewMustRead(t, indexPath) != before || reviewMustRead(t, filepath.Join(f.options.WorkDir, "tracked.txt")) != working || reviewMustRead(t, lockPath) != "owned lock: another Git operation\n" {
				t.Fatal("failed action changed index, working tree, or another operation's lock")
			}
			if err := os.Remove(lockPath); err != nil {
				t.Fatal(err)
			}
			fresh := f.status(source)
			positive := f.write(action, map[string]any{"paths": []string{"tracked.txt"}, "snapshot": fresh.Snapshot, "source": reviewTestSource{Kind: source}})
			if positive.State != "ok" || len(positive.Results) != 1 || !positive.Results[0].OK || reviewMustRead(t, filepath.Join(f.options.WorkDir, "tracked.txt")) != working {
				t.Fatalf("removing the owned lock did not restore the actual action: %+v", positive)
			}
			if action == "stage" && f.git("show", ":tracked.txt") != working {
				t.Fatal("unlocked stage did not put the exact working bytes in the index")
			}
			if action == "unstage" && f.git("diff", "--cached", "--", "tracked.txt") != "" {
				t.Fatal("unlocked unstage did not restore the actual index to HEAD")
			}
		})
	}
}

func TestGitReviewPublicBackupFailurePreventsTrackedAndUntrackedLoss(t *testing.T) {
	f := newReviewFixture(t, true)
	f.put("tracked.txt", "owned irreplaceable tracked edit\n")
	f.put("fresh.txt", "owned irreplaceable untracked bytes\n")
	blocked := filepath.Join(f.options.ConfigDir, "cc-haha", "review-backups")
	if err := os.MkdirAll(filepath.Dir(blocked), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocked, []byte("owned regular file blocks backup directory\n"), 0600); err != nil {
		t.Fatal(err)
	}
	status := f.status("unstaged")
	index := f.git("ls-files", "--stage")
	result := f.write("revert", map[string]any{"paths": []string{"tracked.txt", "fresh.txt"}, "snapshot": status.Snapshot, "source": reviewTestSource{Kind: "unstaged"}})
	if result.State != "error" || len(result.Results) != 2 || result.BackupDir != "" || len(result.DeletedPaths) != 0 || len(result.RevertedPaths) != 0 {
		t.Fatalf("backup failure was not all-path refusal: %+v", result)
	}
	for _, item := range result.Results {
		if item.OK || !strings.Contains(item.Error, "Failed to back up file before revert") {
			t.Fatalf("revert proceeded without its backup: %+v", item)
		}
	}
	if reviewMustRead(t, filepath.Join(f.options.WorkDir, "tracked.txt")) != "owned irreplaceable tracked edit\n" || reviewMustRead(t, filepath.Join(f.options.WorkDir, "fresh.txt")) != "owned irreplaceable untracked bytes\n" || reviewMustRead(t, blocked) != "owned regular file blocks backup directory\n" || f.git("ls-files", "--stage") != index {
		t.Fatal("backup refusal lost original bytes or changed index/config sentinel")
	}
}

func TestGitReviewPublicPartialRevertKeepsRecoverableBackupAndMissingPathError(t *testing.T) {
	f := newReviewFixture(t, true)
	f.put("tracked.txt", "owned changed content before discard\n")
	status := f.status("unstaged")
	index := f.git("ls-files", "--stage")
	head := f.git("rev-parse", "HEAD")
	result := f.write("revert", map[string]any{"paths": []string{"tracked.txt", "never-tracked/deep/missing.txt"}, "snapshot": status.Snapshot, "source": reviewTestSource{Kind: "unstaged"}})
	if result.State != "partial" || len(result.Results) != 2 || !result.Results[0].OK || result.Results[0].Action != "reverted" || result.Results[1].OK || !strings.Contains(result.Results[1].Error, "Failed to revert path") || result.BackupDir == "" || len(result.RevertedPaths) != 1 || result.RevertedPaths[0] != "tracked.txt" || len(result.DeletedPaths) != 0 {
		t.Fatalf("per-path Git failure lost partial result: %+v", result)
	}
	if reviewMustRead(t, filepath.Join(f.options.WorkDir, "tracked.txt")) != "one\ntwo\n" || reviewMustRead(t, filepath.Join(result.BackupDir, "tracked.txt")) != "owned changed content before discard\n" || f.git("ls-files", "--stage") != index || f.git("rev-parse", "HEAD") != head {
		t.Fatal("successful discard was not recoverable or changed the committed/index state")
	}
	if _, err := os.Lstat(filepath.Join(f.options.WorkDir, "never-tracked")); !os.IsNotExist(err) {
		t.Fatalf("failed restore created a missing path: %v", err)
	}
	if result.Status == nil || result.Status.State != "ok" || len(result.Status.Files) != 0 || result.Snapshot != result.Status.Snapshot {
		t.Fatalf("partial write did not refresh the displayed source: %+v", result)
	}
}

func TestGitReviewPublicUnreadableBackupSourcePreservesUntrackedFile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix mode permissions enforced for a non-root process")
	}
	f := newReviewFixture(t, true)
	f.put("fresh.txt", "owned recoverable-only-through-backup bytes\n")
	status := f.status("unstaged")
	index := f.git("ls-files", "--stage")
	path := filepath.Join(f.options.WorkDir, "fresh.txt")
	t.Cleanup(func() {
		if err := os.Chmod(path, 0644); err != nil && !os.IsNotExist(err) {
			t.Errorf("restore private fixture mode: %v", err)
		}
	})
	if err := os.Chmod(path, 0000); err != nil {
		t.Fatal(err)
	}
	if input, err := os.Open(path); err == nil {
		_ = input.Close()
		t.Skip("fixture filesystem does not enforce chmod000 read denial")
	} else if !os.IsPermission(err) {
		t.Fatalf("expected physical read denial, got %v", err)
	}
	result := f.write("revert", map[string]any{"paths": []string{"fresh.txt"}, "snapshot": status.Snapshot, "source": reviewTestSource{Kind: "unstaged"}})
	if result.State != "error" || len(result.Results) != 1 || result.Results[0].OK || !strings.Contains(result.Results[0].Error, "Failed to back up file before revert") || result.BackupDir != "" || len(result.DeletedPaths) != 0 {
		t.Fatalf("unreadable untracked content was discarded without backup: %+v", result)
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0000 {
		t.Fatalf("failed backup changed file existence/type/mode: %v %v", info, err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if reviewMustRead(t, path) != "owned recoverable-only-through-backup bytes\n" || f.git("ls-files", "--stage") != index {
		t.Fatal("failed backup lost bytes or changed the index")
	}
}

func TestGitReviewPublicCorruptIndexIsErrorInsteadOfEmptySuccess(t *testing.T) {
	f := newReviewFixture(t, true)
	f.put("tracked.txt", "owned pending edit\n")
	diff := f.diff("unstaged", "tracked.txt")
	if diff.State != "ok" || diff.Diff == nil {
		t.Fatal(diff)
	}
	indexPath := strings.TrimSpace(f.git("rev-parse", "--git-path", "index"))
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(f.options.WorkDir, indexPath)
	}
	original := reviewMustRead(t, indexPath)
	t.Cleanup(func() {
		if err := os.WriteFile(indexPath, []byte(original), 0644); err != nil {
			t.Errorf("restore private fixture index: %v", err)
		}
	})
	corrupt := "owned invalid Git index\n"
	if err := os.WriteFile(indexPath, []byte(corrupt), 0644); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"?source=unstaged", "/revision?source=unstaged", "/diff?source=unstaged&path=tracked.txt"} {
		raw := f.request("GET", suffix, nil, 200)
		var response map[string]json.RawMessage
		if err := json.Unmarshal(raw, &response); err != nil || stringField(response, "state") != "error" || !strings.Contains(stringField(response, "error"), "Failed to read git status") || stringField(response, "snapshot") != "" {
			t.Fatalf("corrupt index became a valid/empty comparison: %s (%v)", raw, err)
		}
		if response["diff"] != nil || response["binary"] != nil {
			t.Fatal("failed comparison unexpectedly carries a patch", string(raw))
		}
	}
	for _, action := range []string{"stage", "stage-hunk"} {
		result := f.write(action, map[string]any{"paths": []string{"tracked.txt"}, "patch": *diff.Diff, "snapshot": diff.Snapshot})
		if result.State != "error" || len(result.Results) != 0 || !strings.Contains(result.Error, "Failed to read git status") {
			t.Fatalf("corrupt-index write was not refused before mutation: %+v", result)
		}
	}
	if reviewMustRead(t, indexPath) != corrupt || reviewMustRead(t, filepath.Join(f.options.WorkDir, "tracked.txt")) != "owned pending edit\n" {
		t.Fatal("an error response rewrote the corrupted index or user edit")
	}
	if err := os.WriteFile(indexPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	if restored := f.status("unstaged"); restored.State != "ok" || len(restored.Files) != 1 {
		t.Fatalf("restored real index did not recover Review: %+v", restored)
	}
}

func TestGitReviewPublicHunkFreshnessAndActualApplyFailure(t *testing.T) {
	f := newReviewFixture(t, true)
	f.put("tracked.txt", "one\ntwo\nowned third line\n")
	old := f.diff("unstaged", "tracked.txt")
	if old.State != "ok" || old.Diff == nil {
		t.Fatal(old)
	}
	f.put("tracked.txt", "owned replacement with different size\n")
	index := f.git("ls-files", "--stage")
	stale := f.write("stage-hunk", map[string]any{"patch": *old.Diff, "snapshot": old.Snapshot, "source": reviewTestSource{Kind: "unstaged"}})
	if stale.State != "stale" || len(stale.Results) != 0 || stale.Snapshot == "" || stale.Snapshot == old.Snapshot || !strings.Contains(stale.Error, "nothing was applied") || f.git("ls-files", "--stage") != index {
		t.Fatalf("stale displayed patch mutated the index: %+v", stale)
	}
	// The original patch is valid, but the real index now has incompatible
	// contents. A fresh snapshot must not turn git-apply failure into success.
	f.git("add", "--", "tracked.txt")
	f.put("tracked.txt", "owned replacement with different size\nnew pending line\n")
	fresh := f.status("unstaged")
	index = f.git("ls-files", "--stage")
	failed := f.write("stage-hunk", map[string]any{"patch": *old.Diff, "snapshot": fresh.Snapshot, "source": reviewTestSource{Kind: "unstaged"}})
	if failed.State != "error" || len(failed.Results) != 1 || failed.Results[0].OK || !strings.Contains(failed.Results[0].Error, "Failed to stage hunk") || f.git("ls-files", "--stage") != index || reviewMustRead(t, filepath.Join(f.options.WorkDir, "tracked.txt")) != "owned replacement with different size\nnew pending line\n" {
		t.Fatalf("actual rejected patch changed source bytes/index: %+v", failed)
	}
	valid := f.diff("unstaged", "tracked.txt")
	if valid.State != "ok" || valid.Diff == nil {
		t.Fatal(valid)
	}
	// HTTP accepts a valid unified patch without the final transport newline.
	accepted := f.write("stage-hunk", map[string]any{"patch": strings.TrimSuffix(*valid.Diff, "\n"), "snapshot": valid.Snapshot, "source": reviewTestSource{Kind: "unstaged"}})
	if accepted.State != "ok" || f.git("show", ":tracked.txt") != reviewMustRead(t, filepath.Join(f.options.WorkDir, "tracked.txt")) {
		t.Fatalf("valid displayed hunk lost its final line: %+v", accepted)
	}
}

func TestGitReviewPublicHunkTemporaryDirectoryFailureLeavesIndexUntouched(t *testing.T) {
	f := newReviewFixture(t, true)
	f.put("tracked.txt", "one\ntwo\nowned pending hunk\n")
	diff := f.diff("unstaged", "tracked.txt")
	if diff.State != "ok" || diff.Diff == nil {
		t.Fatal(diff)
	}
	index := f.git("ls-files", "--stage")
	blocked := filepath.Join(f.options.HomeDir, "not-a-temp-directory")
	if err := os.WriteFile(blocked, []byte("owned temp-root obstruction\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// This is a real ENOTDIR boundary, independent of root/Unix permissions.
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, blocked)
	}
	raw := f.request("POST", "/stage-hunk", map[string]any{"patch": *diff.Diff, "snapshot": diff.Snapshot, "source": reviewTestSource{Kind: "unstaged"}}, 500)
	var response map[string]json.RawMessage
	if err := json.Unmarshal(raw, &response); err != nil || stringField(response, "message") == "" {
		t.Fatalf("temporary filesystem failure disappeared: %s (%v)", raw, err)
	}
	if f.git("ls-files", "--stage") != index || reviewMustRead(t, filepath.Join(f.options.WorkDir, "tracked.txt")) != "one\ntwo\nowned pending hunk\n" || reviewMustRead(t, blocked) != "owned temp-root obstruction\n" {
		t.Fatal("failed patch setup changed the index, worktree, or obstructing file")
	}
}

func TestGitReviewPublicNestedRenameDiffAndPathRefusal(t *testing.T) {
	f := newReviewFixture(t, true)
	f.put("scope/old.txt", "owned inside baseline\n")
	f.put("outside.txt", "owned outside baseline\n")
	f.git("add", "--", "scope/old.txt", "outside.txt")
	f.git("commit", "-q", "-m", "private scope baseline")
	f.git("mv", "--", "scope/old.txt", "scope/new.txt")
	f.put("outside.txt", "owned outside edit\n")
	f.useWorkspace(filepath.Join(f.options.WorkDir, "scope"))
	status := f.status("uncommitted")
	if status.State != "ok" || len(status.Files) != 1 || status.Files[0].Path != "new.txt" || status.Files[0].OldPath != "old.txt" || status.Files[0].Status != "renamed" {
		t.Fatalf("nested comparison leaked an outside file or lost rename: %+v", status)
	}
	raw := f.request("GET", "/diff?source=uncommitted&path=new.txt&oldPath=old.txt", nil, 200)
	var diff struct {
		reviewTestDiff
		OldPath string `json:"oldPath"`
	}
	if err := json.Unmarshal(raw, &diff); err != nil || diff.State != "ok" || diff.OldPath != "old.txt" || diff.Diff == nil || !strings.Contains(*diff.Diff, "rename from scope/old.txt") || !strings.Contains(*diff.Diff, "rename to scope/new.txt") {
		t.Fatalf("explicit paired rename diff was not preserved: %s (%v)", raw, err)
	}
	for _, example := range []struct {
		query, message string
		code           int
	}{
		{"/diff?source=unstaged&path=" + url.QueryEscape(" \t"), "path is required", 400},
		{"/diff?source=unstaged&path=.", "", 500},
		{"/diff?source=unstaged&path=new.txt&oldPath=" + url.QueryEscape("../outside.txt"), "Path is outside workspace", 403},
	} {
		raw := f.request("GET", example.query, nil, example.code)
		var response map[string]json.RawMessage
		if err := json.Unmarshal(raw, &response); err != nil || stringField(response, "message") == "" || !strings.Contains(stringField(response, "message"), example.message) {
			t.Fatalf("wrong public path refusal: %s (%v)", raw, err)
		}
	}
	if reviewMustRead(t, filepath.Join(f.options.WorkDir, "outside.txt")) != "owned outside edit\n" {
		t.Fatal("nested comparison changed the outside working tree")
	}
}

func TestGitReviewPublicUntrackedBinaryLineEndingsAndQuotedPaths(t *testing.T) {
	for _, example := range []struct {
		name, content string
		additions     int
		binary        bool
	}{
		{"quote\"name.txt", "unterminated text", 1, false},
		{"backslash\\name.txt", "one\rtwo\r", 2, false},
		{"tab\tname.txt", "one\r\ntwo\nlast", 3, false},
		{"carriage\rname.txt", "one\n", 1, false},
		{"control\b\f\x01name.txt", "one\n", 1, false},
		{"nul.bin", "owned\x00binary", 0, true},
		{"invalid-utf8.bin", "owned\xffbinary", 1, true},
	} {
		t.Run(example.name, func(t *testing.T) {
			if runtime.GOOS == "windows" && strings.ContainsAny(example.name, "\"\\\t\r\b\f\x01") {
				t.Skip("filename is not supported by Windows")
			}
			f := newReviewFixture(t, true)
			f.put(example.name, example.content)
			status := f.status("unstaged")
			file, present := reviewTestFileMap(status)[example.name]
			if status.State != "ok" || !present || file.Status != "untracked" || file.Additions != example.additions {
				t.Fatalf("lost exact untracked path/line count: %+v", status)
			}
			diff := f.diff("unstaged", example.name)
			if diff.State != "ok" || diff.Diff == nil || diff.Binary == nil || *diff.Binary != example.binary {
				t.Fatalf("wrong text/binary public diff: %+v", diff)
			}
			if example.binary {
				if !strings.Contains(*diff.Diff, "Binary files /dev/null") || strings.Contains(*diff.Diff, "@@") {
					t.Fatal("binary payload became a text patch", *diff.Diff)
				}
				return
			}
			result := f.write("stage-hunk", map[string]any{"patch": *diff.Diff, "snapshot": diff.Snapshot, "source": reviewTestSource{Kind: "unstaged"}})
			if result.State != "ok" || f.git("show", ":"+example.name) != example.content || reviewMustRead(t, filepath.Join(f.options.WorkDir, example.name)) != example.content {
				t.Fatalf("quoted-path patch changed bytes or staged another path: %+v", result)
			}
		})
	}
}

func TestGitReviewPublicDeletedAndTypeChangedFiles(t *testing.T) {
	t.Run("deleted-file-can-be-restored-from-index", func(t *testing.T) {
		f := newReviewFixture(t, true)
		path := filepath.Join(f.options.WorkDir, "tracked.txt")
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		status := f.status("unstaged")
		file := reviewTestFileMap(status)["tracked.txt"]
		if status.State != "ok" || file.Status != "deleted" || file.Deletions != 2 {
			t.Fatalf("deleted tracked file disappeared from Review: %+v", status)
		}
		diff := f.diff("unstaged", "tracked.txt")
		if diff.State != "ok" || diff.Diff == nil || !strings.Contains(*diff.Diff, "deleted file mode") || !strings.Contains(*diff.Diff, "-one\n-two\n") {
			t.Fatalf("deleted file did not keep its reviewable contents: %+v", diff)
		}
		result := f.write("revert", map[string]any{"paths": []string{"tracked.txt"}, "snapshot": status.Snapshot, "source": reviewTestSource{Kind: "unstaged"}})
		if result.State != "ok" || len(result.Results) != 1 || !result.Results[0].OK || result.BackupDir != "" || reviewMustRead(t, path) != "one\ntwo\n" {
			t.Fatalf("missing tracked file was not restored from the actual index: %+v", result)
		}
	})
	t.Run("symlink-type-change-is-visible-but-not-followed", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("requires Unix symlink creation without elevated privileges")
		}
		f := newReviewFixture(t, true)
		path := filepath.Join(f.options.WorkDir, "tracked.txt")
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("owned-absent-target.txt", path); err != nil {
			t.Fatal(err)
		}
		status := f.status("unstaged")
		if status.State != "ok" || reviewTestFileMap(status)["tracked.txt"].Status != "type_changed" {
			t.Fatalf("type change disappeared from the actual Git status: %+v", status)
		}
		f.request("GET", "/diff?source=unstaged&path=tracked.txt", nil, 500)
		if target, err := os.Readlink(path); err != nil || target != "owned-absent-target.txt" {
			t.Fatalf("Review followed or rewrote the symlink: %q %v", target, err)
		}
		if _, err := os.Lstat(filepath.Join(f.options.WorkDir, "owned-absent-target.txt")); !os.IsNotExist(err) {
			t.Fatalf("Review created the symlink target: %v", err)
		}
	})
}

func TestGitReviewPublicUnrelatedHistoryAndMissingWorktreeStates(t *testing.T) {
	f := newReviewFixture(t, true)
	f.git("checkout", "--orphan", "disconnected")
	f.git("rm", "-q", "-f", "-r", "--", ".")
	f.put("unrelated.txt", "owned unrelated history\n")
	f.git("add", "--", "unrelated.txt")
	f.git("commit", "-q", "-m", "private disconnected root")
	f.git("checkout", "fixture")
	for _, suffix := range []string{"?source=branch&baseRef=disconnected", "/revision?source=branch&baseRef=disconnected", "/diff?source=branch&baseRef=disconnected&path=tracked.txt"} {
		raw := f.request("GET", suffix, nil, 200)
		var response map[string]json.RawMessage
		if err := json.Unmarshal(raw, &response); err != nil || stringField(response, "state") != "error" || !strings.Contains(stringField(response, "error"), "No merge base between HEAD and disconnected") {
			t.Fatalf("unrelated history silently became a valid comparison: %s (%v)", raw, err)
		}
	}
	if invalid := f.status("commit&commit=" + url.QueryEscape("--owned-invalid-option")); invalid.State != "error" || !strings.Contains(invalid.Error, "Invalid commit") {
		t.Fatalf("commit option was treated as a revision: %+v", invalid)
	}
	if missing := f.diff("unstaged", "missing/deep/file.txt"); missing.State != "missing" || missing.Diff != nil || missing.Binary != nil {
		t.Fatalf("missing file became a successful empty patch: %+v", missing)
	}
	missingWorktree := filepath.Join(f.options.HomeDir, "not-present-workspace")
	if err := os.Mkdir(missingWorktree, 0700); err != nil {
		t.Fatal(err)
	}
	f.useWorkspace(missingWorktree)
	if err := os.Remove(missingWorktree); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"?source=unstaged", "/revision?source=unstaged", "/diff?source=unstaged&path=tracked.txt"} {
		raw := f.request("GET", suffix, nil, 200)
		var response map[string]json.RawMessage
		if err := json.Unmarshal(raw, &response); err != nil || stringField(response, "state") != "missing_workdir" || stringField(response, "error") == "" || stringField(response, "snapshot") != "" {
			t.Fatalf("missing workspace became an available comparison: %s (%v)", raw, err)
		}
		if response["diff"] != nil || response["binary"] != nil {
			t.Fatal("missing workspace unexpectedly carries diff fields", string(raw))
		}
	}
	result := f.write("stage", map[string]any{"paths": []string{"tracked.txt"}, "snapshot": "old-client-snapshot"})
	if result.State != "missing_workdir" || result.Error == "" || len(result.Results) != 0 {
		t.Fatalf("missing workspace write was not refused: %+v", result)
	}
	if _, err := os.Lstat(missingWorktree); !os.IsNotExist(err) {
		t.Fatalf("review recreated a missing workspace: %v", err)
	}
}
