package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/errorlog"
	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"strings"
)

func (m *MainCommand) PluginCommand(ctx context.Context, scope *cooperative.Scope, command plugincli.Command) (plugincli.Result, error) {
	if m == nil || !scope.BelongsTo(m.Owner) || m.Native == nil || !m.Native.NativeProcess {
		return plugincli.Result{}, cooperative.ErrRestricted
	}
	if m.ownsNativeCommandFactory() {
		pending, err := m.importNativePluginNamespace(scope)
		if err != nil {
			return plugincli.Result{}, err
		}
		namespace, err := pending.Await(scope)
		if err != nil {
			return plugincli.Result{}, err
		}
		return namespace.invoke(ctx, scope, command)
	}
	return m.pluginCommandHandler(ctx, scope, command)
}

func (m *MainCommand) pluginCommandHandler(ctx context.Context, scope *cooperative.Scope, command plugincli.Command) (plugincli.Result, error) {
	if command.Action == "update" {
		if err := m.nativeCommandPluginEvent(scope, command); err != nil {
			return plugincli.Result{}, err
		}
	}
	if result := plugincli.Preflight(command); result != nil {
		return *result, nil
	}
	if command.Action != "update" {
		if err := m.nativeCommandPluginEvent(scope, command); err != nil {
			return plugincli.Result{}, err
		}
	}
	if command.Action == "marketplace-list" && m.nativeCommandInit != nil {
		return m.nativeMarketplaceListOnOwner(ctx, scope, command)
	}

	data, err := scope.AwaitBytes(cooperative.NetworkOpen, func(worker context.Context) (payload []byte, failure error) {
		jobs, err := newPluginCLIJobs(worker, m.Native)
		if err != nil {
			return nil, err
		}
		defer func() {
			jobs.Join()
			if failure == nil {
				var value plugincli.Result
				if failure = json.Unmarshal(payload, &value); failure == nil {
					payload, failure = json.Marshal(pluginCLIReply{Result: value, IgnoreErrors: jobs.Errors()})
				}
			}
		}()
		if strings.HasPrefix(command.Action, "marketplace-") {
			manager, err := m.Settings.pluginCLIMarketplaces(jobs)
			if err != nil {
				return nil, err
			}
			result, err := plugincli.RunMarketplaceWithAnalytics(worker, command, manager, m.Native.Home, m.Native.CWD, m.credentialPlatform, func(event analytics.Event) { m.emitPluginCLIEvent(worker, event) }, func(line string) int { return m.emitPluginCLIProgress(worker, line) })
			if err != nil {
				return nil, err
			}
			return json.Marshal(result)
		}
		if command.Action == "validate" {
			result, err := plugincli.RunValidate(worker, command.Argument, m.Native.CWD)
			if err != nil {
				return nil, err
			}
			return json.Marshal(result)
		}

		options, err := m.Settings.PluginOptions(m.Secure)
		if err != nil {
			return nil, err
		}
		mcp, _, err := m.Settings.PluginMCP(options, m.Secure)
		if err != nil {
			return nil, err
		}
		operations, err := m.Settings.pluginCLIOperations(options, jobs)
		if err != nil {
			return nil, err
		}
		if command.Action != "list" {
			result, err := plugincli.RunOperation(worker, command, operations, func(line string) int { return m.emitPluginCLIProgress(worker, line) })
			if err != nil {
				return nil, err
			}
			return json.Marshal(result)
		}
		snapshot, err := m.Settings.pluginCLIListSnapshot(worker)
		if err != nil {
			return nil, err
		}
		result, err := plugincli.InstalledList(worker, snapshot, plugincli.ListOptions{LoadMCP: func(ctx context.Context, plugin pluginregistry.Loaded) (*protocol.JSONObject, error) {
			data, err := json.Marshal(plugin)
			if err != nil {
				return nil, err
			}
			var object protocol.JSONObject
			if err = object.UnmarshalJSON(data); err != nil {
				return nil, err
			}
			return mcp.LoadPluginMCPServers(ctx, object, nil)
		}}, command.JSON)
		if err != nil {
			return nil, err
		}
		if command.JSON && command.Available {
			manager, err := m.Settings.pluginCLIMarketplaces(jobs)
			if err != nil {
				return nil, err
			}
			available, err := manager.Available(worker, snapshot.Installed)
			if err != nil {
				return nil, err
			}
			var installed []protocol.JSONObject
			if err = json.Unmarshal([]byte(result.Stdout), &installed); err != nil {
				return nil, err
			}
			value := protocol.NewJSONObject(protocol.JSONField{Name: "installed", Value: rawMain(installed)}, protocol.JSONField{Name: "available", Value: rawMain(available)})
			raw, _ := value.MarshalJSON()
			var formatted bytes.Buffer
			_ = json.Indent(&formatted, raw, "", "  ")
			result.Stdout = formatted.String() + "\n"
		}
		return json.Marshal(result)
	})
	if err != nil {
		return plugincli.Result{}, err
	}
	var reply pluginCLIReply
	err = json.Unmarshal(data, &reply)
	if err == nil {
		for _, failure := range reply.IgnoreErrors {
			if m.commandErrors != nil {
				m.commandErrors.Log(errorlog.Error{Name: "Error", Message: failure})
			} else {
				logger, e := errorlog.New(errorlog.Options{Environment: func() map[string]string { return m.Native.Environment }, Now: m.Native.Now})
				if e != nil {
					return plugincli.Result{}, e
				}
				logger.Log(errorlog.Error{Name: "Error", Message: failure})
			}
		}
	}
	return reply.Result, err
}
func (p *NativeProcessSettings) pluginCLIListSnapshot(ctx context.Context) (plugincli.ListSnapshot, error) {
	enabled, _, err := p.pluginCLIEnabled(ctx)
	if err != nil {
		return plugincli.ListSnapshot{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensureSettings(); err != nil {
		return plugincli.ListSnapshot{}, err
	}
	installed, err := p.plugins.installed.Session(ctx)
	if err != nil {
		return plugincli.ListSnapshot{}, err
	}
	loaded, err := p.plugins.loader.LoadFull(ctx)
	if err != nil {
		return plugincli.ListSnapshot{}, err
	}

	return plugincli.ListSnapshot{Installed: installed, Enabled: enabled, Loaded: loaded}, nil
}

type pluginCLIReply struct {
	Result       plugincli.Result
	IgnoreErrors []string
}

func (m *MainCommand) emitPluginCLIProgress(ctx context.Context, line string) int {
	written := 0
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if !scope.BelongsTo(m.Owner) {
			return cooperative.ErrRestricted
		}
		var err error
		written, err = io.WriteString(m.Output, line)
		return err
	})
	if err == nil {
		_ = receipt.Wait()
	}
	return written
}
