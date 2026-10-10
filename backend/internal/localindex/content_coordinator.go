package localindex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	sqlite "modernc.org/sqlite"
)

const ContentStorageLimit int64 = 512 * 1024 * 1024

type ContentStatus struct {
	State           string  `json:"state"`
	Discovered      int64   `json:"discovered"`
	Indexed         int64   `json:"indexed"`
	DegradedSources int64   `json:"degradedSources"`
	DatabaseBytes   int64   `json:"databaseBytes"`
	WALBytes        int64   `json:"walBytes"`
	LastErrorCode   *string `json:"lastErrorCode"`
}
type ContentCoordinatorOptions struct {
	Scope             string
	DatabasePath      string
	SpoolDir          string
	Budget            *sessionstore.Budget
	Now               func() time.Time
	StorageLimitBytes int64
	WatcherOptions    WatchOptions
	MaxLineBytes      int
	BeforeProject     func(context.Context, ContentCandidate) error
	OpenDatabase      func(context.Context, DatabaseOptions) (*Database, error)
}
type ContentCoordinator struct {
	o                               ContentCoordinatorOptions
	mu                              sync.Mutex
	lifecycleMu                     sync.Mutex
	queueMu                         sync.Mutex
	started, complete, watchHealthy bool
	status                          ContentStatus
	revision                        int64
	db                              *Database
	index                           *ContentIndex
	projector                       *ContentProjector
	watcher                         *Watcher
	ctx                             context.Context
	cancel                          context.CancelFunc
	initialDone                     chan struct{}
	failed                          map[string]string
	discoveryFailure                string
	dirtyQueued                     bool
	recovering                      bool
}

func NewContentCoordinator(o ContentCoordinatorOptions) (*ContentCoordinator, error) {
	if !filepath.IsAbs(o.Scope) || o.Budget == nil {
		return nil, errors.New("Content coordinator requires an explicit absolute config scope and shared history budget")
	}
	if o.DatabasePath == "" {
		o.DatabasePath = filepath.Join(o.Scope, "cc-haha", "db", filename(Content))
	}
	if o.SpoolDir == "" {
		o.SpoolDir = os.TempDir()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.StorageLimitBytes <= 0 {
		o.StorageLimitBytes = ContentStorageLimit
	}
	if o.OpenDatabase == nil {
		o.OpenDatabase = OpenDatabase
	}
	return &ContentCoordinator{o: o, status: ContentStatus{State: "building"}, watchHealthy: true, failed: map[string]string{}}, nil
}
func stringPointer(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func errorCode(err error, fallback string) string {
	var coded *Error
	if errors.As(err, &coded) {
		return coded.Code
	}
	var sqlerr *sqlite.Error
	if errors.As(err, &sqlerr) {
		switch sqlerr.Code() & 255 {
		case 11, 26:
			return "SQLITE_CORRUPT"
		case 5, 6:
			return "SQLITE_BUSY"
		case 13:
			return "SQLITE_FULL"
		case 10:
			return "SQLITE_IOERR"
		case 8:
			return "SQLITE_READONLY"
		}
	}
	return fallback
}
func confirmedCorruption(err error) bool { return errorCode(err, "") == "SQLITE_CORRUPT" }
func removeDatabaseFamily(path string) error {
	if err := PrepareManagedDatabasePath(path, "", filename(Content)); err != nil {
		return err
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
func (c *ContentCoordinator) Status() ContentStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.status
	if s.LastErrorCode != nil {
		v := *s.LastErrorCode
		s.LastErrorCode = &v
	}
	return s
}
func (c *ContentCoordinator) markDirty() {
	c.mu.Lock()
	queue := false
	if c.started {
		c.revision++
		c.status.State = "building"
		c.status.LastErrorCode = nil
		if !c.dirtyQueued {
			c.dirtyQueued = true
			queue = true
		}
	}
	ctx := c.ctx
	c.mu.Unlock()
	if queue {
		go func() {
			c.queueMu.Lock()
			defer c.queueMu.Unlock()
			c.mu.Lock()
			if !c.started || c.ctx != ctx || ctx.Err() != nil {
				c.mu.Unlock()
				return
			}
			c.dirtyQueued = false
			s := c.status
			generation := c.revision
			index := c.index
			healthy := c.watchHealthy
			c.mu.Unlock()
			degraded := s.DegradedSources
			if !healthy {
				degraded++
			}
			_ = index.SetReadiness(ctx, ContentReadinessWrite{State: s.State, Generation: &generation, Discovered: s.Discovered, Indexed: s.Indexed, Degraded: degraded, LastErrorCode: s.LastErrorCode})
		}()
	}
}

// Start establishes resources and schedules projection, so canonical transcript
// reads never delay the HTTP health endpoint while it waits for startup.
func (c *ContentCoordinator) Start(ctx context.Context) error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	return c.startLocked(ctx)
}
func (c *ContentCoordinator) startLocked(ctx context.Context) error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	active, cancel := context.WithCancel(context.WithoutCancel(ctx))
	db, err := c.o.OpenDatabase(active, DatabaseOptions{Path: c.o.DatabasePath, Scope: c.o.Scope, Kind: Content})
	if confirmedCorruption(err) {
		if cleanup := removeDatabaseFamily(c.o.DatabasePath); cleanup == nil {
			db, err = c.o.OpenDatabase(active, DatabaseOptions{Path: c.o.DatabasePath, Scope: c.o.Scope, Kind: Content})
		} else {
			err = cleanup
		}
	}
	if err != nil {
		cancel()
		c.mu.Lock()
		c.status.State = "degraded"
		c.status.LastErrorCode = stringPointer(errorCode(err, "SEARCH_CONTENT_START_FAILED"))
		c.mu.Unlock()
		return nil
	}
	index, _ := NewContentIndex(db, c.o.Scope, c.o.Now)
	projector, err := NewContentProjector(ContentProjectorOptions{Index: index, Budget: c.o.Budget, SpoolDir: c.o.SpoolDir, Now: c.o.Now, MaxLineBytes: c.o.MaxLineBytes})
	if err != nil {
		cancel()
		_ = db.Close()
		return err
	}
	sources, err := index.ListSources(active)
	if err != nil {
		cancel()
		_ = db.Close()
		c.mu.Lock()
		c.status.State = "degraded"
		c.status.LastErrorCode = stringPointer(errorCode(err, "SEARCH_CONTENT_START_FAILED"))
		c.mu.Unlock()
		return nil
	}
	c.mu.Lock()
	c.started = true
	c.complete = false
	c.watchHealthy = true
	c.ctx = active
	c.cancel = cancel
	c.db = db
	c.index = index
	c.projector = projector
	c.revision++
	c.status = ContentStatus{State: "building"}
	c.failed = map[string]string{}
	c.discoveryFailure = ""
	c.dirtyQueued = false
	for _, s := range sources {
		if s.State == "degraded" {
			code := "SEARCH_CONTENT_SOURCE_DEGRADED"
			if s.LastErrorCode != nil {
				code = *s.LastErrorCode
			}
			c.failed[s.Path] = code
		}
	}
	generation := c.revision
	c.initialDone = make(chan struct{})
	done := c.initialDone
	c.mu.Unlock()
	err = index.SetReadiness(active, ContentReadinessWrite{State: "building", Generation: &generation, Discovered: int64(len(sources))})
	if err != nil {
		cancel()
		_ = db.Close()
		c.mu.Lock()
		c.started = false
		c.status.State = "degraded"
		c.status.LastErrorCode = stringPointer(errorCode(err, "SEARCH_CONTENT_START_FAILED"))
		close(done)
		c.mu.Unlock()
		return nil
	}
	watchOptions := c.o.WatcherOptions
	watchOptions.Scope = c.o.Scope
	watchOptions.IsTargetPath = IsContentTranscriptPath
	watchOptions.OnDirty = c.markDirty
	watchOptions.OnFailure = func() {
		c.mu.Lock()
		if c.started {
			c.watchHealthy = false
			c.revision++
			c.status.State = "degraded"
			c.status.LastErrorCode = stringPointer("SEARCH_CONTENT_WATCH_FAILED")
		}
		c.mu.Unlock()
	}
	watchOptions.OnRecovered = func() { c.mu.Lock(); c.watchHealthy = true; c.mu.Unlock() }
	watchOptions.OnBatch = func(_ context.Context, b ReconciliationBatch) error { return c.reconcile(active, b) }
	watcher, _ := NewWatcher(watchOptions)
	c.mu.Lock()
	c.watcher = watcher
	c.mu.Unlock()
	if err = watcher.Start(active); err != nil {
		watchOptions.OnFailure()
	}
	go func() { defer close(done); _ = c.reconcile(active, ReconciliationBatch{FullSweep: true}) }()
	return nil
}
func (c *ContentCoordinator) Stop(ctx context.Context) error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	return c.stopLocked(ctx)
}
func (c *ContentCoordinator) stopLocked(ctx context.Context) error {
	c.mu.Lock()
	if !c.started {
		c.status = ContentStatus{State: "building"}
		c.mu.Unlock()
		return nil
	}
	c.started = false
	c.complete = false
	c.cancel()
	watcher, done, db := c.watcher, c.initialDone, c.db
	c.mu.Unlock()
	if watcher != nil {
		if err := watcher.Stop(context.WithoutCancel(ctx)); err != nil {
			return err
		}
	}
	<-done
	c.queueMu.Lock()
	defer c.queueMu.Unlock()
	err := db.Close()
	c.mu.Lock()
	c.db = nil
	c.index = nil
	c.projector = nil
	c.watcher = nil
	c.status = ContentStatus{State: "building"}
	c.failed = map[string]string{}
	c.discoveryFailure = ""
	c.dirtyQueued = false
	c.recovering = false
	c.mu.Unlock()
	return err
}
func (c *ContentCoordinator) WaitInitial(ctx context.Context) error {
	c.mu.Lock()
	done := c.initialDone
	c.mu.Unlock()
	if done == nil {
		return errors.New("Content index is not started")
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (c *ContentCoordinator) NotifyPath(path string) {
	c.mu.Lock()
	watcher := c.watcher
	c.mu.Unlock()
	if watcher != nil {
		watcher.QueuePath(path)
	}
}
func (c *ContentCoordinator) NotifyFullSweep() {
	c.mu.Lock()
	watcher := c.watcher
	c.mu.Unlock()
	if watcher != nil {
		watcher.QueueFullSweep()
	}
}
func candidateFromPath(scope, path string) (*ContentCandidate, error) {
	parts := pathParts(scope, path)
	if parts == nil {
		return nil, nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(absolute)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil
	}
	s, err := snapshot(info)
	if err != nil {
		return nil, err
	}
	project := filepath.Base(filepath.Join(scope, "projects"))
	owner := strings.TrimSuffix(parts[0], ".jsonl")
	ownerPath := absolute
	if len(parts) >= 2 {
		project = parts[0]
		owner = strings.TrimSuffix(parts[1], ".jsonl")
		if len(parts) > 2 {
			owner = parts[1]
			ownerPath = filepath.Join(scope, "projects", project, owner+".jsonl")
			info, err := os.Lstat(ownerPath)
			if os.IsNotExist(err) || err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
				return nil, coded("SEARCH_CONTENT_OWNER_MISSING", "Nested search source has no openable owner transcript")
			}
			if err != nil {
				return nil, err
			}
		}
	}
	return &ContentCandidate{absolute, project, owner, ownerPath, s.MtimeMs}, nil
}

type DiscoveryResult struct {
	Complete    bool `json:"complete"`
	RootMissing bool `json:"rootMissing,omitempty"`
}

func DiscoverContentSources(ctx context.Context, scope string, emit func([]ContentCandidate) error) (DiscoveryResult, error) {
	root := filepath.Join(scope, "projects")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return DiscoveryResult{Complete: true, RootMissing: true}, nil
	}
	if err != nil {
		return DiscoveryResult{}, err
	}
	type directory struct {
		path    string
		entries []os.DirEntry
	}
	stack := []directory{{root, entries}}
	result := DiscoveryResult{Complete: true}
	batch := []ContentCandidate{}
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		sort.SliceStable(batch, func(a, b int) bool {
			if batch[a].ModifiedAtMs != batch[b].ModifiedAtMs {
				return batch[a].ModifiedAtMs > batch[b].ModifiedAtMs
			}
			return batch[a].Path < batch[b].Path
		})
		e := emit(batch)
		batch = nil
		return e
	}
	for len(stack) > 0 {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		d := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, entry := range d.entries {
			if err = ctx.Err(); err != nil {
				return result, err
			}
			p := filepath.Join(d.path, entry.Name())
			if entry.IsDir() {
				children, e := os.ReadDir(p)
				if os.IsNotExist(e) {
					result.Complete = false
					continue
				}
				if e != nil {
					return result, e
				}
				stack = append(stack, directory{p, children})
				continue
			}
			if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				continue
			}
			candidate, e := candidateFromPath(scope, p)
			if errorCode(e, "") == "SEARCH_CONTENT_OWNER_MISSING" {
				result.Complete = false
				continue
			}
			if e != nil {
				return result, e
			}
			if candidate == nil {
				result.Complete = false
				continue
			}
			batch = append(batch, *candidate)
			if len(batch) >= 25 {
				if e = flush(); e != nil {
					return result, e
				}
			}
		}
	}
	if err = flush(); err != nil {
		return result, err
	}
	return result, nil
}
func (c *ContentCoordinator) reconcile(ctx context.Context, b ReconciliationBatch) error {
	c.queueMu.Lock()
	defer c.queueMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	if !c.started || c.ctx != ctx {
		c.mu.Unlock()
		return nil
	}
	revision := c.revision
	index, projector := c.index, c.projector
	c.mu.Unlock()
	var err error
	if b.FullSweep {
		err = c.sweep(ctx, index, projector)
	} else {
		err = c.targeted(ctx, index, projector, b.Paths)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		c.mu.Lock()
		c.status.State = "degraded"
		c.status.LastErrorCode = stringPointer(errorCode(err, "SEARCH_CONTENT_RECONCILE_FAILED"))
		c.mu.Unlock()
		return err
	}
	return c.finish(ctx, index, revision)
}
func (c *ContentCoordinator) project(ctx context.Context, p *ContentProjector, candidate ContentCandidate) (ContentProjectResult, error) {
	if c.o.BeforeProject != nil {
		if err := c.o.BeforeProject(ctx, candidate); err != nil {
			return ContentProjectResult{}, err
		}
	}
	return p.Project(ctx, candidate)
}
func (c *ContentCoordinator) sweep(ctx context.Context, index *ContentIndex, p *ContentProjector) error {
	sources, err := index.ListSources(ctx)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	failures := map[string]string{}
	discovery, err := DiscoverContentSources(ctx, c.o.Scope, func(batch []ContentCandidate) error {
		for _, candidate := range batch {
			seen[candidate.Path] = true
			result, e := c.project(ctx, p, candidate)
			if e != nil {
				return e
			}
			if result.Kind == "retry" {
				code := "SEARCH_CONTENT_TRANSIENT_IO"
				if result.Reason == "changed-during-read" {
					code = "SEARCH_CONTENT_SOURCE_CHANGED"
				}
				failures[candidate.Path] = code
			}
		}
		return ctx.Err()
	})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.discoveryFailure = errorCode(err, "SEARCH_CONTENT_DISCOVERY_FAILED")
		return nil
	}
	if discovery.RootMissing && len(sources) > 0 {
		c.complete = false
		c.discoveryFailure = "SEARCH_CONTENT_PROJECTS_ROOT_MISSING"
	} else if discovery.Complete {
		c.mu.Unlock()
		for _, source := range sources {
			if !seen[source.Path] {
				if _, err = p.Delete(ctx, source.Path); err != nil {
					c.mu.Lock()
					return err
				}
			}
		}
		c.mu.Lock()
		c.complete = true
		c.failed = failures
		c.discoveryFailure = ""
	} else {
		for path, code := range failures {
			c.failed[path] = code
		}
		c.discoveryFailure = "SEARCH_CONTENT_DISCOVERY_INCOMPLETE"
	}
	return nil
}
func (c *ContentCoordinator) targeted(ctx context.Context, index *ContentIndex, p *ContentProjector, paths []string) error {
	recoveredOwner := false
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		candidate, err := candidateFromPath(c.o.Scope, path)
		var result ContentProjectResult
		if err == nil && candidate != nil {
			result, err = c.project(ctx, p, *candidate)
			if len(pathParts(c.o.Scope, path)) == 2 {
				c.mu.Lock()
				for _, code := range c.failed {
					if code == "SEARCH_CONTENT_OWNER_MISSING" {
						recoveredOwner = true
					}
				}
				c.mu.Unlock()
			}
		} else if err == nil {
			if len(pathParts(c.o.Scope, path)) == 2 {
				sources, e := index.ListSources(ctx)
				if e != nil {
					return e
				}
				for _, source := range sources {
					if source.OwnerTranscriptPath == path && source.Path != path {
						if _, e = p.Delete(ctx, source.Path); e != nil {
							return e
						}
						c.mu.Lock()
						c.failed[source.Path] = "SEARCH_CONTENT_OWNER_MISSING"
						c.mu.Unlock()
					}
				}
			}
			result, err = p.Delete(ctx, path)
		} else if errorCode(err, "") == "SEARCH_CONTENT_OWNER_MISSING" {
			_, _ = p.Delete(ctx, path)
		}
		c.mu.Lock()
		if err != nil {
			c.failed[path] = errorCode(err, "SEARCH_CONTENT_RECONCILE_FAILED")
		} else if result.Kind == "retry" {
			code := "SEARCH_CONTENT_TRANSIENT_IO"
			if result.Reason == "changed-during-read" {
				code = "SEARCH_CONTENT_SOURCE_CHANGED"
			}
			c.failed[path] = code
		} else {
			delete(c.failed, path)
		}
		c.mu.Unlock()
	}
	if recoveredOwner {
		return c.sweep(ctx, index, p)
	}
	return nil
}
func (c *ContentCoordinator) finish(ctx context.Context, index *ContentIndex, revision int64) error {
	sources, err := index.ListSources(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	failures := []string{}
	for _, code := range c.failed {
		failures = append(failures, code)
	}
	if c.discoveryFailure != "" {
		failures = append(failures, c.discoveryFailure)
	}
	pending := false
	for _, source := range sources {
		if source.State != "ready" {
			pending = true
		}
	}
	if pending {
		failures = append(failures, "SEARCH_CONTENT_SOURCE_PENDING")
	}
	db := c.db
	c.mu.Unlock()
	_, storageErr := db.CheckpointPassive(ctx)
	stats, err := db.StorageStats()
	if storageErr == nil {
		storageErr = err
	}
	healthy := storageErr == nil && stats.DatabaseBytes+stats.WALBytes <= c.o.StorageLimitBytes
	c.mu.Lock()
	if !c.started || ctx.Err() != nil {
		c.mu.Unlock()
		return ctx.Err()
	}
	code := ""
	if !healthy {
		code = errorCode(storageErr, "SEARCH_CONTENT_STORAGE_LIMIT")
	} else if len(failures) > 0 {
		code = failures[len(failures)-1]
	} else if !c.watchHealthy {
		code = "SEARCH_CONTENT_WATCH_FAILED"
	}
	ready := c.complete && c.watchHealthy && len(failures) == 0 && revision == c.revision && healthy
	state := "degraded"
	if ready {
		state = "ready"
	} else if revision != c.revision && code == "" {
		state = "building"
	}
	count := int64(len(sources))
	next := ContentStatus{state, count, max(int64(0), count-int64(len(failures))), int64(len(failures)), stats.DatabaseBytes, stats.WALBytes, stringPointer(code)}
	degraded := next.DegradedSources
	if !c.watchHealthy {
		degraded++
	}
	generation := c.revision
	c.mu.Unlock()
	// Publish ready only after its durable readiness row has committed. Status
	// remains lock-free with respect to SQLite I/O, while a foreground query can
	// never observe ready paired with a still-building database snapshot.
	err = index.SetReadiness(ctx, ContentReadinessWrite{State: state, Generation: &generation, Discovered: count, Indexed: next.Indexed, Degraded: degraded, LastErrorCode: next.LastErrorCode})
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started || ctx.Err() != nil || c.index != index {
		return ctx.Err()
	}
	if err != nil {
		c.status.State = "degraded"
		c.status.LastErrorCode = stringPointer(errorCode(err, "SEARCH_CONTENT_RECONCILE_FAILED"))
		return err
	}
	if c.revision == generation && c.watchHealthy {
		c.status = next
	} else {
		c.status.Discovered = next.Discovered
		c.status.Indexed = next.Indexed
		c.status.DatabaseBytes = next.DatabaseBytes
		c.status.WALBytes = next.WALBytes
	}
	return nil
}
func (c *ContentCoordinator) Query(ctx context.Context, query string, o ContentQueryOptions) (*ContentQueryResult, error) {
	c.mu.Lock()
	if !c.started || !c.complete || c.status.State != "ready" || ctx.Err() != nil {
		c.mu.Unlock()
		return nil, nil
	}
	index := c.index
	c.mu.Unlock()
	result, err := index.Query(ctx, query, o)
	if err != nil {
		c.mu.Lock()
		var recoverContext context.Context
		if c.started && c.index == index {
			c.status.State = "degraded"
			c.status.LastErrorCode = stringPointer(errorCode(err, "SEARCH_CONTENT_QUERY_FAILED"))
			if confirmedCorruption(err) && !c.recovering {
				c.recovering = true
				recoverContext = c.ctx
			}
		}
		c.mu.Unlock()
		if recoverContext != nil {
			go c.recoverCorruption(recoverContext)
		}
		return nil, nil
	}
	if ctx.Err() != nil {
		return nil, nil
	}
	return result, nil
}
func (c *ContentCoordinator) recoverCorruption(failedContext context.Context) {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	c.mu.Lock()
	current := c.started && c.ctx == failedContext && c.recovering
	c.mu.Unlock()
	if !current {
		return
	}
	err := c.stopLocked(context.Background())
	if err == nil {
		err = removeDatabaseFamily(c.o.DatabasePath)
	}
	if err == nil {
		err = c.startLocked(context.WithoutCancel(failedContext))
	}
	if err != nil {
		c.mu.Lock()
		c.status.State = "degraded"
		c.status.LastErrorCode = stringPointer(errorCode(err, "SEARCH_CONTENT_CORRUPTION_RECOVERY_FAILED"))
		c.recovering = false
		c.mu.Unlock()
	}
}
func (c *ContentCoordinator) Suggest(ctx context.Context, query string, limit *float64) (*ContentSuggestions, error) {
	c.mu.Lock()
	if !c.started || !c.complete || c.status.State != "ready" || ctx.Err() != nil {
		c.mu.Unlock()
		return nil, nil
	}
	index := c.index
	c.mu.Unlock()
	result, err := index.Suggest(ctx, query, limit)
	if err != nil || ctx.Err() != nil {
		return nil, nil
	}
	return result, nil
}
