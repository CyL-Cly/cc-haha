package application

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/activitystatsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/searchapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

type desktopLocalIndexes struct {
	primary   *localindex.Coordinator
	content   *localindex.ContentCoordinator
	search    *searchapi.Service
	budget    *sessionstore.Budget
	ctx       context.Context
	cancel    context.CancelFunc
	startOnce sync.Once
	done      chan struct{}
}

func runtimeIndexPlatform() string {
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return runtime.GOOS
}

func newDesktopLocalIndexes(o ServerOptions, native *clibootstrap.NativeBoundary) (*desktopLocalIndexes, error) {
	budget := sessionstore.NewBudget()
	parser := activitystatsapi.NewIndexDateParser(time.Local)
	comparator := collate.New(language.English)
	var compareMu sync.Mutex
	primary, err := localindex.NewCoordinator(localindex.CoordinatorOptions{Scope: o.ConfigDir, Budget: budget, SourceMetadata: server.NativeIndexSourceMetadata, Reduction: localindex.ReductionOptions{ParseDate: parser.Parse, Location: time.Local, Platform: runtimeIndexPlatform(), Compare: func(a, b string) int {
		compareMu.Lock()
		defer compareMu.Unlock()
		return comparator.CompareString(a, b)
	}}, ResolveConfiguration: func() localindex.Configuration {
		mode, present := o.Environment["CC_HAHA_LOCAL_INDEX"]
		var warning *string
		if !present {
			mode = "on"
		} else if mode != "on" && mode != "off" && mode != "shadow" {
			mode = "on"
			code := "LOCAL_INDEX_INVALID_MODE"
			warning = &code
		}
		return localindex.Configuration{Scope: o.ConfigDir, Mode: mode, WarningCode: warning}
	}})
	if err != nil {
		return nil, err
	}
	content, err := localindex.NewContentCoordinator(localindex.ContentCoordinatorOptions{Scope: o.ConfigDir, Budget: budget, SpoolDir: native.Temp})
	if err != nil {
		return nil, err
	}
	adapter, err := searchapi.NewNativeIndexAdapter(primary, o.ConfigDir, parser.Parse)
	if err != nil {
		return nil, err
	}
	var resolve sync.Once
	command := searchapi.Command{}
	search, err := searchapi.New(searchapi.Options{ConfigDir: o.ConfigDir, WorkDir: o.WorkDir, Environment: o.Environment, Content: content, Metadata: adapter.Metadata, ReadEntriesAtLines: adapter.ReadEntriesAtLines, GetCandidatesForFilters: adapter.GetCandidatesForFilters, ParseDate: parser.Parse, Ripgrep: func() searchapi.Command {
		resolve.Do(func() {
			path, args, err := clibootstrap.NativeRipgrepCommand(native)
			if err == nil {
				command = searchapi.Command{Path: path, Args: args}
			}
		})
		return searchapi.Command{Path: command.Path, Args: append([]string{}, command.Args...), Argv0: command.Argv0}
	}})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &desktopLocalIndexes{primary: primary, content: content, search: search, budget: budget, ctx: ctx, cancel: cancel, done: make(chan struct{})}, nil
}

func (d *desktopLocalIndexes) start() {
	d.startOnce.Do(func() {
		go func() {
			defer close(d.done)
			deadline := time.Now().Add(30 * time.Second)
			// Source runStart resolves after recording an unavailable/degraded
			// primary index. Its independent content startup still runs.
			_ = d.primary.Start(d.ctx)
			for d.ctx.Err() == nil && d.primary.Status().State == "building" && time.Now().Before(deadline) {
				timer := time.NewTimer(50 * time.Millisecond)
				select {
				case <-d.ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
			if d.ctx.Err() == nil {
				_ = d.content.Start(d.ctx)
			}
		}()
	})
}
func (d *desktopLocalIndexes) close(ctx context.Context) error {
	d.cancel()
	d.startOnce.Do(func() { close(d.done) })
	<-d.done
	cleanup := context.WithoutCancel(ctx)
	return errors.Join(d.content.Stop(cleanup), d.primary.Stop(cleanup), ctx.Err())
}
func (d *desktopLocalIndexes) Mode() string             { return d.primary.Mode() }
func (d *desktopLocalIndexes) ActivityScopeReady() bool { return d.primary.ActivityScopeReady(d.ctx) }
func (d *desktopLocalIndexes) ActivityStats(rangeName string, now time.Time) (*activitystatsapi.Stats, error) {
	return d.primary.GetActivityStats(d.ctx, rangeName, now)
}
