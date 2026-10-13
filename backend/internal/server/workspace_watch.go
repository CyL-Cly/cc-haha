package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

type workspaceWatchTarget struct{ absolute, relative string }

func (server *Server) workspaceWatchTargets(ctx context.Context, info sessionInfo, requested []string) ([]workspaceWatchTarget, error) {
	if len(requested) == 0 || len(requested) > 64 {
		return nil, badRequest("Watch requires 1 to 64 directory paths")
	}
	result, indexes := []workspaceWatchTarget{}, map[string]int{}
	seen := map[string]bool{}
	for _, path := range requested {
		if seen[path] {
			continue
		}
		seen[path] = true
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resolved, err := server.resolveWorkspacePath(info, path)
		if err != nil {
			return nil, err
		}
		stat, err := os.Stat(resolved.canonical)
		for workspaceMissing(err) && resolved.relative != "" {
			parent := filepath.Dir(resolved.absolute)
			if parent == resolved.absolute {
				break
			}
			resolved, err = server.resolveWorkspacePath(info, parent)
			if err != nil {
				return nil, err
			}
			stat, err = os.Stat(resolved.canonical)
		}
		if err != nil || !stat.IsDir() {
			return nil, fmt.Errorf("Watch directory is unavailable: %s", path)
		}
		target := workspaceWatchTarget{resolved.canonical, resolved.relative}
		if index, exists := indexes[target.absolute]; exists {
			result[index] = target
		} else {
			indexes[target.absolute] = len(result)
			result = append(result, target)
		}
	}
	return result, ctx.Err()
}

func (server *Server) workspaceWatchHTTP(writer http.ResponseWriter, request *http.Request, info sessionInfo) error {
	ctx, cancel := context.WithCancel(requestSignalContext(request))
	defer cancel()
	stopRelay := context.AfterFunc(server.ctx, cancel)
	defer stopRelay()
	targets, err := server.workspaceWatchTargets(ctx, info, request.URL.Query()["path"])
	if err != nil {
		return err
	}
	done := make(chan struct{})
	server.mu.Lock()
	if server.closed {
		server.mu.Unlock()
		return context.Canceled
	}
	if server.workspaceWatches == nil {
		server.workspaceWatches = map[chan struct{}]struct{}{}
	}
	server.workspaceWatches[done] = struct{}{}
	server.mu.Unlock()
	defer func() {
		server.mu.Lock()
		delete(server.workspaceWatches, done)
		close(done)
		server.mu.Unlock()
	}()
	states := map[string]*workspaceWatchState{}
	scans := map[string]*workspaceWatchScan{}
	defer func() {
		for _, scan := range scans {
			scan.close()
		}
	}()
	validate := func(target workspaceWatchTarget) error {
		resolved, e := server.resolveWorkspacePath(info, target.relative)
		if e != nil {
			return e
		}
		if resolved.canonical != target.absolute {
			return fmt.Errorf("Watch directory changed its canonical path: %s", target.relative)
		}
		return ctx.Err()
	}
	for _, target := range targets {
		if e := validate(target); e != nil {
			return e
		}
		state, e := readWorkspaceWatchState(ctx, target.absolute)
		if e != nil {
			return e
		}
		if e = validate(target); e != nil {
			return e
		}
		states[target.absolute] = state
	}
	// Attach native OS watchers only after every path has passed validation.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := watcher.Add(target.absolute); err != nil {
			return err
		}
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(writer)
	writeAbortDone := make(chan struct{})
	stopWriteAbort := context.AfterFunc(ctx, func() {
		// A slow SSE consumer must not hold native descriptors open after a
		// server shutdown merely because a socket write is blocked.
		_ = controller.SetWriteDeadline(time.Now())
		close(writeAbortDone)
	})
	defer func() {
		if !stopWriteAbort() {
			<-writeAbortDone
		}
		_ = controller.SetWriteDeadline(time.Time{})
	}()
	send := func(value any) bool {
		if ctx.Err() != nil {
			return false
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(writer, "data: %s\n\n", raw); err != nil {
			return false
		}
		return controller.Flush() == nil
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	var timer *time.Timer
	var due <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	paths, directories := []string{}, []string{}
	seenPaths, seenDirectories := map[string]bool{}, map[string]bool{}
	notify := func(target workspaceWatchTarget, names []string) {
		if !seenDirectories[target.relative] {
			directories = append(directories, target.relative)
			seenDirectories[target.relative] = true
		}
		for _, name := range names {
			path := normalizeWorkspacePath(filepath.Join(target.relative, name))
			if !seenPaths[path] {
				paths = append(paths, path)
				seenPaths[path] = true
			}
		}
		if due == nil {
			timer = time.NewTimer(60 * time.Millisecond)
			due = timer.C
		}
	}
	// The registration gap can lose native events. Reconcile against the
	// baseline before Ready so a client cannot mistake the gap for a clean tree.
	for _, target := range targets {
		if e := validate(target); e != nil {
			return e
		}
		scan := &workspaceWatchScan{path: target.absolute}
		scans[target.absolute] = scan
		next, e := scan.readChunk(ctx)
		if e != nil {
			return e
		}
		if e = validate(target); e != nil {
			return e
		}
		if next == nil {
			continue
		}
		delete(scans, target.absolute)
		before := states[target.absolute]
		names := changedWorkspaceWatchEntries(before, next)
		identityChanged := !sameWorkspaceWatchIdentity(before.identity, next.identity)
		if identityChanged && target.relative != "" {
			names = append(names, "")
		}
		if len(names) > 0 || identityChanged || before.fingerprint != next.fingerprint {
			notify(target, names)
		}
		states[target.absolute] = next
	}
	if !send(map[string]string{"type": "ready"}) {
		return nil
	}
	reconciliation := time.NewTicker(250 * time.Millisecond)
	defer reconciliation.Stop()
	cursor := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-heartbeat.C:
			if !send(map[string]string{"type": "heartbeat"}) {
				return nil
			}
		case <-reconciliation.C:
			for i := 0; i < min(len(targets), 4); i++ {
				target := targets[cursor]
				cursor = (cursor + 1) % len(targets)
				if e := validate(target); e != nil {
					send(map[string]string{"type": "error", "message": e.Error()})
					return nil
				}
				scan := scans[target.absolute]
				if scan == nil {
					scan = &workspaceWatchScan{path: target.absolute}
					scans[target.absolute] = scan
				}
				next, e := scan.readChunk(ctx)
				if e == nil {
					e = validate(target)
				}
				if e != nil {
					send(map[string]string{"type": "error", "message": e.Error()})
					return nil
				}
				if next == nil {
					continue
				}
				delete(scans, target.absolute)
				before := states[target.absolute]
				names := changedWorkspaceWatchEntries(before, next)
				identityChanged := !sameWorkspaceWatchIdentity(before.identity, next.identity)
				if identityChanged && target.relative != "" {
					names = append(names, "")
				}
				if len(names) > 0 || identityChanged || before.fingerprint != next.fingerprint {
					notify(target, names)
				}
				if identityChanged && next.identity != nil {
					_ = watcher.Remove(target.absolute)
					if e := watcher.Add(target.absolute); e != nil {
						send(map[string]string{"type": "error", "message": e.Error()})
						return nil
					}
				}
				states[target.absolute] = next
			}
		case failure, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			send(map[string]string{"type": "error", "message": failure.Error()})
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			for _, target := range targets {
				name := filepath.Clean(event.Name)
				if name != target.absolute && filepath.Dir(name) != target.absolute {
					continue
				}
				filename := ""
				if name != target.absolute {
					filename = filepath.Base(name)
				} else if event.Has(fsnotify.Write) {
					// Kqueue supplies both a directory-write and its named child
					// events. The named event preserves fs.watch's filename filter.
					continue
				}
				if filename != "" && vcsDirectory(strings.Split(strings.ReplaceAll(filename, `\`, "/"), "/")[0]) {
					continue
				}
				if !seenDirectories[target.relative] {
					directories = append(directories, target.relative)
					seenDirectories[target.relative] = true
				}
				if filename != "" {
					path := normalizeWorkspacePath(filepath.Join(target.relative, filename))
					if !seenPaths[path] {
						paths = append(paths, path)
						seenPaths[path] = true
					}
				}
				if due == nil {
					timer = time.NewTimer(60 * time.Millisecond)
					due = timer.C
				}
			}
		case <-due:
			due, timer = nil, nil
			if !send(map[string]any{"type": "change", "paths": paths, "directories": directories}) {
				return nil
			}
			paths, directories = []string{}, []string{}
			seenPaths, seenDirectories = map[string]bool{}, map[string]bool{}
		}
	}
}
