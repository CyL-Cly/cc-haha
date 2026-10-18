package errorlog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type initializationFailureFiles struct {
	physicalSinkFiles
	failure error
	reject  atomic.Bool
}

func (files *initializationFailureFiles) AppendFile(path string, body []byte) error {
	if files.reject.Load() && strings.Contains(string(body), "Error log sink initialized") {
		return files.failure
	}
	return files.physicalSinkFiles.AppendFile(path, body)
}

func TestAttachNativeSinkPhysicallyFlushesQueuedErrorBeforeInitializationDebug(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "debug.log")
	env := map[string]string{"DEBUG": "1", "CLAUDE_CODE_DEBUG_LOGS_DIR": path, "USER_TYPE": "external"}
	now := func() time.Time { return time.UnixMilli(1700000000000) }
	service, err := New(Options{Environment: func() map[string]string { return env }, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	service.Log(Error{Name: "Error", Message: "queued-before-attach"})
	sink, err := NewAndAttachNativeSink(NativeSinkOptions{Files: physicalSinkFiles{}, Now: now, Metadata: func() Metadata {
		return Metadata{Environment: env, Home: home, ConfigDir: home, CWD: home, SessionID: "owned"}
	}}, service)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Index(text, "queued-before-attach") < 0 || strings.Index(text, "queued-before-attach") > strings.Index(text, "Error log sink initialized") {
		t.Fatal("Source attach-before-debug ordering changed", text)
	}
}

func TestAttachedSinkInitializationFailureRetainsOriginalSinkUntilActualClose(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "debug.log")
	env := map[string]string{"DEBUG": "1", "CLAUDE_CODE_DEBUG_LOGS_DIR": path, "CLAUDE_CONFIG_DIR": home, "USER_TYPE": "ant"}
	now := func() time.Time { return time.UnixMilli(1700000000000) }
	service, err := New(Options{Environment: func() map[string]string { return env }, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("actual initialization append rejected")
	files := &initializationFailureFiles{failure: failure}
	files.reject.Store(true)
	options := NativeSinkOptions{Files: files, Now: now, Metadata: func() Metadata {
		return Metadata{Environment: env, Home: home, ConfigDir: home, CWD: home, SessionID: "owned"}
	}}
	// The pre-existing public constructor still returns nil on initialization
	// failure. Only the attach-before-debug caller owns an attached resource.
	standalone, err := NewNativeSink(options)
	if standalone != nil || err != failure {
		t.Fatal("standalone constructor failure changed", standalone, err)
	}
	service.Log(Error{Name: "Error", Message: "queued-before-failed-initialization"})
	sink, err := NewAndAttachNativeSink(options, service)
	if sink == nil || err != failure {
		t.Fatal("already-attached sink resource was lost", sink, err)
	}
	defer sink.Close()
	files.reject.Store(false)
	if err := sink.Debug("Error log sink initialized", "DEBUG"); err != nil {
		t.Fatal(err)
	}
	service.Attach(func(error) { t.Error("first attached sink replaced after failure") })
	service.Log(Error{Name: "Error", Message: "after-next-initialization"})
	sink.Close()
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), "queued-before-failed-initialization") || strings.Count(string(raw), "Error log sink initialized") != 1 || !strings.Contains(string(raw), "after-next-initialization") {
		t.Fatal("failed initialization/next attempt changed original error identity or debug", err, string(raw))
	}
	cachePath := filepath.Join(home, "Cache", sanitize(home), "errors", strings.NewReplacer(":", "-", ".", "-").Replace(timestamp(now()))+".jsonl")
	cache, err := os.ReadFile(cachePath)
	if err != nil || !strings.Contains(string(cache), "queued-before-failed-initialization") || !strings.Contains(string(cache), "after-next-initialization") || sink.buffers[cachePath].timer != nil || !sink.closed {
		t.Fatal("actual Close did not drain retained attached buffer/timer", err, string(cache))
	}
	service.Log(Error{Name: "Error", Message: "after-close"})
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(raw) {
		t.Fatal("closed retained sink wrote a late error", err)
	}
}
