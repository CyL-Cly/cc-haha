package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceReconciliationBoundsLargeDirectoryAndIgnoresReadAccess(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 513; i++ {
		if e := os.WriteFile(filepath.Join(root, fmt.Sprintf("%04d.txt", i)), []byte("old"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	scan := &workspaceWatchScan{path: root}
	defer scan.close()
	if state, e := scan.readChunk(context.Background()); e != nil || state != nil || scan.count != 256 {
		t.Fatal("first chunk not bounded", state, e, scan.count)
	}
	var initial *workspaceWatchState
	for initial == nil {
		var e error
		initial, e = scan.readChunk(context.Background())
		if e != nil {
			t.Fatal(e)
		}
	}
	if !initial.large || len(initial.entries) != 0 || scan.handle != nil {
		t.Fatal("large scan retained unbounded entries/handle", initial)
	}
	_, _ = os.ReadFile(filepath.Join(root, "0000.txt"))
	unchanged, e := readWorkspaceWatchState(context.Background(), root)
	if e != nil || initial.fingerprint != unchanged.fingerprint {
		t.Fatal("read access caused dirty event", e)
	}
	if e = os.WriteFile(filepath.Join(root, "0512.txt"), []byte("new"), 0600); e != nil {
		t.Fatal(e)
	}
	after, e := readWorkspaceWatchState(context.Background(), root)
	if e != nil || initial.fingerprint == after.fingerprint || len(changedWorkspaceWatchEntries(initial, after)) != 0 {
		t.Fatal("last entry change lost or coarse scan unbounded", e)
	}
}
func TestWorkspaceWatchReconciliationRestoresReplacedNativeDirectory(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Fatal("watch opened Agent")
		return nil, nil
	})
	_, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	nested := filepath.Join(options.WorkDir, "nested")
	if e := os.Mkdir(nested, 0700); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", host.URL+"/api/sessions/"+id+"/workspace/watch?path=nested", nil)
	reply, e := host.Client().Do(request)
	if e != nil {
		t.Fatal(e)
	}
	defer reply.Body.Close()
	frames := readWorkspaceWatchFrames(reply.Body)
	requireWorkspaceWatchFrame(t, frames, "ready")
	if e = os.Rename(nested, nested+"-old"); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(nested, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(nested, "fresh.txt"), []byte("restored"), 0600); e != nil {
		t.Fatal(e)
	}
	observed := false
	for !observed {
		select {
		case frame := <-frames:
			if frame.Type == "error" {
				t.Fatal("replacement failed", frame)
			}
			if frame.Type == "change" {
				for _, path := range frame.Paths {
					if path == "nested" || strings.HasSuffix(path, "/fresh.txt") {
						observed = true
					}
				}
			}
		case <-ctx.Done():
			t.Fatal("replaced native watch failed to reconcile", ctx.Err())
		}
	}
	// The replacement now has a native watch too, not only a one-time dirty event.
	if e = os.WriteFile(filepath.Join(nested, "after.txt"), []byte("live"), 0600); e != nil {
		t.Fatal(e)
	}
	for {
		select {
		case frame := <-frames:
			for _, path := range frame.Paths {
				if path == "nested/after.txt" {
					return
				}
			}
		case <-ctx.Done():
			t.Fatal("new directory watch was not installed", ctx.Err())
		}
	}
}
