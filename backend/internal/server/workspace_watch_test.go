package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type workspaceWatchFrame struct {
	Type               string
	Paths, Directories []string
}

func readWorkspaceWatchFrames(input io.Reader) <-chan workspaceWatchFrame {
	frames := make(chan workspaceWatchFrame, 16)
	go func() {
		defer close(frames)
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var frame workspaceWatchFrame
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame) == nil {
				frames <- frame
			}
		}
	}()
	return frames
}
func requireWorkspaceWatchFrame(t *testing.T, frames <-chan workspaceWatchFrame, kind string) workspaceWatchFrame {
	t.Helper()
	select {
	case frame, open := <-frames:
		if !open || frame.Type != kind {
			t.Fatal("unexpected native watch frame", frame, open)
		}
		return frame
	case <-time.After(3 * time.Second):
		t.Fatal("native watch did not emit", kind)
	}
	return workspaceWatchFrame{}
}

func TestWorkspaceNativeWatchAttachesBeforeReadyBatchesNamedChangesAndDrainsOnShutdown(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Fatal("watch opened Agent")
		return nil, nil
	})
	var server *Server
	shutdown := false
	options.OnShutdown = func(context.Context) error {
		server.mu.Lock()
		defer server.mu.Unlock()
		if len(server.workspaceWatches) != 0 {
			t.Error("native watchers not drained before host shutdown")
		}
		shutdown = true
		return nil
	}
	var hostURL string
	server, host := testHost(t, options)
	hostURL = host.URL
	id := createTestSession(t, host, options.WorkDir)
	writeSearchFixture(t, options.WorkDir, "nested/hidden.txt", "before")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", hostURL+"/api/sessions/"+id+"/workspace/watch?path=&path=", nil)
	response, err := host.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" || response.Header.Get("Cache-Control") != "no-cache" || response.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatal(response.StatusCode, response.Header)
	}
	frames := readWorkspaceWatchFrames(response.Body)
	requireWorkspaceWatchFrame(t, frames, "ready")
	// These writes start only after ready, proving native registration is live.
	writeSearchFixture(t, options.WorkDir, "actual.txt", "first\n")
	frame := requireWorkspaceWatchFrame(t, frames, "change")
	if len(frame.Paths) != 1 || frame.Paths[0] != "actual.txt" || len(frame.Directories) != 1 || frame.Directories[0] != "" {
		t.Fatal(frame)
	}
	// Neither metadata directories nor a deeper file-only write is subscribed.
	writeSearchFixture(t, options.WorkDir, ".git/ignored.txt", "metadata")
	writeSearchFixture(t, options.WorkDir, "nested/hidden.txt", "after")
	select {
	case frame := <-frames:
		t.Fatal("native nonrecursive/VCS filtering leaked a change", frame)
	case <-time.After(150 * time.Millisecond):
	}
	if err := server.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !shutdown {
		t.Fatal("host shutdown callback was not run")
	}
	select {
	case _, open := <-frames:
		if open {
			t.Fatal("watch stream remained open after server close")
		}
	case <-time.After(time.Second):
		t.Fatal("watch stream did not close")
	}
}

func TestWorkspaceNativeWatchValidatesAllTargetsAndWatchesNearestLiveParent(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Fatal("watch opened Agent")
		return nil, nil
	})
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	base := "/api/sessions/" + id + "/workspace/watch"
	writeSearchFixture(t, options.WorkDir, "not-directory", "actual")
	requestJSON(t, host, "GET", base, "", 400)
	requestJSON(t, host, "POST", base+"?path=", "", 405)
	requestJSON(t, host, "GET", base+"?path=&path=not-directory", "", 500)
	requestJSON(t, host, "GET", base+"?path=../outside", "", 403)
	requestJSON(t, host, "GET", base+"?"+strings.Repeat("path=&", 65), "", 400)
	server.mu.Lock()
	count := len(server.workspaceWatches)
	server.mu.Unlock()
	if count != 0 {
		t.Fatal("invalid later path leaked a registered watcher", count)
	}
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, "GET", host.URL+base+"?path="+url.QueryEscape("missing/descendant"), nil)
	response, err := host.Client().Do(request)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	frames := readWorkspaceWatchFrames(response.Body)
	requireWorkspaceWatchFrame(t, frames, "ready")
	if err := os.Mkdir(filepath.Join(options.WorkDir, "missing"), 0700); err != nil {
		t.Fatal(err)
	}
	frame := requireWorkspaceWatchFrame(t, frames, "change")
	if len(frame.Paths) != 1 || frame.Paths[0] != "missing" || len(frame.Directories) != 1 || frame.Directories[0] != "" {
		t.Fatal(frame)
	}
	cancel()
	response.Body.Close()
	deadline := time.Now().Add(time.Second)
	for {
		server.mu.Lock()
		count = len(server.workspaceWatches)
		server.mu.Unlock()
		if count == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("request cancellation leaked native watcher")
		}
		time.Sleep(time.Millisecond)
	}
}
