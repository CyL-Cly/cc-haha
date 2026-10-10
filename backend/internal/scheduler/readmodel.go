package scheduler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"sync"
	"sync/atomic"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type readModel struct {
	options                  Options
	mu                       sync.Mutex
	index                    *runIndex
	generation               atomic.Uint64
	queueMu                  sync.Mutex
	stopping                 atomic.Bool
	cooldown                 int64
	closing                  bool
	workers                  sync.WaitGroup
	canonicalReads, rebuilds int
}

func (m *readModel) mode() string {
	v, ok := m.options.Environment()["CC_HAHA_LOCAL_INDEX"]
	if !ok || v == "on" {
		return "on"
	}
	if v == "off" || v == "shadow" {
		return v
	}
	return "on"
}
func (m *readModel) deactivateLocked() {
	m.generation.Add(1)
	if m.index != nil {
		_ = m.index.db.Close()
		m.index = nil
	}
}
func (s *Service) deactivateIfOff() {
	if s.projection.mode() == "off" {
		s.projection.mu.Lock()
		s.projection.deactivateLocked()
		s.projection.mu.Unlock()
	}
}
func (m *readModel) getLocked() (*runIndex, error) {
	if m.stopping.Load() || m.cooldown > m.options.Now().UnixMilli() {
		return nil, nil
	}
	if m.index == nil {
		index, e := openRunIndex(m.options.ConfigDir, func() int64 { return m.options.Now().UnixMilli() })
		if e != nil {
			return nil, e
		}
		m.index = index
	}
	return m.index, nil
}
func (m *readModel) errorLocked(e error, reason string) {
	if busyError(e) {
		m.cooldown = m.options.Now().UnixMilli() + 5000
		if m.index != nil {
			_ = m.index.db.Close()
			m.index = nil
		}
	}
	if m.index != nil {
		m.index.degraded(context.Background(), reason)
	}
}
func captureRunSnapshot(ctx context.Context, path string, expected []byte) ([]byte, *localindex.Fingerprint, error) {
	info, e := os.Stat(path)
	if e != nil {
		return nil, nil, e
	}
	f, e := localindex.CaptureFingerprint(ctx, path, info.Size(), 1)
	if e != nil {
		return nil, nil, e
	}
	file, e := os.Open(path)
	if e != nil {
		return nil, nil, e
	}
	defer file.Close()
	if !localindex.FingerprintMatchesFile(file, *f) {
		return nil, nil, nil
	}
	data, e := io.ReadAll(file)
	if e != nil {
		return nil, nil, e
	}
	if int64(len(data)) != f.Size || expected != nil && !bytes.Equal(data, expected) || !localindex.FingerprintMatchesFile(file, *f) || localindex.VerifyFingerprint(ctx, path, *f).Kind != "unchanged" {
		return nil, nil, nil
	}
	return data, f, nil
}
func (m *readModel) replaceLocked(ctx context.Context, index *runIndex, runs []protocol.JSONObject, path string, f *localindex.Fingerprint) error {
	status, e := index.status(ctx)
	if e != nil {
		return e
	}
	serialized := serializeRunFingerprint(*f)
	if status.State == "ready" && status.SourcePath == path && status.Fingerprint == serialized {
		return nil
	}
	m.rebuilds++
	return index.replace(ctx, runs, path, *f, serialized, 0)
}
func (m *readModel) read(ctx context.Context, path string, o PageOptions) *Page {
	if !o.SummaryOnly {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	index, e := m.getLocked()
	if e != nil {
		m.errorLocked(e, "SCHEDULED_RUN_INDEX_READ_FAILED")
		return nil
	}
	if index == nil {
		return nil
	}
	status, e := index.status(ctx)
	if e != nil {
		m.errorLocked(e, "SCHEDULED_RUN_INDEX_READ_FAILED")
		return nil
	}
	f := deserializeRunFingerprint(status.Fingerprint)
	if status.State != "ready" || status.SourcePath != path || f == nil || localindex.VerifyFingerprint(ctx, path, *f).Kind != "unchanged" {
		data, f, e := captureRunSnapshot(ctx, path, nil)
		if e != nil || f == nil {
			m.errorLocked(e, "SCHEDULED_RUN_INDEX_READ_FAILED")
			return nil
		}
		m.canonicalReads++
		// The original projection intentionally does not use the tolerant BOM/blank
		// canonical reader: such files fall through to that reader without loss.
		var file protocol.JSONObject
		var runs []protocol.JSONObject
		if file.UnmarshalJSON(data) != nil || !file.IsObject() || json.Unmarshal(file.Get("runs"), &runs) != nil || len(file.Get("runs")) == 0 || file.Get("runs")[0] != '[' {
			m.errorLocked(nil, "SCHEDULED_RUN_INDEX_READ_FAILED")
			return nil
		}
		if e = m.replaceLocked(ctx, index, runs, path, f); e != nil {
			m.errorLocked(e, "SCHEDULED_RUN_INDEX_READ_FAILED")
			return nil
		}
	}
	page, e := index.page(ctx, o)
	if e != nil {
		m.errorLocked(e, "SCHEDULED_RUN_INDEX_READ_FAILED")
		return nil
	}
	return &page
}
func (s *Service) projectedPage(ctx context.Context, o PageOptions) *Page {
	if s.projection.mode() == "off" {
		s.deactivateIfOff()
		return nil
	}
	if s.projection.mode() == "shadow" {
		return nil
	}
	return s.projection.read(ctx, s.store.runsPath(), o)
}
func shadowDigest(rows any) string {
	b := protocol.StringifyValidatedJavaScriptJSON(raw(rows))
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func (s *Service) shadowPage(ctx context.Context, runs []protocol.JSONObject, o PageOptions, operation string) {
	if s.projection.mode() != "shadow" {
		return
	}
	o.Cursor = ""
	limit := float64(2147483647)
	o.Limit = &limit
	o.SummaryOnly = true
	canonical, e := Paginate(runs, o, "memory")
	if e != nil {
		return
	}
	projected := s.projection.read(ctx, s.store.runsPath(), o)
	if projected != nil && shadowDigest(canonical.Runs) == shadowDigest(projected.Runs) {
		return
	}
	var indexed []protocol.JSONObject
	count := 0
	if projected != nil {
		indexed = projected.Runs
		count = len(indexed)
	}
	details := protocol.NewJSONObject(protocol.JSONField{Name: "operation", Value: quoted(operation)}, protocol.JSONField{Name: "fileCount", Value: raw(len(canonical.Runs))}, protocol.JSONField{Name: "indexedCount", Value: raw(count)}, protocol.JSONField{Name: "fileHash", Value: quoted(shadowDigest(canonical.Runs))}, protocol.JSONField{Name: "indexedHash", Value: quoted(shadowDigest(indexed))})
	if s.options.RecordDiagnostic != nil {
		s.options.RecordDiagnostic(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("local_index_scheduled_run_shadow_comparison")}, protocol.JSONField{Name: "severity", Value: quoted("warn")}, protocol.JSONField{Name: "summary", Value: quoted("Scheduled-run index shadow comparison differed")}, protocol.JSONField{Name: "details", Value: raw(details)}))
	}
}
func (m *readModel) afterWrite(path string, data []byte, runs []protocol.JSONObject) {
	if m.mode() == "off" {
		m.mu.Lock()
		m.deactivateLocked()
		m.mu.Unlock()
		return
	}
	m.queueMu.Lock()
	if m.stopping.Load() {
		m.queueMu.Unlock()
		return
	}
	generation := m.generation.Add(1)
	m.workers.Add(1)
	m.queueMu.Unlock()
	copyData := append([]byte{}, data...)
	copyRuns := make([]protocol.JSONObject, len(runs))
	for n := range runs {
		copyRuns[n] = runs[n].Clone()
	}
	go func() {
		defer m.workers.Done()
		lock := pathLock(path)
		lock.Lock()
		defer lock.Unlock()
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.stopping.Load() || m.mode() == "off" || generation != m.generation.Load() {
			return
		}
		ctx := context.Background()
		_, f, e := captureRunSnapshot(ctx, path, copyData)
		if e != nil {
			m.errorLocked(e, "SCHEDULED_RUN_INDEX_WRITE_FAILED")
			return
		}
		if f == nil {
			return
		}
		if m.stopping.Load() || m.mode() == "off" || generation != m.generation.Load() || localindex.VerifyFingerprint(ctx, path, *f).Kind != "unchanged" {
			return
		}
		index, e := m.getLocked()
		if e == nil && index != nil {
			e = m.replaceLocked(ctx, index, copyRuns, path, f)
		}
		if e != nil {
			m.errorLocked(e, "SCHEDULED_RUN_INDEX_WRITE_FAILED")
		}
	}()
}
func (m *readModel) stop(ctx context.Context) error {
	m.queueMu.Lock()
	m.stopping.Store(true)
	m.queueMu.Unlock()
	m.mu.Lock()
	m.closing = true
	m.deactivateLocked()
	m.mu.Unlock()
	done := make(chan struct{})
	go func() { m.workers.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
