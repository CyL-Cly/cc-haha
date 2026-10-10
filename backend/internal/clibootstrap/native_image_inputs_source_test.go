package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/imagegeneration"
)

type parallelImageFiles struct {
	imagegeneration.NativeFiles
	mu                             sync.Mutex
	roots, paths                   []string
	rootCount, inputCount, reads   int
	rootGate, inputGate, firstGate chan struct{}
	readDone                       chan struct{}
	reject                         bool
	allRoots, allImages            bool
}

func (files *parallelImageFiles) Realpath(ctx context.Context, path string) (string, error) {
	files.mu.Lock()
	root, input := false, false
	for _, candidate := range files.roots {
		root = root || candidate == path
	}
	for _, candidate := range files.paths {
		input = input || candidate == path
	}
	if root {
		files.rootCount++
		if files.rootCount == 3 {
			files.allRoots = true
			close(files.rootGate)
		}
	} else if input {
		files.inputCount++
		if files.inputCount == 3 {
			files.allImages = true
			close(files.inputGate)
		}
	}
	files.mu.Unlock()
	if root {
		<-files.rootGate
	} else if input {
		<-files.inputGate
		if files.reject && path == files.paths[0] {
			<-files.firstGate
		}
	}
	return files.NativeFiles.Realpath(ctx, path)
}
func (files *parallelImageFiles) ReadNoFollow(ctx context.Context, path string) ([]byte, error) {
	data, err := files.NativeFiles.ReadNoFollow(ctx, path)
	files.mu.Lock()
	files.reads++
	want := 3
	if files.reject {
		want = 2
	}
	if files.reads == want {
		close(files.readDone)
	}
	files.mu.Unlock()
	return data, err
}

func TestActualImageInputPromiseAllStartsAllBeforeAwaitRejectsEarlyAndRetainsPhysicalReads(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-image-input-parallel.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_IMAGE_INPUT_PARALLEL_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	raw, err := command.Output()
	if err != nil {
		t.Fatal("actual image input Source", err, stderr.String())
	}
	var expected []map[string]any
	if err = json.Unmarshal(raw, &expected); err != nil || len(expected) != 2 {
		t.Fatal("Source image input output", err, string(raw))
	}
	for index, reject := range []bool{false, true} {
		m := openAIAuthMainFixture(t)
		root := m.options.Native.Home
		roots := []string{filepath.Join(root, "root0"), filepath.Join(root, "root1"), filepath.Join(root, "root2")}
		for _, path := range roots {
			if err := os.MkdirAll(path, 0700); err != nil {
				t.Fatal(err)
			}
		}
		paths := []string{filepath.Join(roots[0], "image0.png"), filepath.Join(roots[0], "image1.png"), filepath.Join(roots[0], "image2.png")}
		if reject {
			paths[1] = filepath.Join(root, "not-provided.png")
		}
		for _, path := range paths {
			if err := os.WriteFile(path, nativeImagePNG, 0600); err != nil {
				t.Fatal(err)
			}
		}
		files := &parallelImageFiles{NativeFiles: imagegeneration.NativeFiles{Check: m.options.Native.check}, roots: roots, paths: paths, rootGate: make(chan struct{}), inputGate: make(chan struct{}), firstGate: make(chan struct{}), readDone: make(chan struct{}), reject: reject}
		var images []imagegeneration.PreparedImage
		done := make(chan error, 1)
		go func() {
			done <- m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				var err error
				images, err = m.prepareNativeImageInputs(ctx, scope, paths, roots, files)
				return err
			})
		}()
		var failure error
		select {
		case failure = <-done:
		case <-ctx.Done():
			t.Fatal("image inputs were awaited before all original Promise.all admissions", ctx.Err())
		}
		rejectedBeforeRelease := reject && failure != nil
		close(files.firstGate)
		select {
		case <-files.readDone:
		case <-ctx.Done():
			t.Fatal("admitted image reads disappeared after aggregate failure", ctx.Err())
		}
		files.mu.Lock()
		actual := map[string]any{"reject": reject, "allRootsStarted": files.allRoots, "allImagesStarted": files.allImages, "rejectedBeforeRelease": rejectedBeforeRelease, "reads": float64(files.reads), "names": []string{}}
		files.mu.Unlock()
		if failure != nil {
			actual["error"] = strings.ReplaceAll(failure.Error(), root, "$HOME")
		}
		for _, image := range images {
			actual["names"] = append(actual["names"].([]string), image.FileName)
		}
		data, _ := json.Marshal(actual)
		var normalized any
		_ = json.Unmarshal(data, &normalized)
		if !reflect.DeepEqual(normalized, expected[index]) {
			t.Errorf("image input Promise.all row%d Go%s Source%s", index, data, raw)
		}
		if err := m.ShutdownSession(context.Background()); err != nil {
			t.Fatal("pending native file lifetimes not joined", err)
		}
	}
}
