package autodream

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestPhysicalSchedulerGatesExcludeCurrentThrottleRollbackAndRetry(t *testing.T) {
	root := t.TempDir()
	memory := filepath.Join(root, "memory")
	transcripts := filepath.Join(root, "transcripts")
	if err := os.MkdirAll(transcripts, 0700); err != nil {
		t.Fatal(err)
	}
	now := float64(time.Now().UnixMilli())
	pid := os.Getpid()
	scans := 0
	service, err := New(Options{MemoryDirectory: func() (string, error) { return memory, nil }, TranscriptDirectory: func() (string, error) { scans++; return transcripts, nil }, NowMillis: func() float64 { return now }, PID: pid, IsProcessRunning: func(other int) bool { return other == pid }})
	if err != nil {
		t.Fatal(err)
	}
	writeSession := func(n int) string {
		t.Helper()
		id := fmt.Sprintf("%08x-1234-1234-1234-123456789abc", n)
		p := filepath.Join(transcripts, id+".jsonl")
		if err := os.WriteFile(p, []byte("{}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		at := time.UnixMilli(int64(now) - 1)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		return id
	}
	current := writeSession(1)
	for i := 2; i <= 6; i++ {
		writeSession(i)
	}
	on := true
	gates := Gates{AutoMemoryEnabled: true, Enabled: &on, FeatureConfig: protocol.NewJSONObject(), CurrentSessionID: current}
	for _, closed := range []Gates{{AutoMemoryEnabled: true}, {AutoMemoryEnabled: false, Enabled: &on}, {AutoMemoryEnabled: true, Enabled: &on, Kairos: true}, {AutoMemoryEnabled: true, Enabled: &on, Remote: true}} {
		if service.TryStart(closed) != nil || scans != 0 {
			t.Fatal("closed gate scanned or started")
		}
		if _, err := os.Stat(memory); !os.IsNotExist(err) {
			t.Fatal("closed gate created memory lease", err)
		}
	}
	attempt := service.TryStart(gates)
	if attempt == nil || attempt.PriorMtime != 0 || len(attempt.SessionIDs) != 5 || scans != 2 {
		t.Fatal("actual first attempt", attempt, scans)
	}
	for _, id := range attempt.SessionIDs {
		if id == current {
			t.Fatal("current session leaked into dream hints")
		}
	}
	if _, err := os.Stat(filepath.Join(memory, ".consolidate-lock")); err != nil {
		t.Fatal(err)
	}
	service.Rollback(attempt.PriorMtime)
	now += 599999
	if service.TryStart(gates) != nil || scans != 2 {
		t.Fatal("failed attempt ignored scan backoff", scans)
	}
	now++
	if service.TryStart(gates) == nil || scans != 4 {
		t.Fatal("ten-minute exact retry did not start", scans)
	}
}

func TestPhysicalSchedulerSessionThresholdAndCompletionStamp(t *testing.T) {
	root := t.TempDir()
	memory := filepath.Join(root, "memory")
	transcripts := filepath.Join(root, "transcripts")
	os.MkdirAll(transcripts, 0700)
	now := float64(time.Now().UnixMilli())
	pid := os.Getpid()
	scans := 0
	service, err := New(Options{MemoryDirectory: func() (string, error) { return memory, nil }, TranscriptDirectory: func() (string, error) { scans++; return transcripts, nil }, NowMillis: func() float64 { return now }, PID: pid, IsProcessRunning: func(p int) bool { return p == pid }})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		p := filepath.Join(transcripts, fmt.Sprintf("%08x-1234-1234-1234-123456789abc.jsonl", i))
		if err := os.WriteFile(p, []byte("{}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	on := true
	gates := Gates{AutoMemoryEnabled: true, Enabled: &on, FeatureConfig: protocol.NewJSONObject(protocol.JSONField{Name: "minSessions", Value: []byte(`5.5`)})}
	if service.TryStart(gates) != nil {
		t.Fatal("fractional minSessions was rounded down")
	}
	if _, err := os.Stat(memory); !os.IsNotExist(err) {
		t.Fatal("session gate acquired lease", err)
	}
	if err := os.WriteFile(filepath.Join(transcripts, "99999999-1234-1234-1234-123456789abc.jsonl"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	now += 600000
	attempt := service.TryStart(gates)
	if attempt == nil || len(attempt.SessionIDs) != 6 {
		t.Fatal("new ready session ignored after throttle", attempt)
	}
	service.Complete()
	stamp := service.ReadLastConsolidatedAt()
	if stamp <= 0 {
		t.Fatal("completion did not retain a successful timestamp")
	}
	before := scans
	now = stamp + 24*3600000 - 1
	if service.TryStart(gates) != nil || scans != before {
		t.Fatal("successful completion did not enforce time gate")
	}
	now++
	_ = service.TryStart(gates)
	if scans != before+1 {
		t.Fatal("exact 24-hour gate did not permit the actual session scan", scans, before)
	}
}
