package clibootstrap

import (
	"context"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/releasenotes"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
)

type nativeLocalCommandState struct{ releaseNotes *releasenotes.Service }

func (m *MainPrint) nativeLocalCommand(ctx context.Context, scope *cooperative.Scope, name, args string) (string, error) {
	if !scope.BelongsTo(m.Owner) || m.options.Native == nil {
		return "", cooperative.ErrRestricted
	}
	if name == "context" {
		return m.nativeContextCommand(ctx, scope)
	}
	if name == "effort" {
		return m.nativeEffortCommand(scope, args)
	}
	if name == "heapdump" {
		return m.nativeHeapDump(ctx, scope)
	}
	if name != "release-notes" {
		return "", mainCap("native local command /" + name)
	}
	if m.nativeLocalCommands == nil {
		n := m.options.Native
		config := n.Environment["CLAUDE_CONFIG_DIR"]
		if config == "" {
			config = filepath.Join(n.Home, ".claude")
		}
		client, err := webfetch.NewHTTPClient(n.Environment, m.tls)
		if err != nil {
			return "", err
		}
		files := mainResumeFiles{n: n}
		service, err := releasenotes.New(releasenotes.Options{CachePath: filepath.Join(config, "cache", "changelog.md"), ReadFile: n.readFile, MkdirAll: files.MkdirAll, WriteFile: files.WriteFile, Fetch: releasenotes.HTTPFetcher(client, releasenotes.RawChangelogURL), NowMillis: n.NowMillis, SaveTimestamp: func(timestamp float64) error {
			receipt, err := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
				if !next.BelongsTo(m.Owner) {
					return cooperative.ErrRestricted
				}
				return m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
					value := current.Clone()
					value.Set("changelogLastFetched", rawMain(timestamp))
					return &value, nil
				})
			})
			if err == nil {
				err = receipt.Wait()
			}
			return err
		}})
		if err != nil {
			return "", err
		}
		if _, err = m.Cleanup.Register(scope, runtimecleanup.NewCallback(func(next *cooperative.Scope) (*runtimecleanup.Future, error) {
			return nativeSkillWork(m, next, func() (struct{}, error) { service.Close(); return struct{}{}, nil })
		})); err != nil {
			service.Close()
			return "", err
		}
		m.nativeLocalCommands = &nativeLocalCommandState{releaseNotes: service}
	}
	// Source's global session flag is interactive only for the terminal process.
	// The embedded SDK's own normalized noninteractive policy stays unchanged.
	nonInteractive := !m.options.ForegroundShellProgress
	essentialOnly := m.options.Native.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] != ""
	future, err := nativeSkillWork(m, scope, func() (string, error) {
		return m.nativeLocalCommands.releaseNotes.Command(ctx, nonInteractive, essentialOnly)
	})
	if err != nil {
		return "", err
	}
	return future.Await(scope)
}
