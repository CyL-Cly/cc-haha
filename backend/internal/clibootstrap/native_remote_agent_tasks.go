package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/ccrsessions"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

func (m *MainPrint) BeginNativeRemoteAgentTasksClose() {
	if m != nil {
		m.nativeRemoteAgentLife.close()
	}
}

func (m *MainPrint) joinNativeRemoteAgentTasks(ctx context.Context) error {
	return m.nativeRemoteAgentLife.join(ctx)
}

func (m *MainPrint) remoteAgentTasksAllowed(scope *cooperative.Scope) bool {
	return m != nil && scope != nil && scope.BelongsTo(m.Owner) && m.options.Native != nil && m.options.Native.NativeProcess && m.options.Interactive && !m.agentChild && m.teamWorker == nil && m.options.AgentID == "" && m.Controller != nil && m.Host != nil && !m.closed && !m.shuttingDown
}

// REPL's initial-history mount consumer. Source does not restore tasks during
// headless startup, on an empty fresh conversation or on every ordinary ASK.
func (m *MainPrint) StartTerminalRemoteTasks(ctx context.Context) error {
	if m == nil || m.Owner == nil || ctx == nil {
		return cooperative.ErrRestricted
	}
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if !m.remoteAgentTasksAllowed(scope) {
			return cooperative.ErrRestricted
		}
		if m.nativeRemoteAgentLife.mounted {
			return nil
		}
		m.nativeRemoteAgentLife.mounted = true
		return m.restoreNativeRemoteAgentTasks(scope)
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}

// The actual non-fork REPL adoption invokes this without awaiting discovery.
// Each awaited filesystem/HTTP operation resumes on the same existing Owner.
func (m *MainPrint) restoreNativeRemoteAgentTasks(scope *cooperative.Scope) error {
	if !m.remoteAgentTasksAllowed(scope) {
		return cooperative.ErrRestricted
	}
	_, err := m.nativeRemoteAgentLife.launch(m, func(ctx context.Context, current *cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error) {
		result, err := ownerasync.NewOwnerFuture[struct{}](m.Owner)
		if err != nil {
			return nil, err
		}
		metadata, err := m.beginNativeRemoteAgentMetadata(current)
		if err != nil {
			return nil, err
		}
		_, err = metadata.Then(current, func(next *cooperative.Scope, values []protocol.JSONObject, failure error) error {
			if failure != nil {
				m.nativeRemoteAgentDebug(next, "restoreRemoteAgentTasks failed: "+failure.Error())
				return result.Resolve(next, struct{}{})
			}
			if len(values) == 0 || ctx.Err() != nil {
				return result.Resolve(next, struct{}{})
			}
			api, err := m.nativeRemoteAgentAPI(next)
			if err != nil {
				m.nativeRemoteAgentDebug(next, "restoreRemoteAgentTasks failed: "+err.Error())
				return result.Resolve(next, struct{}{})
			}
			job := &nativeRemoteAgentRestore{main: m, ctx: ctx, values: values, api: api, result: result}
			return job.next(next)
		})
		return result, err
	})
	return err
}

type nativeRemoteAgentRestore struct {
	main   *MainPrint
	ctx    context.Context
	values []protocol.JSONObject
	index  int
	api    *ccrsessions.TaskAPI
	result *ownerasync.OwnerFuture[struct{}]
}

func (job *nativeRemoteAgentRestore) finish(scope *cooperative.Scope, failure error) error {
	job.api.Close()
	if failure != nil && job.ctx.Err() == nil {
		job.main.nativeRemoteAgentDebug(scope, "restoreRemoteAgentTasks failed: "+failure.Error())
	}
	return job.result.Resolve(scope, struct{}{})
}
func (job *nativeRemoteAgentRestore) next(scope *cooperative.Scope) error {
	if job.ctx.Err() != nil || job.index == len(job.values) {
		return job.finish(scope, nil)
	}
	meta := job.values[job.index]
	job.index++
	sid := meta.String("sessionId")
	fetch, err := nativeSkillWork(job.main, scope, func() (protocol.JSONObject, error) { return job.api.FetchSession(job.ctx, sid) })
	if err != nil {
		return job.finish(scope, err)
	}
	_, err = fetch.Then(scope, func(next *cooperative.Scope, remote protocol.JSONObject, failure error) error {
		if job.ctx.Err() != nil {
			return job.finish(next, nil)
		}
		var err error
		if failure != nil {
			if strings.HasPrefix(failure.Error(), "Session not found:") {
				job.main.nativeRemoteAgentDebug(next, "restoreRemoteAgentTasks: dropping "+meta.String("taskId")+" (404: "+failure.Error()+")")
				err = job.main.removeNativeRemoteAgentMetadata(next, meta.String("taskId"))
			} else {
				job.main.nativeRemoteAgentDebug(next, "restoreRemoteAgentTasks: skipping "+meta.String("taskId")+" (recoverable: "+failure.Error()+")")
			}
		} else if remote.String("session_status") == "archived" {
			err = job.main.removeNativeRemoteAgentMetadata(next, meta.String("taskId"))
		} else {
			err = job.main.registerNativeRemoteAgent(next, meta)
		}
		if err != nil {
			return job.finish(next, err)
		}
		return job.next(next)
	})
	return err
}

func (m *MainPrint) registerNativeRemoteAgent(scope *cooperative.Scope, meta protocol.JSONObject) error {
	id := meta.String("taskId")
	directory, err := m.nativeTaskOutputDirectory(scope)
	if err != nil {
		return err
	}
	path := filepath.Join(directory, id+".output")
	data := nativeRemoteAgentTaskData(meta, path, m.options.Native.NowMillis())
	api, err := m.nativeRemoteAgentAPI(scope)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancelCause(m.nativeRemoteAgentLife.context())
	done, err := m.nativeRemoteAgentLife.launch(m, func(_ context.Context, current *cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error) {
		return m.beginNativeRemoteAgentPoll(ctx, current, id, api)
	})
	if err != nil {
		cancel(err)
		api.Close()
		return err
	}
	entry := &sessioncontroller.BackgroundTask{Data: data, Cancel: cancel, ReadOutput: func(context.Context) (json.RawMessage, error) {
		if err := m.options.Native.check("readFile", path); err != nil {
			return nil, err
		}
		return foregroundTaskRead(path)
	}, Join: func(ctx context.Context) error {
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}}
	entry.Stop = func(current *cooperative.Scope, now float64) error { return m.stopNativeRemoteAgent(current, id, now) }
	if err = m.Controller.RegisterBackgroundTaskOnOwner(scope, entry); err != nil {
		cancel(err)
		return err
	}
	return m.initNativeRemoteAgentOutput(scope, path)
}

func nativeRemoteAgentTaskData(meta protocol.JSONObject, path string, now float64) protocol.JSONObject {
	kind := meta.String("remoteTaskType")
	switch kind {
	case "remote-agent", "ultraplan", "ultrareview", "autofix-pr", "background-pr":
	default:
		kind = "remote-agent"
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: meta.Get("taskId")}, protocol.JSONField{Name: "type", Value: quoteMain("remote_agent")}, protocol.JSONField{Name: "status", Value: quoteMain("running")}, protocol.JSONField{Name: "description", Value: meta.Get("title")}, protocol.JSONField{Name: "toolUseId", Value: meta.Get("toolUseId")}, protocol.JSONField{Name: "startTime", Value: meta.Get("spawnedAt")}, protocol.JSONField{Name: "outputFile", Value: quoteMain(path)}, protocol.JSONField{Name: "outputOffset", Value: rawMain(0)}, protocol.JSONField{Name: "notified", Value: rawMain(false)}, protocol.JSONField{Name: "remoteTaskType", Value: quoteMain(kind)}, protocol.JSONField{Name: "sessionId", Value: meta.Get("sessionId")}, protocol.JSONField{Name: "command", Value: meta.Get("command")}, protocol.JSONField{Name: "title", Value: meta.Get("title")}, protocol.JSONField{Name: "todoList", Value: rawMain([]any{})}, protocol.JSONField{Name: "log", Value: rawMain([]any{})}, protocol.JSONField{Name: "isRemoteReview", Value: meta.Get("isRemoteReview")}, protocol.JSONField{Name: "isUltraplan", Value: meta.Get("isUltraplan")}, protocol.JSONField{Name: "isLongRunning", Value: meta.Get("isLongRunning")}, protocol.JSONField{Name: "pollStartedAt", Value: rawMain(now)}, protocol.JSONField{Name: "remoteTaskMetadata", Value: meta.Get("remoteTaskMetadata")})
}

func (m *MainPrint) appendNativeRemoteAgentOutput(scope *cooperative.Scope, id, path, value string) error {
	if value == "" {
		return nil
	}
	life := &m.nativeRemoteAgentLife
	if life.outputs == nil {
		life.outputs = map[string]*nativeRemoteAgentOutput{}
	}
	out := life.outputs[id]
	if out == nil {
		out = &nativeRemoteAgentOutput{main: m, path: path}
		life.outputs[id] = out
	}
	return out.append(scope, value)
}

func (m *MainPrint) evictNativeRemoteAgentOutput(scope *cooperative.Scope, id string) error {
	out := m.nativeRemoteAgentLife.outputs[id]
	if out == nil {
		return nil
	}
	if !out.draining {
		delete(m.nativeRemoteAgentLife.outputs, id)
		return nil
	}
	_, err := out.flush.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
		delete(m.nativeRemoteAgentLife.outputs, id)
		return nil
	})
	return err
}

func (m *MainPrint) notifyNativeRemoteAgent(scope *cooperative.Scope, id, message string) error {
	task, err := m.Controller.BackgroundTaskOnOwner(scope, id)
	if err != nil || task == nil || nativeSettingsTruthy(task.Data.Get("notified")) {
		return err
	}
	if err = m.Controller.MarkBackgroundNotifiedOnOwner(scope, id); err != nil {
		return err
	}
	return m.Controller.EnqueueTaskNotificationOnOwner(scope, "", message)
}

func (m *MainPrint) endNativeRemoteAgent(scope *cooperative.Scope, id string) error {
	if err := m.evictNativeRemoteAgentOutput(scope, id); err != nil {
		return err
	}
	return m.removeNativeRemoteAgentMetadata(scope, id)
}

type nativeRemoteAgentPollingJob struct {
	main              *MainPrint
	ctx               context.Context
	id                string
	api               *ccrsessions.TaskAPI
	poll              nativeRemoteAgentPoll
	result            *ownerasync.OwnerFuture[struct{}]
	timer             *time.Timer
	stop              func() bool
	pending, finished bool
}

func (m *MainPrint) beginNativeRemoteAgentPoll(ctx context.Context, scope *cooperative.Scope, id string, api *ccrsessions.TaskAPI) (*ownerasync.OwnerFuture[struct{}], error) {
	result, err := ownerasync.NewOwnerFuture[struct{}](m.Owner)
	if err != nil {
		return nil, err
	}
	job := &nativeRemoteAgentPollingJob{main: m, ctx: ctx, id: id, api: api, poll: nativeRemoteAgentPoll{log: []protocol.JSONObject{}}, result: result}
	job.stop = context.AfterFunc(ctx, func() {
		receipt, err := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			if job.finished || job.pending {
				return nil
			}
			if job.timer != nil {
				job.timer.Stop()
			}
			return job.finish(next, nil)
		})
		if err == nil {
			_ = receipt.Wait()
		}
	})
	err = job.next(scope)
	return result, err
}
func (job *nativeRemoteAgentPollingJob) finish(scope *cooperative.Scope, failure error) error {
	if job.finished {
		return nil
	}
	job.finished = true
	if job.timer != nil {
		job.timer.Stop()
	}
	if job.stop != nil {
		job.stop()
	}
	job.api.Close()
	if failure != nil && job.ctx.Err() == nil {
		job.main.nativeRemoteAgentDebug(scope, "remote task poll failed: "+failure.Error())
	}
	return job.result.Resolve(scope, struct{}{})
}
func (job *nativeRemoteAgentPollingJob) next(scope *cooperative.Scope) error {
	if job.finished {
		return nil
	}
	if job.ctx.Err() != nil {
		return job.finish(scope, nil)
	}
	task, err := job.main.Controller.BackgroundTaskOnOwner(scope, job.id)
	if err != nil || task == nil || task.Data.String("status") != "running" {
		return job.finish(scope, err)
	}
	sid, last := task.Data.String("sessionId"), append(json.RawMessage(nil), job.poll.last...)
	job.pending = true
	fetched, err := nativeSkillWork(job.main, scope, func() (ccrsessions.TaskPoll, error) { return job.api.Poll(job.ctx, sid, last) })
	if err != nil {
		job.pending = false
		return job.finish(scope, err)
	}
	_, err = fetched.Then(scope, func(next *cooperative.Scope, response ccrsessions.TaskPoll, failure error) error {
		job.pending = false
		if job.ctx.Err() != nil {
			return job.finish(next, nil)
		}
		current, err := job.main.Controller.BackgroundTaskOnOwner(next, job.id)
		if err != nil || current == nil {
			return job.finish(next, err)
		}
		if failure == nil {
			result := job.poll.consume(current.Data, response, job.main.options.Native.NowMillis())
			if err = job.main.appendNativeRemoteAgentOutput(next, job.id, current.Data.String("outputFile"), result.delta); err != nil {
				return job.finish(next, err)
			}
			if err = job.main.Controller.UpdateBackgroundTaskOnOwner(next, job.id, func(protocol.JSONObject) (protocol.JSONObject, error) { return result.task, nil }); err != nil {
				return job.finish(next, err)
			}
			if result.notification != "" {
				if err = job.main.notifyNativeRemoteAgent(next, job.id, result.notification); err != nil {
					return job.finish(next, err)
				}
			}
			if result.terminal {
				err = job.main.endNativeRemoteAgent(next, job.id)
				return job.finish(next, err)
			}
		} else {
			job.main.nativeRemoteAgentDebug(next, "remote task poll failed: "+failure.Error())
			job.poll.idle = 0
			var started float64
			_ = json.Unmarshal(current.Data.Get("pollStartedAt"), &started)
			if nativeSettingsTruthy(current.Data.Get("isRemoteReview")) && current.Data.String("status") == "running" && job.main.options.Native.NowMillis()-started > 30*60*1000 {
				if err = job.main.Controller.UpdateBackgroundTaskOnOwner(next, job.id, func(value protocol.JSONObject) (protocol.JSONObject, error) {
					value.Set("status", quoteMain("failed"))
					value.Set("endTime", rawMain(job.main.options.Native.NowMillis()))
					return value, nil
				}); err != nil {
					return job.finish(next, err)
				}
				if err = job.main.notifyNativeRemoteAgent(next, job.id, remoteAgentReviewNotification(current.Data, nil, "remote session exceeded 30 minutes")); err != nil {
					return job.finish(next, err)
				}
				err = job.main.endNativeRemoteAgent(next, job.id)
				return job.finish(next, err)
			}
		}
		// The actual 1000ms timer posts its next original callback; there is no
		// parked idle callback preventing an unrelated ordinary ASK from starting.
		job.timer = time.AfterFunc(time.Second, func() {
			receipt, err := job.main.Owner.SubmitIndependent(context.Background(), cooperative.Input, func(next *cooperative.Scope) error { return job.next(next) })
			if err == nil {
				_ = receipt.Wait()
			}
		})
		return nil
	})
	return err
}

func (m *MainPrint) stopNativeRemoteAgent(scope *cooperative.Scope, id string, now float64) error {
	if !m.remoteAgentTasksAllowed(scope) {
		return cooperative.ErrRestricted
	}
	task, err := m.Controller.BackgroundTaskOnOwner(scope, id)
	if err != nil || task == nil || task.Data.String("type") != "remote_agent" {
		return cooperative.ErrRestricted
	}
	if task.Data.String("status") == "running" {
		if err = m.Controller.UpdateBackgroundTaskOnOwner(scope, id, func(value protocol.JSONObject) (protocol.JSONObject, error) {
			value.Set("status", quoteMain("killed"))
			value.Set("notified", rawMain(true))
			value.Set("endTime", rawMain(now))
			return value, nil
		}); err != nil {
			return err
		}
		if sid := task.Data.String("sessionId"); sid != "" {
			_, err = m.nativeRemoteAgentLife.launch(m, func(ctx context.Context, current *cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error) {
				api, err := m.nativeRemoteAgentAPI(current)
				if err != nil {
					return nil, err
				}
				result, err := ownerasync.NewOwnerFuture[struct{}](m.Owner)
				if err != nil {
					api.Close()
					return nil, err
				}
				work, err := nativeSkillWork(m, current, func() (struct{}, error) { return struct{}{}, api.Archive(ctx, sid) })
				if err != nil {
					api.Close()
					return nil, err
				}
				_, err = work.Then(current, func(next *cooperative.Scope, _ struct{}, failure error) error {
					api.Close()
					if failure != nil && ctx.Err() == nil {
						m.nativeRemoteAgentDebug(next, "RemoteAgentTask archive failed: "+failure.Error())
					}
					return result.Resolve(next, struct{}{})
				})
				return result, err
			})
			if err != nil {
				return err
			}
		}
	}
	m.nativeRemoteAgentDebug(scope, fmt.Sprintf("RemoteAgentTask %s killed, archiving session %s", id, task.Data.String("sessionId")))
	return m.endNativeRemoteAgent(scope, id)
}
