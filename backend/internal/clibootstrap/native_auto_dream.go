package clibootstrap

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/autodream"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/systemprompt"
)

type nativeAutoDreamRuntime struct {
	mu      sync.Mutex // Native scheduler file work only; never held across an owner await.
	service *autodream.Service
	ctx     context.Context
	cancel  context.CancelFunc
	closed  bool
}

func (m *MainPrint) prepareNativeAutoDream() error {
	if m.options.Native == nil || !m.options.Native.NativeProcess || m.memoryPaths == nil {
		return nil
	}
	life := &nativeAutoDreamRuntime{}
	life.ctx, life.cancel = context.WithCancel(context.Background())
	service, err := autodream.New(autodream.Options{MemoryDirectory: m.memoryPaths.Directory,
		TranscriptDirectory: func() (string, error) {
			return filepath.Join(m.options.Native.ConfigHomeDirectory(), "projects", projectpath.Sanitize(m.state.Snapshot().OriginalCWD)), nil
		},
		NowMillis: m.options.Native.NowMillis, PID: os.Getpid(), IsProcessRunning: nativeAutoDreamProcessRunning})
	if err != nil {
		life.cancel()
		return err
	}
	life.service = service
	m.autoDream = life
	return nil
}

func (m *MainPrint) nativeAutoDream(scope *cooperative.Scope, cache queryhooks.CacheSafeParams, run sdkhost.NativeAutoDreamQuery) error {
	if !scope.IsNativeAutoDream(m.Owner) || m.autoDream == nil || run == nil {
		return cooperative.ErrRestricted
	}
	life := m.autoDream
	settings, err := m.Startup.GetInitialSettings()
	if err != nil {
		return err
	}
	var enabled *bool
	if value := settings.Get("autoDreamEnabled"); value != nil {
		var flag bool
		if err = json.Unmarshal(value, &flag); err != nil {
			return err
		}
		enabled = &flag
	}
	gates := autodream.Gates{AutoMemoryEnabled: systemprompt.AutoMemoryEnabled(m.environment, *settings), Enabled: enabled, FeatureConfig: m.nativeAnalyticsConfig(scope, "tengu_onyx_plover"), CurrentSessionID: m.Controller.SessionID()}
	if !gates.AutoMemoryEnabled || !autodream.Enabled(gates.Enabled, gates.FeatureConfig) {
		return nil
	}
	// Pure native file work is off owner; settings and task mutations remain on
	// the original owner. The source's ordinary ASK abort is deliberately absent.
	future, err := nativeSkillWork(m, scope, func() (*autodream.Attempt, error) {
		life.mu.Lock()
		defer life.mu.Unlock()
		if life.closed {
			return nil, nil
		}
		return life.service.TryStart(gates), nil
	})
	if err != nil {
		return err
	}
	attempt, err := future.Await(scope)
	if err != nil || attempt == nil {
		return err
	}
	ctx, cancel := context.WithCancelCause(life.ctx)
	defer cancel(nil)
	var bytes [8]byte
	if _, err = rand.Read(bytes[:]); err != nil {
		return err
	}
	id := "d"
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	for _, value := range bytes {
		id += string(alphabet[int(value)%len(alphabet)])
	}
	directory, err := m.nativeTaskOutputDirectory(scope)
	if err != nil {
		return err
	}
	data := autodream.RegisterTask(id, filepath.Join(directory, id+".output"), len(attempt.SessionIDs), attempt.PriorMtime, m.options.Native.NowMillis())
	done := make(chan struct{})
	defer close(done)
	entry := &sessioncontroller.BackgroundTask{Data: data, Cancel: cancel, ReadOutput: func(context.Context) (json.RawMessage, error) { return nil, nil }, Join: func(ctx context.Context) error {
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}}
	entry.Stop = func(current *cooperative.Scope, now float64) error {
		return m.killNativeAutoDreamOnOwner(current, id, now, life.service)
	}
	if err = m.Controller.RegisterBackgroundTaskOnOwner(scope, entry); err != nil {
		return err
	}
	if !m.options.Interactive {
		if err = m.nativeBackgroundBindings().Emit(scope, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("system")}, protocol.JSONField{Name: "subtype", Value: quoteMain("task_started")}, protocol.JSONField{Name: "task_id", Value: quoteMain(id)}, protocol.JSONField{Name: "description", Value: quoteMain("dreaming")}, protocol.JSONField{Name: "task_type", Value: quoteMain("dream")})); err != nil {
			return err
		}
	}
	prompt := autodream.BuildConsolidationPrompt(attempt.MemoryRoot, attempt.TranscriptDir, autodream.AttemptExtra(*attempt))
	err = run(ctx, prompt, attempt.MemoryRoot, func(message protocol.JSONObject) error {
		if message.String("type") != "assistant" {
			return nil
		}
		body, _ := message.Object("message")
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(body.Get("content"), &blocks)
		text := ""
		uses := 0
		touched := []json.RawMessage{}
		for _, block := range blocks {
			switch block.String("type") {
			case "text":
				text += block.String("text")
			case "tool_use":
				uses++
				if block.String("name") == "Edit" || block.String("name") == "Write" {
					in, _ := block.Object("input")
					if path := in.Get("file_path"); len(path) > 0 && path[0] == '"' {
						touched = append(touched, path)
					}
				}
			}
		}
		turn := protocol.NewJSONObject(protocol.JSONField{Name: "text", Value: quoteMain(protocol.TrimJavaScriptString(text))}, protocol.JSONField{Name: "toolUseCount", Value: rawMain(uses)})
		return m.Controller.UpdateBackgroundTaskOnOwner(scope, id, func(task protocol.JSONObject) (protocol.JSONObject, error) {
			if task.String("status") != "running" {
				return task, nil
			}
			next, _ := autodream.AddTurn(task, turn, touched)
			return next, nil
		})
	})
	if ctx.Err() != nil {
		return nil
	} // kill/forced retirement already owns terminal state and rollback.
	status := "completed"
	if err != nil {
		status = "failed"
	}
	lockWork, lockErr := nativeSkillWork(m, scope, func() (struct{}, error) {
		life.mu.Lock()
		defer life.mu.Unlock()
		if err != nil {
			life.service.Rollback(attempt.PriorMtime)
			return struct{}{}, nil
		}
		life.service.Complete()
		return struct{}{}, nil
	})
	if lockErr == nil {
		_, lockErr = lockWork.Await(scope)
	}
	if lockErr != nil {
		return lockErr
	}
	var terminal *autodream.TerminalEvent
	var written json.RawMessage
	if err := m.Controller.UpdateBackgroundTaskOnOwner(scope, id, func(task protocol.JSONObject) (protocol.JSONObject, error) {
		if task.String("status") != "running" {
			return task, nil
		}
		written = task.Get("filesTouched")
		next, event := autodream.FinishTask(task, status, m.options.Native.NowMillis())
		terminal = event
		return next, nil
	}); err != nil {
		return err
	}
	if terminal != nil {
		if err := m.emitNativeAutoDreamTerminal(scope, id, *terminal); err != nil {
			return err
		}
		var paths []json.RawMessage
		_ = json.Unmarshal(written, &paths)
		if status == "completed" && len(paths) > 0 && m.options.Interactive {
			note := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("system")}, protocol.JSONField{Name: "subtype", Value: quoteMain("memory_saved")}, protocol.JSONField{Name: "writtenPaths", Value: written}, protocol.JSONField{Name: "verb", Value: quoteMain("Improved")}, protocol.JSONField{Name: "timestamp", Value: quoteMain(m.options.Native.Now().UTC().Format("2006-01-02T15:04:05.000Z"))}, protocol.JSONField{Name: "uuid", Value: quoteMain(uuidMain())}, protocol.JSONField{Name: "isMeta", Value: rawMain(false)})
			if err := m.Host.AppendAutoDreamMemorySaved(scope, note); err != nil {
				return err
			}
			return m.nativeBackgroundBindings().Emit(scope, note)
		}
	}
	return nil
}

func (m *MainPrint) emitNativeAutoDreamTerminal(scope *cooperative.Scope, id string, event autodream.TerminalEvent) error {
	if m.options.Interactive {
		return nil
	}
	return m.nativeBackgroundBindings().Emit(scope, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("system")}, protocol.JSONField{Name: "subtype", Value: quoteMain("task_notification")}, protocol.JSONField{Name: "task_id", Value: quoteMain(id)}, protocol.JSONField{Name: "status", Value: quoteMain(event.Status)}, protocol.JSONField{Name: "summary", Value: quoteMain(event.Summary)}, protocol.JSONField{Name: "output_file", Value: quoteMain("")}))
}

// TaskStop the Agent tool retains its Source unsupported-dream behavior. This
// entry is exclusively the original background dialog's DreamTask.kill action.
func (m *MainPrint) StopNativeAutoDream(ctx context.Context, id string) error {
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Cancel, func(scope *cooperative.Scope) error {
		if m.autoDream == nil {
			return mainCap("auto-dream runtime")
		}
		return m.killNativeAutoDreamOnOwner(scope, id, m.options.Native.NowMillis(), m.autoDream.service)
	})
	if err != nil {
		return err
	}
	return receipt.Wait()
}

func (m *MainPrint) killNativeAutoDreamOnOwner(scope *cooperative.Scope, id string, now float64, service *autodream.Service) error {
	entry, err := m.Controller.BackgroundTaskOnOwner(scope, id)
	if err != nil || entry == nil {
		return err
	}
	next, event, prior := autodream.KillTask(entry.Data, now)
	if event == nil {
		return nil
	}
	entry.Cancel(context.Canceled)
	if err = m.Controller.UpdateBackgroundTaskOnOwner(scope, id, func(protocol.JSONObject) (protocol.JSONObject, error) { return next, nil }); err != nil {
		return err
	}
	if err = m.emitNativeAutoDreamTerminal(scope, id, *event); err != nil {
		return err
	}
	work, err := nativeSkillWork(m, scope, func() (struct{}, error) {
		m.autoDream.mu.Lock()
		defer m.autoDream.mu.Unlock()
		service.Rollback(*prior)
		return struct{}{}, nil
	})
	if err != nil {
		return err
	}
	_, err = work.Await(scope)
	return err
}

func (m *MainPrint) closeNativeAutoDream(ctx context.Context) error {
	if m.autoDream == nil {
		return nil
	}
	life := m.autoDream
	life.mu.Lock()
	life.closed = true
	life.cancel()
	life.mu.Unlock()
	receipt, err := m.Owner.SubmitIndependent(context.WithoutCancel(ctx), cooperative.Cancel, func(scope *cooperative.Scope) error {
		return m.retireNativeAutoDreamOnOwner(scope)
	})
	if err != nil {
		return err
	}
	return receipt.Wait()
}

// Desktop Stop/runtime closure is Source's forced runtime-loss boundary,
// distinct from DreamTask.kill in the task dialog. Freeze the authoritative
// failed state before cancellation and join actual work before its bookend.
func (m *MainPrint) retireNativeAutoDreamOnOwner(scope *cooperative.Scope) error {
	if m.autoDream == nil {
		return nil
	}
	rows, err := m.Controller.BackgroundTasksOnOwner(scope)
	if err != nil {
		return err
	}
	var failures []error
	for _, row := range rows {
		if row.String("type") != "dream" || row.String("status") != "running" {
			continue
		}
		id := row.String("id")
		entry, err := m.Controller.BackgroundTaskOnOwner(scope, id)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if entry == nil || entry.Cancel == nil || entry.Join == nil {
			failures = append(failures, mainCap("actual dream cancellation/join"))
			continue
		}
		var prior float64
		_ = json.Unmarshal(row.Get("priorMtime"), &prior)
		if err = m.Controller.UpdateBackgroundTaskOnOwner(scope, id, func(task protocol.JSONObject) (protocol.JSONObject, error) {
			next, _ := autodream.FinishTask(task, "failed", m.options.Native.NowMillis())
			return next, nil
		}); err != nil {
			failures = append(failures, err)
			continue
		}
		entry.Cancel(context.Canceled)
		work, err := nativeSkillWork(m, scope, func() (struct{}, error) {
			err := entry.Join(context.Background())
			m.autoDream.mu.Lock()
			m.autoDream.service.Rollback(prior)
			m.autoDream.mu.Unlock()
			return struct{}{}, err
		})
		if err == nil {
			_, err = work.Await(scope)
		}
		if err != nil {
			failures = append(failures, err)
			continue
		}
		event := autodream.TerminalEvent{Status: "failed", Summary: row.String("description") + ": runtime connection lost; task completion could not be confirmed"}
		failures = append(failures, m.emitNativeAutoDreamTerminal(scope, id, event))
	}
	return errors.Join(failures...)
}
