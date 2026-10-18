package application

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

type terminalStatusLineRuntime struct {
	mu            sync.Mutex
	cancel        context.CancelFunc
	done          chan struct{}
	wake          chan struct{}
	vimMode       string
	listener      func(json.RawMessage)
	presentation  json.RawMessage
	unsubscribe   func()
	mounted       bool
	closed        bool
	mountRevision uint64
	activeCancel  context.CancelFunc
}

func terminalStatusLinePresentation(state clibootstrap.TerminalStatusLineState, text *string, trustBlocked bool) json.RawMessage {
	value := map[string]any{"configured": state.Configured, "padding": state.Padding}
	if text != nil {
		value["text"] = *text
	}
	if trustBlocked {
		value["trustBlocked"] = true
	}
	return raw(value)
}

func (s *Session) SubscribeTerminalStatusLine(ctx context.Context, listener func(json.RawMessage)) (json.RawMessage, func(), error) {
	return s.SubscribeTerminalStatusLineMount(ctx, true, "", listener)
}

func (s *Session) SubscribeTerminalStatusLineMount(ctx context.Context, mounted bool, vimMode string, listener func(json.RawMessage)) (json.RawMessage, func(), error) {
	life, cancel := context.WithCancel(ctx)
	runtime := &terminalStatusLineRuntime{cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1), listener: listener, mounted: mounted, vimMode: vimMode}
	initial, removeNative, err := s.process.SubscribeNativeTerminalStatusLine(ctx, func() {
		select {
		case runtime.wake <- struct{}{}:
		default:
		}
	})
	if err != nil {
		cancel()
		return nil, nil, err
	}
	runtime.unsubscribe = removeNative
	if runtime.vimMode == "" && initial.EditorMode == "vim" {
		runtime.vimMode = "INSERT"
	}
	runtime.presentation = terminalStatusLinePresentation(initial, nil, initial.Configured && !initial.Trusted)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		removeNative()
		return nil, nil, cooperative.ErrClosed
	}
	previous := s.terminalStatusLine
	s.terminalStatusLine = runtime
	s.mu.Unlock()
	if previous != nil {
		previous.unsubscribe()
		previous.cancel()
		<-previous.done
	}
	initialPresentation := append(json.RawMessage(nil), runtime.presentation...)
	go s.runTerminalStatusLine(life, runtime, initial)
	var once sync.Once
	return initialPresentation, func() {
		once.Do(func() {
			runtime.unsubscribe()
			runtime.mu.Lock()
			runtime.listener = nil
			runtime.closed = true
			runtime.mu.Unlock()
			runtime.cancel()
			<-runtime.done
			s.mu.Lock()
			if s.terminalStatusLine == runtime {
				s.terminalStatusLine = nil
			}
			s.mu.Unlock()
		})
	}, nil
}

// A committed terminal visibility change is a component mount boundary. This
// setter performs no Owner operation or IO; it cancels the previous command
// synchronously, then wakes the owned loop to reset its cache/timer or remount.
func (s *Session) SetTerminalStatusLineMounted(ctx context.Context, mounted bool, vimMode string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	runtime, closed := s.terminalStatusLine, s.closed
	s.mu.Unlock()
	if closed {
		return cooperative.ErrClosed
	}
	if runtime == nil {
		return nil
	}
	runtime.mu.Lock()
	if runtime.closed {
		runtime.mu.Unlock()
		return cooperative.ErrClosed
	}
	if runtime.mounted != mounted {
		runtime.mountRevision++
		runtime.mounted = mounted
	}
	runtime.vimMode = vimMode
	cancel := runtime.activeCancel
	runtime.mu.Unlock()
	if !mounted && cancel != nil {
		cancel()
	}
	select {
	case runtime.wake <- struct{}{}:
	default:
	}
	return nil
}

// The terminal supplies its real editor mode. Current messages, model,
// permissions and settings remain on their existing native Owner, sampled
// by the component at the original update callback rather than copied by UI.
func (s *Session) UpdateTerminalStatusLine(ctx context.Context, vimMode string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	runtime, closed := s.terminalStatusLine, s.closed
	s.mu.Unlock()
	if closed {
		return cooperative.ErrClosed
	}
	if runtime == nil {
		return nil
	}
	runtime.mu.Lock()
	runtime.vimMode = vimMode
	runtime.mu.Unlock()
	select {
	case runtime.wake <- struct{}{}:
	default:
	}
	return nil
}

func (s *Session) runTerminalStatusLine(ctx context.Context, runtime *terminalStatusLineRuntime, initial clibootstrap.TerminalStatusLineState) {
	defer close(runtime.done)
	type completion struct {
		generation uint64
		result     clibootstrap.TerminalStatusLineResult
	}
	finished := make(chan completion, 1)
	var jobs sync.WaitGroup
	var active context.CancelFunc
	var generation uint64
	var timer *time.Timer
	var timerC <-chan time.Time
	var text *string
	var cache clibootstrap.TerminalStatusLineCache
	runtime.mu.Lock()
	mounted, revision, vimMode := runtime.mounted, runtime.mountRevision, runtime.vimMode
	runtime.mu.Unlock()
	state := initial
	logNext := true
	publish := func() {
		value := terminalStatusLinePresentation(state, text, false)
		runtime.mu.Lock()
		changed := string(value) != string(runtime.presentation)
		runtime.presentation = value
		listener := runtime.listener
		runtime.mu.Unlock()
		if changed && listener != nil && ctx.Err() == nil {
			listener(append(json.RawMessage(nil), value...))
		}
	}
	stopTimer := func() {
		if timer != nil {
			timer.Stop()
			timer = nil
			timerC = nil
		}
	}
	start := func() {
		if active != nil {
			active()
		}
		generation++
		current := generation
		work, cancel := context.WithCancel(ctx)
		active = cancel
		runtime.mu.Lock()
		allowed := runtime.mounted && !runtime.closed
		runtime.activeCancel = cancel
		runtime.mu.Unlock()
		if !allowed {
			cancel()
			return
		}
		invocation, err := s.process.StartTerminalStatusLine(work, vimMode, &cache, logNext)
		logNext = false
		if err != nil {
			cancel()
			return
		}
		jobs.Add(1)
		go func() {
			defer jobs.Done()
			result, err := invocation.Wait(context.Background())
			if err != nil || work.Err() != nil {
				return
			}
			select {
			case finished <- completion{current, result}:
			case <-ctx.Done():
			}
		}()
	}
	defer func() {
		stopTimer()
		if active != nil {
			active()
		}
		jobs.Wait()
	}()
	if state.Configured && mounted {
		_ = s.process.MountTerminalStatusLine(ctx)
		start()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-finished:
			runtime.mu.Lock()
			allowed := runtime.mounted && !runtime.closed
			runtime.mu.Unlock()
			if item.generation == generation && state.Configured && allowed {
				text = item.result.Text
				publish()
			}
		case <-timerC:
			timer, timerC = nil, nil
			start()
		case <-runtime.wake:
			next, err := s.process.TerminalStatusLineState(ctx)
			if err != nil {
				continue
			}
			runtime.mu.Lock()
			nextVim := runtime.vimMode
			nextMounted, nextRevision := runtime.mounted, runtime.mountRevision
			runtime.mu.Unlock()
			wasConfigured := state.Configured
			mountChanged := mounted != nextMounted || revision != nextRevision
			commandChanged := state.Command != next.Command
			triggerChanged := state.AssistantID != next.AssistantID || state.PermissionMode != next.PermissionMode || state.Model != next.Model || vimMode != nextVim
			state, vimMode = next, nextVim
			mounted, revision = nextMounted, nextRevision
			if !state.Configured || !mounted {
				generation++
				stopTimer()
				if active != nil {
					active()
				}
				cache = clibootstrap.TerminalStatusLineCache{}
				publish()
				continue
			}
			publish()
			if !wasConfigured || mountChanged {
				cache = clibootstrap.TerminalStatusLineCache{}
				_ = s.process.MountTerminalStatusLine(ctx)
			}
			if !wasConfigured || commandChanged || mountChanged {
				logNext = true
				start()
			} else if triggerChanged {
				stopTimer()
				timer = time.NewTimer(300 * time.Millisecond)
				timerC = timer.C
			}
		}
	}
}

func (s *Session) closeTerminalStatusLine() {
	s.mu.Lock()
	runtime := s.terminalStatusLine
	s.terminalStatusLine = nil
	s.mu.Unlock()
	if runtime != nil {
		runtime.unsubscribe()
		runtime.mu.Lock()
		runtime.listener = nil
		runtime.closed = true
		runtime.mu.Unlock()
		runtime.cancel()
		<-runtime.done
	}
}
