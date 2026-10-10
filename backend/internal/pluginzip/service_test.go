package pluginzip

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestConvertPublishesBeforeRemovingSourceAndRollsBackTemp(t *testing.T) {
	for _, phase := range []string{"write", "rename", "remove-tree", "none"} {
		t.Run(phase, func(t *testing.T) {
			home := t.TempDir()
			source := filepath.Join(home, "source")
			seed(t, source, []seedItem{seededFile("actual", "new data", 0751)})
			target := filepath.Join(home, "mounted", "version.zip")
			_ = os.MkdirAll(filepath.Dir(target), 0700)
			_ = os.WriteFile(target, []byte("old cache"), 0600)
			cause := errors.New("controlled " + phase + " failure")
			operations := []string{}
			service := mustService(t, home, func(op Operation) error {
				if op.Kind == "write" || op.Kind == "rename" || op.Kind == "remove-tree" || op.Kind == "remove" {
					operations = append(operations, op.Kind)
				}
				if op.Kind == phase {
					return cause
				}
				return nil
			})
			err := service.Convert(context.Background(), source, target)
			if phase != "none" && !errors.Is(err, cause) {
				t.Fatalf("primary failure changed: %#v", err)
			}
			if phase == "none" && err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "write" || phase == "rename" {
				if string(data) != "old cache" {
					t.Fatal("failed publication changed old cache")
				}
				if _, err = os.Stat(source); err != nil {
					t.Fatal("source removed before publication")
				}
			} else {
				entries, err := Unzip(data, "darwin")
				if err != nil || len(entries) != 1 || string(entries[0].Data) != "new data" {
					t.Fatalf("new cache missing: %v", err)
				}
			}
			siblings, err := os.ReadDir(filepath.Dir(target))
			if err != nil {
				t.Fatal(err)
			}
			if len(siblings) != 1 || siblings[0].Name() != "version.zip" {
				t.Fatal("failed operation leaked temporary file")
			}
			var expected []string
			switch phase {
			case "write":
				expected = []string{"write", "remove"}
			case "rename":
				expected = []string{"write", "rename", "remove"}
			default:
				expected = []string{"write", "rename", "remove-tree"}
			}
			if !reflect.DeepEqual(operations, expected) {
				t.Fatalf("actual I/O order %#v expected %#v", operations, expected)
			}
		})
	}
}

func TestSessionPromiseLifecycleAndFailedCreation(t *testing.T) {
	home := t.TempDir()
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	service := mustService(t, home, func(op Operation) error {
		if op.Kind == "mkdir" {
			once.Do(func() { close(entered) })
			<-release
		}
		return nil
	})
	type result struct {
		path string
		err  error
	}
	results := make(chan result, 32)
	for i := 0; i < 32; i++ {
		go func() { path, err := service.SessionPath(context.Background()); results <- result{path, err} }()
	}
	<-entered
	if err := service.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(release)
	path := ""
	for i := 0; i < 32; i++ {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		if path == "" {
			path = r.path
		}
		if path != r.path {
			t.Fatal("concurrent creation allocated multiple roots")
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	service.options.Before = nil
	if err := service.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cleanup kept session root")
	}
	next, err := service.SessionPath(context.Background())
	if err != nil || next == path {
		t.Fatalf("recreation %s %v", next, err)
	}
	_ = service.Cleanup(context.Background())
	cause := errors.New("creation failed")
	calls := 0
	failed := mustService(t, home, func(op Operation) error { calls++; return cause })
	for i := 0; i < 2; i++ {
		path, err := failed.SessionPath(context.Background())
		if path != "" || !errors.Is(err, cause) {
			t.Fatalf("cached creation rejection: %s %v", path, err)
		}
	}
	if calls != 1 {
		t.Fatalf("rejected promise retried unexpectedly: %d", calls)
	}
	_ = failed.Cleanup(context.Background())
	if calls != 1 {
		t.Fatal("empty-path cleanup touched filesystem")
	}
	failed.Reset()
	_, _ = failed.SessionPath(context.Background())
	if calls != 2 {
		t.Fatal("reset didn't clear rejected promise")
	}
}

func TestCleanupFailureClearsCacheAndRandomFailureDoesNotWrite(t *testing.T) {
	home := t.TempDir()
	service := mustService(t, home, nil)
	path, err := service.SessionPath(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	service.options.Before = func(op Operation) error {
		if op.Kind == "remove-tree" {
			return errors.New("cleanup failed")
		}
		return nil
	}
	if err = service.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("controlled cleanup unexpectedly removed root")
	}
	service.options.Before = nil
	next, err := service.SessionPath(context.Background())
	if err != nil || next == path {
		t.Fatal("cleanup failure retained old promise")
	}
	_ = service.Cleanup(context.Background())
	_ = os.RemoveAll(path)
	failed, _ := New(Options{TempDir: filepath.Join(home, "tmp"), Random: func([]byte) (int, error) { return 0, errors.New("entropy failed") }})
	if path, err := failed.SessionPath(context.Background()); path != "" || err == nil {
		t.Fatal("random failure created session")
	}
	target := filepath.Join(home, "mounted", "owned.json")
	if err = failed.AtomicWrite(context.Background(), target, []byte("x")); err == nil {
		t.Fatal("random failure wrote target")
	}
	if _, err = os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("target exists")
	}
}

func TestExtractionValidatesWholeArchiveBeforeWritesAndChmodIsBestEffort(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "target")
	path := filepath.Join(home, "archive.zip")
	data := zipBytes(t, []Entry{{Name: "valid", Data: []byte("must not publish")}, {Name: "../bad", Data: []byte("bad")}}, zip.Store)
	_ = os.WriteFile(path, data, 0600)
	operations := []string{}
	service := mustService(t, home, func(op Operation) error { operations = append(operations, op.Kind); return nil })
	if err := service.Extract(context.Background(), path, root); err == nil {
		t.Fatal("unsafe archive accepted")
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial archive published before validation")
	}
	if !reflect.DeepEqual(operations, []string{"read"}) {
		t.Fatalf("premature writes: %#v", operations)
	}
	data = zipBytes(t, []Entry{{Name: "run", Data: []byte("actual"), Mode: 0100755}}, zip.Store)
	_ = os.WriteFile(path, data, 0600)
	service.options.Before = func(op Operation) error {
		if op.Kind == "chmod" {
			return errors.New("not supported")
		}
		return nil
	}
	if err := service.Extract(context.Background(), path, root); err != nil {
		t.Fatalf("best effort chmod aborted extraction: %v", err)
	}
	info, _ := os.Stat(filepath.Join(root, "run"))
	if info.Mode().Perm() != 0644 {
		t.Fatalf("unexpected default mode %o", info.Mode().Perm())
	}
}

func TestExtractPluginUsesSharedRootAndPreservesCorruptArchive(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, "source")
	seed(t, source, []seedItem{seededFile("actual", "retained", 0755)})
	service := mustService(t, home, nil)
	path := source + ".zip"
	if err := service.Convert(context.Background(), source, path); err != nil {
		t.Fatal(err)
	}
	target, err := service.ExtractPlugin(context.Background(), path, "office 🧠@owned")
	if err != nil {
		t.Fatal(err)
	}
	root, _ := service.SessionPath(context.Background())
	if target != filepath.Join(root, "office---@owned") {
		t.Fatalf("UTF16 sanitizer %s", target)
	}
	if _, err = os.Stat(filepath.Join(target, "actual")); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(home, "bad.zip")
	_ = os.WriteFile(bad, []byte("bad"), 0600)
	if _, err = service.ExtractPlugin(context.Background(), bad, "bad@owned"); err == nil {
		t.Fatal("corrupt archive accepted")
	}
	if _, err = os.Stat(bad); err != nil {
		t.Fatal("extractor deleted corrupt archive owned by caller policy")
	}
	_ = service.Cleanup(context.Background())
}

func TestCreateSkipsUnreadableFilesAndReadWrapperPreservesENOENT(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, "source")
	seed(t, source, []seedItem{seededFile("allowed", "keep", 0644), seededFile("blocked", "skip", 0644)})
	service := mustService(t, home, func(op Operation) error {
		if op.Kind == "read" && strings.HasSuffix(op.Path, "blocked") {
			return errors.New("read denied")
		}
		return nil
	})
	data, err := service.Create(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := Unzip(data, "darwin")
	if err != nil || len(entries) != 1 || entries[0].Name != "allowed" {
		t.Fatalf("skipped file: %#v %v", entries, err)
	}
	service.options.Before = nil
	_, err = service.ReadAndUnzip(context.Background(), filepath.Join(home, "missing.zip"))
	var io *IOError
	if !errors.As(err, &io) || io.Code != "ENOENT" || strings.HasPrefix(err.Error(), "Failed to") {
		t.Fatalf("ENOENT wrapping: %#v", err)
	}
	bad := filepath.Join(home, "bad.zip")
	_ = os.WriteFile(bad, []byte("bad"), 0600)
	_, err = service.ReadAndUnzip(context.Background(), bad)
	if err == nil || err.Error() != "Failed to read or unzip file: invalid zip data" {
		t.Fatalf("corrupt wrapping: %v", err)
	}
	data, err = service.Create(context.Background(), filepath.Join(home, "missing-source"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err = Unzip(data, "darwin")
	if err != nil || len(entries) != 0 {
		t.Fatal("missing source did not produce original empty archive")
	}
}

func TestExplicitCacheEnvironmentAndPathValidation(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("implicit temp/home accepted")
	}
	for _, path := range []string{`C:\absolute`, `C:/absolute`, `\absolute`, `/absolute`, `dir\..\bad`} {
		if IsPathSafe(path, "windows") {
			t.Fatalf("unsafe Windows path %s", path)
		}
	}
	if !IsPathSafe(`C:relative`, "windows") || !IsPathSafe(`C:\literal`, "darwin") {
		t.Fatal("platform path semantics changed")
	}
	state := ValidationState{CompressedSize: 1}
	if err := ValidateFile(Metadata{Name: "safe", OriginalSize: 0}, &state, "darwin"); err != nil {
		t.Fatal(err)
	}
}
