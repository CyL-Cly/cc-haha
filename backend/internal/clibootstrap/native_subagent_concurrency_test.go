package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSubagentConcurrencyCountsUnlimitedAndRejectsWithoutQueue(t *testing.T) {
	d := &SubagentConcurrency{}
	release, e := d.reserve(nil)
	if e != nil {
		t.Fatal(e)
	}
	limit := int64(1)
	if _, e = d.reserve(&limit); e == nil {
		t.Fatal("lowering limit forgot existing unlimited worker")
	}
	release()
	release()
	if d.active != 0 {
		t.Fatal("release was not idempotent", d.active)
	}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := d.reserve(nil)
			if e != nil {
				t.Error(e)
			} else {
				r()
				r()
			}
		}()
	}
	wg.Wait()
	if d.active != 0 {
		t.Fatal("concurrent lease leak", d.active)
	}
}

func TestSyncSubagentLimitActualBackgroundForegroundAndIndependentRuntime(t *testing.T) {
	started := make(chan struct{}, 2)
	finish := make(chan struct{})
	var once sync.Once
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		started <- struct{}{}
		select {
		case <-finish:
			emitPlanFixture(w, 1, "", "CONCURRENCY_FINISHED")
		case <-r.Context().Done():
		}
	}))
	defer peer.Close()
	defer once.Do(func() { close(finish) })
	m := backgroundAgentMainWithSetup(t, peer.URL, func(n *NativeBoundary) {
		if e := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"maxConcurrentSubagents":1}`), 0600); e != nil {
			t.Fatal(e)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	spawn := func(parent *MainPrint) error {
		return parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			request, e := actualBackgroundAgentRequest(scope, parent, "hold actual transport")
			if e != nil {
				return e
			}
			future, e := parent.runBackgroundAgent(ctx, scope, request)
			if e != nil {
				return e
			}
			_, e = future.Await(scope)
			return e
		})
	}
	if e := spawn(m); e != nil {
		t.Fatal(e)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Both the background launch and foreground entry fail before transport/worktree side effects.
	if e := spawn(m); e == nil || !strings.Contains(e.Error(), "Maximum concurrent subagents reached (1)") {
		t.Fatal("background quota", e)
	}
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		request, e := actualBackgroundAgentRequest(scope, m, "must reject")
		if e != nil {
			return e
		}
		_, e = m.startNativeForegroundAgent(ctx, scope, request)
		return e
	}); e == nil || !strings.Contains(e.Error(), "Maximum concurrent subagents reached") {
		t.Fatal("foreground quota", e)
	}
	other := backgroundAgentMainWithSetup(t, peer.URL, func(n *NativeBoundary) {
		_ = os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"maxConcurrentSubagents":1}`), 0600)
	})
	if other.options.OrdinarySubagents == m.options.OrdinarySubagents {
		t.Fatal("independent runtime shares domain")
	}
	if e := spawn(other); e != nil {
		t.Fatal("independent runtime incorrectly throttled", e)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	once.Do(func() { close(finish) })
	for _, p := range []*MainPrint{m, other} {
		for {
			p.options.OrdinarySubagents.mu.Lock()
			active := p.options.OrdinarySubagents.active
			p.options.OrdinarySubagents.mu.Unlock()
			if active == 0 {
				break
			}
			select {
			case <-time.After(time.Millisecond * 10):
			case <-ctx.Done():
				t.Fatal("completed lease leak", ctx.Err())
			}
		}
	}
}

func TestSyncNestedChildInheritsUncachedRuntimeQuotaAndCreationFailureReleases(t *testing.T) {
	m := backgroundAgentMain(t, "http://127.0.0.1:1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		request, e := actualBackgroundAgentRequest(scope, m, "original scoped child")
		if e != nil {
			return e
		}
		options, e := m.nativeIndependentAgentOptions(scope, request, "quota-child", m.workingDirectory(), "fixture-model", m.options.Native.Temp, request.SystemPromptSections, nil)
		if e != nil {
			return e
		}
		child, e := NewMainPrint(options)
		if e != nil {
			return e
		}
		defer child.Close()
		if child.options.OrdinarySubagents != m.options.OrdinarySubagents {
			t.Fatal("nested Owner minted new quota domain")
		}
		release, e := m.reserveOrdinarySubagent()
		if e != nil {
			return e
		}
		defer release()
		if e = os.WriteFile(filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"maxConcurrentSubagents":1}`), 0600); e != nil {
			return e
		}
		if _, e = child.reserveOrdinarySubagent(); e == nil {
			t.Fatal("uncached lowered quota did not see already running parent child")
		}
		release()
		request.SystemPromptSections = nil
		if _, e = m.startNativeForegroundAgent(ctx, scope, request); e == nil {
			t.Fatal("invalid child preparation accepted")
		}
		m.options.OrdinarySubagents.mu.Lock()
		active := m.options.OrdinarySubagents.active
		m.options.OrdinarySubagents.mu.Unlock()
		if active != 0 {
			t.Fatal("creation failure leaked slot", active)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
