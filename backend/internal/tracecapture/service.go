package tracecapture

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

type Options struct {
	Scope       string
	Store       *storage.Store
	Env         func(string) string
	EntryPoint  string
	ResolveMode func() string
	Compare     func(string, string) int
	Now         func() time.Time
	UUID        func() string
	OnError     func(error)
	// Tests control only scheduling/failure seams; the canonical files, parser,
	// index and response capture remain the native production implementations.
	BeforeAppend func(context.Context) error
	AfterScan    func(context.Context, string) error
}
type Service struct {
	o              Options
	ctx            context.Context
	cancel         context.CancelFunc
	mu             sync.Mutex
	closed         bool
	closing        bool
	closeDone      chan struct{}
	closeError     error
	tails          map[string]chan struct{}
	jobs           map[string]*projectionJob
	backfill       map[string]bool
	tasks          sync.WaitGroup
	captures       sync.WaitGroup
	settingsMu     sync.Mutex
	indexMu        sync.Mutex
	index          *Index
	unavailable    bool
	cooldown       time.Time
	scanGate       chan struct{}
	scanMu         sync.Mutex
	scanWaiters    int
	projectionGate chan struct{}
	canonical      map[string]canonicalRevision
}
type canonicalRevision struct {
	fingerprint localindex.Fingerprint
	resetToken  string
	revision    int64
}
type projectionJob struct {
	done      chan struct{}
	ctx       context.Context
	cancel    context.CancelFunc
	consumers int
	value     *IndexedOverview
	err       error
}

func New(o Options) (*Service, error) {
	if !filepath.IsAbs(o.Scope) {
		return nil, errors.New("Trace capture requires an explicit absolute configuration directory")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.UUID == nil {
		o.UUID = uuid
	}
	if o.Env == nil {
		o.Env = func(string) string { return "" }
	}
	if o.ResolveMode == nil {
		o.ResolveMode = func() string {
			v := o.Env("CC_HAHA_LOCAL_INDEX")
			if v == "off" || v == "shadow" {
				return v
			}
			return "on"
		}
	}
	if o.Compare == nil {
		c := collate.New(language.English)
		var mu sync.Mutex
		o.Compare = func(a, b string) int { mu.Lock(); defer mu.Unlock(); return c.CompareString(a, b) }
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{o: o, ctx: ctx, cancel: cancel, closeDone: make(chan struct{}), tails: map[string]chan struct{}{}, jobs: map[string]*projectionJob{}, backfill: map[string]bool{}, scanGate: make(chan struct{}, 1), projectionGate: make(chan struct{}, 1), canonical: map[string]canonicalRevision{}}, nil
}
func (s *Service) storageDir() string { return filepath.Join(s.o.Scope, "cc-haha", "traces") }
func (s *Service) filePath(id string) string {
	return filepath.Join(s.storageDir(), normalizeSessionID(id)+".jsonl")
}
func (s *Service) managedPath() string { return filepath.Join(s.o.Scope, "cc-haha", "settings.json") }
func (s *Service) readSettings() protocol.JSONObject {
	b, e := os.ReadFile(s.managedPath())
	if e != nil || !json.Valid(b) {
		return obj()
	}
	o := object(b)
	if !o.IsObject() {
		return obj()
	}
	return o
}
func (s *Service) Settings(context.Context) Settings {
	o := object(s.readSettings().Get("traceCapture"))
	return Settings{Enabled: string(o.Get("enabled")) != "false", StorageDir: s.storageDir()}
}
func (s *Service) UpdateSettings(ctx context.Context, enabled *bool) (out Settings, e error) {
	update := func() error {
		if e := ctx.Err(); e != nil {
			return e
		}
		current := s.readSettings()
		previous := object(current.Get("traceCapture"))
		if !previous.IsObject() {
			previous = obj()
		}
		if enabled != nil {
			previous.Set("enabled", raw(*enabled))
		}
		current.Set("traceCapture", raw(previous))
		b := append(pretty(raw(current)), '\n')
		p := s.managedPath()
		if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			return e
		}
		tmp := p + ".tmp." + integerString(int64(os.Getpid())) + "." + integerString(s.o.Now().UnixMilli()) + "." + s.o.UUID()
		if e := os.WriteFile(tmp, b, 0644); e != nil {
			return e
		}
		if e := os.Rename(tmp, p); e != nil {
			return e
		}
		out = s.Settings(ctx)
		return nil
	}
	if s.o.Store != nil {
		e = s.o.Store.WithWriteLock(s.managedPath(), update)
	} else {
		s.settingsMu.Lock()
		e = update()
		s.settingsMu.Unlock()
	}
	return
}
func envBool(v string) (value, defined bool) {
	switch strings.ToLower(string(utf16.Decode(trimUnits(units(jstr(v)))))) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	return false, false
}
func (s *Service) Enabled() bool {
	if v, defined := envBool(s.o.Env("CC_HAHA_TRACE_API_CALLS")); defined {
		return v
	}
	return s.Settings(s.ctx).Enabled
}
func (s *Service) ShouldCapture() bool {
	if v, defined := envBool(s.o.Env("CC_HAHA_TRACE_API_CALLS")); defined {
		return v
	}
	entry := s.o.EntryPoint
	if entry == "" {
		entry = s.o.Env("CLAUDE_CODE_ENTRYPOINT")
	}
	return s.Settings(s.ctx).Enabled && entry == "claude-desktop"
}
func (s *Service) mode() string {
	m := s.o.ResolveMode()
	if m != "on" && m != "off" && m != "shadow" {
		m = "on"
	}
	if m == "off" {
		s.indexMu.Lock()
		db := s.index
		s.index = nil
		s.unavailable = false
		s.cooldown = time.Time{}
		if db != nil {
			_ = db.DB.Close()
		}
		s.indexMu.Unlock()
	}
	return m
}
func (s *Service) getIndex() *Index {
	if s.mode() == "off" {
		return nil
	}
	s.indexMu.Lock()
	defer s.indexMu.Unlock()
	s.mu.Lock()
	closed := s.closed || s.closing
	s.mu.Unlock()
	if closed || s.unavailable || s.o.Now().Before(s.cooldown) {
		return nil
	}
	if s.index != nil {
		return s.index
	}
	db, e := localindex.OpenDatabase(s.ctx, localindex.DatabaseOptions{Path: filepath.Join(s.o.Scope, "cc-haha", "db", "trace-index-v1.sqlite"), Scope: s.o.Scope, Kind: localindex.Trace})
	if e != nil {
		s.failedIndexLocked(e)
		return nil
	}
	s.index = NewIndex(db)
	s.index.Now = s.o.Now
	s.index.UUID = s.o.UUID
	return s.index
}
func (s *Service) failedIndexLocked(e error) {
	if s.index != nil {
		_ = s.index.DB.Close()
		s.index = nil
	}
	code := localindex.ClassifyFailure(e)
	if code == "SQLITE_BUSY" {
		s.cooldown = s.o.Now().Add(5 * time.Second)
	} else {
		s.unavailable = true
	}
	s.report(e)
}
func (s *Service) quarantine(e error) { s.indexMu.Lock(); s.failedIndexLocked(e); s.indexMu.Unlock() }
func (s *Service) report(e error) {
	if e != nil && s.o.OnError != nil {
		s.o.OnError(e)
	}
}
func (s *Service) enqueue(id string, fn func() error) (<-chan error, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errors.New("Trace capture service is closed")
	}
	previous := s.tails[id]
	done := make(chan struct{})
	result := make(chan error, 1)
	s.tails[id] = done
	s.tasks.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.tasks.Done()
		if previous != nil {
			<-previous
		}
		e := fn()
		result <- e
		close(result)
		close(done)
		s.mu.Lock()
		if s.tails[id] == done {
			delete(s.tails, id)
		}
		s.mu.Unlock()
		s.report(e)
	}()
	return result, nil
}
func (s *Service) writeEntry(ctx context.Context, id string, entry protocol.JSONObject) error {
	if s.o.BeforeAppend != nil {
		if e := s.o.BeforeAppend(context.WithoutCancel(ctx)); e != nil {
			return e
		}
	}
	p := s.filePath(id)
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		return e
	}
	f, e := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if e != nil {
		return e
	}
	b := append(protocol.StringifyValidatedJavaScriptJSON(boundedEntry(raw(entry))), '\n')
	n, e := f.Write(b)
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if n != len(b) {
		return errors.New("Short trace JSONL append")
	}
	if closeErr != nil {
		return closeErr
	}
	if _, e = s.ensure(context.WithoutCancel(ctx), normalizeSessionID(id), p, ScanOptions{}); e != nil {
		if i := s.getIndex(); i != nil {
			_ = i.Degrade(context.Background(), normalizeSessionID(id), "TRACE_INDEX_APPEND_FAILED")
		}
	}
	return nil
}
func (s *Service) appendEntry(ctx context.Context, id string, entry protocol.JSONObject) error {
	result, e := s.enqueue(normalizeSessionID(id), func() error { return s.writeEntry(ctx, id, entry) })
	if e != nil {
		return e
	}
	return <-result
}

func (s *Service) Drain(ctx context.Context) error {
	done := make(chan struct{})
	go func() { s.captures.Wait(); s.tasks.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closing {
		done := s.closeDone
		s.mu.Unlock()
		<-done
		s.mu.Lock()
		e := s.closeError
		s.mu.Unlock()
		return e
	}
	s.closing = true
	s.mu.Unlock()
	s.cancel()
	s.captures.Wait()
	s.tasks.Wait()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.indexMu.Lock()
	var e error
	if s.index != nil {
		e = s.index.DB.Close()
		s.index = nil
	}
	s.indexMu.Unlock()
	s.mu.Lock()
	s.closeError = e
	close(s.closeDone)
	s.mu.Unlock()
	return e
}
func (s *Service) scan(ctx context.Context, p string, scan ScanOptions, appendSource *SourceRecord) (*ScanSnapshot, error) {
	s.scanMu.Lock()
	reserved := false
	select {
	case s.scanGate <- struct{}{}:
		reserved = true
	default:
		if s.scanWaiters >= 8 {
			s.scanMu.Unlock()
			return nil, &Error{"TRACE_INDEX_BUSY", "Trace reader queue is full; retry shortly"}
		}
		s.scanWaiters++
	}
	s.scanMu.Unlock()
	if !reserved {
		select {
		case s.scanGate <- struct{}{}:
		case <-ctx.Done():
			s.scanMu.Lock()
			s.scanWaiters--
			s.scanMu.Unlock()
			return nil, ctx.Err()
		}
		s.scanMu.Lock()
		s.scanWaiters--
		s.scanMu.Unlock()
	}
	defer func() { <-s.scanGate }()
	snapshot, e := scanFile(ctx, p, scan, appendSource, s.o.Compare)
	if e == nil && snapshot != nil && s.o.AfterScan != nil {
		e = s.o.AfterScan(ctx, p)
		if e == nil && localindex.DetectSourceChange(ctx, p, snapshot.Fingerprint, ParserVersion).Kind != "unchanged" {
			snapshot = nil
		}
	}
	return snapshot, e
}

func (s *Service) ensure(ctx context.Context, id, p string, scan ScanOptions) (*IndexedOverview, error) {
	if s.getIndex() == nil {
		return nil, nil
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	key := p + "\x00" + integerString(scan.ByteStart)
	s.mu.Lock()
	j := s.jobs[key]
	if j == nil {
		if len(s.jobs) >= 8 {
			s.mu.Unlock()
			return nil, &Error{"TRACE_INDEX_BUSY", "Trace indexing queue is full; retry shortly"}
		}
		jobCtx, cancel := context.WithCancel(s.ctx)
		j = &projectionJob{done: make(chan struct{}), ctx: jobCtx, cancel: cancel}
		s.jobs[key] = j
		s.tasks.Add(1)
		go func() {
			defer s.tasks.Done()
			select {
			case s.projectionGate <- struct{}{}:
				defer func() { <-s.projectionGate }()
				j.value, j.err = s.ensureNow(j.ctx, id, p, scan, 0)
			case <-j.ctx.Done():
				j.err = j.ctx.Err()
			}
			s.mu.Lock()
			if s.jobs[key] == j {
				delete(s.jobs, key)
			}
			close(j.done)
			s.mu.Unlock()
		}()
	}
	j.consumers++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		j.consumers--
		if j.consumers == 0 {
			j.cancel()
		}
		s.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-j.done:
		return j.value, j.err
	}
}
func (s *Service) ensureNow(ctx context.Context, id, p string, scan ScanOptions, attempt int) (*IndexedOverview, error) {
	i := s.getIndex()
	if i == nil {
		return nil, nil
	}
	source, e := i.GetSource(ctx, id)
	if e != nil {
		s.quarantine(e)
		return nil, nil
	}
	var appendSource *SourceRecord
	if source != nil && source.State == "ready" && source.FilePath == p && source.WindowStartByte == scan.ByteStart {
		snapshot, ok := snapshotFromSource(*source)
		if ok {
			change := localindex.DetectSourceChange(ctx, p, snapshot.Fingerprint, ParserVersion)
			switch change.Kind {
			case "unchanged":
				v, e := i.GetSummary(ctx, id)
				if e != nil {
					s.quarantine(e)
					return nil, nil
				}
				return v, nil
			case "deleted":
				_ = i.Delete(ctx, id)
				return nil, nil
			case "append":
				if !source.ScanTruncated {
					appendSource = source
					page, events, e := i.GetSession(ctx, id, ptr(int64(0)), ptr(int64(1)))
					if e != nil {
						s.quarantine(e)
						return nil, nil
					}
					calls := int64(0)
					if page != nil {
						calls = page.Summary.APICalls
					}
					scan.RemainingRecords = ptr(max(int64(0), WindowRecordLimit-calls-events-source.OversizedRecords))
				}
			case "retry":
				return nil, nil
			}
		}
	}
	snapshot, e := s.scan(ctx, p, scan, appendSource)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if os.IsNotExist(e) {
			_ = i.Delete(context.Background(), id)
			return nil, nil
		}
		_ = i.Degrade(context.Background(), id, "TRACE_INDEX_SYNC_FAILED")
		return nil, nil
	}
	if snapshot == nil {
		if attempt < 1 {
			return s.ensureNow(ctx, id, p, scan, attempt+1)
		}
		return nil, nil
	}
	input := projectionInput(id, p, snapshot)
	batches := max(1, (max(len(input.Calls), len(input.Events))+255)/256)
	for n := 0; n < batches; n++ {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		batch := input
		batch.Calls = input.Calls[min(n*256, len(input.Calls)):min((n+1)*256, len(input.Calls))]
		batch.Events = input.Events[min(n*256, len(input.Events)):min((n+1)*256, len(input.Events))]
		if n == 0 && appendSource == nil {
			_, e = i.Replace(ctx, batch)
		} else {
			_, e = i.Append(ctx, batch)
		}
		if e != nil {
			s.quarantine(e)
			return nil, nil
		}
		if n < batches-1 {
			_ = i.Degrade(ctx, id, "TRACE_INDEX_BUILDING")
		}
	}
	v, e := i.GetSummary(ctx, id)
	if e != nil {
		s.quarantine(e)
		return nil, nil
	}
	return v, nil
}
func (s *Service) scheduleBackfill(id, p string, size int64) {
	if size > WindowBytesLimit {
		return
	}
	s.mu.Lock()
	if s.closed || s.backfill[id] || len(s.backfill) >= 8 {
		s.mu.Unlock()
		return
	}
	s.backfill[id] = true
	s.tasks.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.tasks.Done()
		_, e := s.ensure(s.ctx, id, p, ScanOptions{})
		s.report(e)
		s.mu.Lock()
		delete(s.backfill, id)
		s.mu.Unlock()
	}()
}

type traceFile struct {
	id, path string
	size     int64
	mtime    float64
	updated  string
}

func (s *Service) files() ([]traceFile, error) {
	files := []traceFile{}
	entries, e := os.ReadDir(s.storageDir())
	if os.IsNotExist(e) {
		return files, nil
	}
	if e != nil {
		return nil, e
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		p := filepath.Join(s.storageDir(), entry.Name())
		stat, e := os.Stat(p)
		if e != nil {
			continue
		}
		snapshot, e := localindex.SnapshotInfo(stat)
		if e != nil {
			continue
		}
		files = append(files, traceFile{strings.TrimSuffix(entry.Name(), ".jsonl"), p, stat.Size(), snapshot.MtimeMs, iso(stat.ModTime())})
	}
	sort.SliceStable(files, func(a, b int) bool { return s.o.Compare(files[b].updated, files[a].updated) < 0 })
	return files, nil
}
func iso(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }
func (s *Service) ListFiles(ctx context.Context) (protocol.JSONObject, error) {
	files, e := s.files()
	if e != nil {
		return protocol.JSONObject{}, e
	}
	items := []protocol.JSONObject{}
	for _, f := range files {
		items = append(items, obj("sessionId", f.id, "fileSize", f.size, "fileUpdatedAt", f.updated))
	}
	return obj("files", items, "total", len(files), "storageDir", s.storageDir(), "settings", s.Settings(ctx)), nil
}

type ListOptions struct {
	Limit, Offset int64
	All           bool
	Query         string
	SessionIDs    []string
}

func (s *Service) List(ctx context.Context, o ListOptions) (protocol.JSONObject, error) {
	mode := s.mode()
	files, e := s.files()
	if e != nil {
		return protocol.JSONObject{}, e
	}
	wanted := map[string]bool{}
	for _, id := range o.SessionIDs {
		wanted[normalizeSessionID(id)] = true
	}
	matching := []traceFile{}
	query := localindex.NormalizeContent(string(utf16.Decode(trimUnits(units(jstr(o.Query))))))
	for _, f := range files {
		if len(wanted) > 0 && !wanted[f.id] {
			continue
		}
		if query != "" && !strings.Contains(localindex.NormalizeContent(f.id), query) {
			continue
		}
		matching = append(matching, f)
	}
	total := len(matching)
	offset := max(int64(0), o.Offset)
	limit := o.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(200, limit)
	page := matching
	if !o.All {
		a := min(offset, int64(total))
		b := min(a+limit, int64(total))
		page = matching[a:b]
	}
	items := []protocol.JSONObject{}
	for _, f := range page {
		if e = ctx.Err(); e != nil {
			return protocol.JSONObject{}, e
		}
		summary := emptySummary()
		var window *Window
		if mode == "on" {
			if i := s.getIndex(); i != nil {
				v, e := i.GetSummary(ctx, f.id)
				if e != nil {
					s.quarantine(e)
				} else if v != nil {
					summary = v.Summary
					if snap, ok := snapshotFromSource(v.SourceRecord); ok {
						w, e := windowMetadata(snap, v.Summary.APICalls, 0, OverviewOptions{})
						if e != nil {
							return protocol.JSONObject{}, e
						}
						window = &w
					}
					if v.State != "ready" || v.Size != f.size || v.MtimeMs != f.mtime {
						s.scheduleBackfill(f.id, f.path, f.size)
					}
				} else {
					s.scheduleBackfill(f.id, f.path, f.size)
				}
			}
		} else {
			snapshot, e := s.scan(ctx, f.path, ScanOptions{}, nil)
			if e != nil {
				return protocol.JSONObject{}, e
			}
			if snapshot == nil {
				return protocol.JSONObject{}, errors.New("Trace changed while loading; retry the request")
			}
			calls := []protocol.JSONObject{}
			for _, c := range snapshot.Calls {
				calls = append(calls, shellCall(f.id, c))
			}
			summary = summarize(calls)
			summary.Models = summary.Models[:min(64, len(summary.Models))]
			summary.FailedCalls = 0
			for _, c := range snapshot.Calls {
				if c.Failed {
					summary.FailedCalls++
				}
			}
			w, e := windowMetadata(*snapshot, int64(len(snapshot.Calls)), int64(len(snapshot.Events)), OverviewOptions{})
			if e != nil {
				return protocol.JSONObject{}, e
			}
			window = &w
			if mode == "shadow" {
				_, _ = s.ensure(ctx, f.id, f.path, ScanOptions{})
			}
		}
		if summary.UpdatedAt == nil || *summary.UpdatedAt == "" {
			summary.UpdatedAt = &f.updated
		}
		item := obj("sessionId", f.id)
		if window != nil {
			item.Set("window", raw(window))
		}
		item.Set("summary", raw(summary))
		item.Set("fileSize", raw(f.size))
		item.Set("fileUpdatedAt", jstr(f.updated))
		items = append(items, item)
	}
	sort.SliceStable(items, func(a, b int) bool {
		x := text(object(items[a].Get("summary")).Get("updatedAt"))
		y := text(object(items[b].Get("summary")).Get("updatedAt"))
		return s.o.Compare(y, x) < 0
	})
	return obj("traces", items, "total", total, "storageDir", s.storageDir(), "settings", s.Settings(ctx)), nil
}
func (s *Service) File(ctx context.Context, id string) (string, bool, error) {
	p := s.filePath(id)
	_, e := os.Stat(p)
	if os.IsNotExist(e) {
		return "", false, nil
	}
	return p, e == nil, e
}
func (s *Service) Delete(ctx context.Context, id string) (protocol.JSONObject, error) {
	s.mode()
	id = normalizeSessionID(id)
	if id == "" {
		return obj("sessionId", id, "deleted", false), nil
	}
	s.mu.Lock()
	tail := s.tails[id]
	s.mu.Unlock()
	if tail != nil {
		<-tail
	}
	p := s.filePath(id)
	e := os.Remove(p)
	deleted := e == nil
	if e != nil && !os.IsNotExist(e) {
		return protocol.JSONObject{}, e
	}
	s.mu.Lock()
	delete(s.canonical, p)
	s.mu.Unlock()
	if i := s.getIndex(); i != nil {
		if e = i.Delete(ctx, id); e != nil {
			s.quarantine(e)
		}
	}
	return obj("sessionId", id, "deleted", deleted), nil
}
func (s *Service) canonicalRevision(ctx context.Context, p string) (canonicalRevision, error) {
	s.mu.Lock()
	old, exists := s.canonical[p]
	s.mu.Unlock()
	appendOnly := false
	if exists {
		change := localindex.DetectSourceChange(ctx, p, old.fingerprint, ParserVersion)
		if change.Kind == "unchanged" {
			return old, nil
		}
		if change.Kind == "deleted" {
			return canonicalRevision{}, os.ErrNotExist
		}
		appendOnly = change.Kind == "append"
	}
	stat, e := os.Stat(p)
	if e != nil {
		return canonicalRevision{}, e
	}
	fp, e := localindex.CaptureFingerprint(ctx, p, stat.Size(), ParserVersion)
	if e != nil {
		return canonicalRevision{}, e
	}
	derived := min(int64(9007199254740991), max(int64(1), int64(math.Trunc(fp.MtimeMs*1000))+fp.Size))
	token := s.o.UUID()
	if exists && appendOnly {
		token = old.resetToken
		if derived == old.revision {
			derived = min(int64(9007199254740991), derived+1)
		}
	}
	v := canonicalRevision{*fp, token, derived}
	s.mu.Lock()
	s.canonical[p] = v
	s.mu.Unlock()
	return v, nil
}
func revisionResult(id string, rev int64, token string, since *int64, sinceToken *string, legacyReset bool) protocol.JSONObject {
	changed := since == nil || *since != rev
	if sinceToken != nil {
		changed = *sinceToken != token
	}
	reset := false
	if changed {
		if sinceToken != nil {
			key := func(v string) string {
				if p := strings.LastIndexByte(v, ':'); p >= 0 {
					return v[:p]
				}
				return v
			}
			reset = key(*sinceToken) != key(token)
		} else {
			reset = since != nil && legacyReset
		}
	}
	return obj("sessionId", id, "revision", rev, "revisionToken", token, "changed", changed, "reset", reset)
}
func (s *Service) Revision(ctx context.Context, id string, since *int64, sinceToken *string) (protocol.JSONObject, error) {
	id = normalizeSessionID(id)
	p := s.filePath(id)
	_, e := os.Stat(p)
	if os.IsNotExist(e) {
		if i := s.getIndex(); i != nil {
			if e = i.Delete(ctx, id); e != nil {
				s.quarantine(e)
			}
		}
		s.mu.Lock()
		delete(s.canonical, p)
		s.mu.Unlock()
		return revisionResult(id, 0, "missing", since, sinceToken, since != nil && *since != 0), nil
	}
	if e != nil {
		return protocol.JSONObject{}, e
	}
	mode := s.mode()
	if mode == "on" {
		scan := ScanOptions{}
		if i := s.getIndex(); i != nil {
			source, e := i.GetSource(ctx, id)
			if e != nil {
				s.quarantine(e)
			} else if source != nil {
				scan.ByteStart = source.WindowStartByte
			}
		}
		projection, e := s.ensure(ctx, id, p, scan)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		if projection != nil {
			return revisionResult(id, projection.Revision, "db:"+projection.ResetToken+":"+integerString(projection.Revision), since, sinceToken, since != nil && (*since < projection.LastResetRevision || *since > projection.Revision)), nil
		}
	}
	canonical, e := s.canonicalRevision(ctx, p)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	if mode == "shadow" {
		_, _ = s.ensure(ctx, id, p, ScanOptions{})
	}
	token := "file:" + canonical.resetToken + ":" + digest([]byte(localindex.SerializeFingerprint(canonical.fingerprint)))
	return revisionResult(id, canonical.revision, token, since, sinceToken, mode == "on" && since != nil && *since != canonical.revision), nil
}
