package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/releasenotes"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"github.com/charmbracelet/x/term"
	"os"
	"path/filepath"
)

func (m *MainPrint) populateTerminalExportLogo(ctx context.Context, scope *cooperative.Scope, global *protocol.JSONObject, subscriber bool, subscription string, value *tui.ConversationExportPresentation) error {
	n := m.options.Native
	environment := m.nativeOpenAIEnvironment()
	value.Columns = 80
	if columns, _, err := term.GetSize(os.Stdout.Fd()); err == nil && columns > 0 {
		value.Columns = columns
	}
	value.HomeDirectory = m.workingDirectory() == n.Home
	value.Terminal = environment["TERM_PROGRAM"]
	if environment["IS_DEMO"] == "" && global != nil {
		if account, ok := global.Object("oauthAccount"); ok {
			value.Organization = account.String("organizationName")
		}
	}
	projectKey, err := m.originalProjectConfigKey(scope)
	if err != nil {
		return err
	}
	project, err := m.Global.GetProject(projectKey)
	if err != nil {
		return err
	}
	var seen float64
	if project != nil {
		_ = json.Unmarshal(project.Get("projectOnboardingSeenCount"), &seen)
	}
	show := environment["IS_DEMO"] == "" && seen < 4 && (project == nil || string(project.Get("hasCompletedProjectOnboarding")) != "true")
	changelog := ""
	if m.nativeLocalCommands != nil {
		changelog = m.nativeLocalCommands.releaseNotes.Memory()
	}
	if m.terminalExportStartup != nil {
		value.Activities = append([]tui.ExportActivity(nil), m.terminalExportStartup.activities...)
	}
	value.NowMillis = n.NowMillis()
	future, err := nativeSkillWork(m, scope, func() (struct {
		Empty, ClaudeMD bool
	}, error) {
		result := struct {
			Empty, ClaudeMD bool
		}{}
		entries, err := n.readDir(m.workingDirectory())
		if err != nil {
			return result, err
		}
		result.Empty = len(entries) == 0
		_, err = n.stat(filepath.Join(m.workingDirectory(), "CLAUDE.md"))
		result.ClaudeMD = err == nil
		return result, nil
	})
	if err != nil {
		return err
	}
	actual, err := future.Await(scope)
	if err != nil {
		return err
	}
	value.Onboarding = show && (actual.Empty || !actual.ClaudeMD)
	if value.Onboarding {
		if actual.Empty {
			value.Tips = append(value.Tips, "Ask Claude to create a new app or clone a repository")
		} else {
			value.Tips = append(value.Tips, "Run /init to create a CLAUDE.md file with instructions for Claude")
		}
		if value.HomeDirectory {
			value.Tips = append(value.Tips, "Note: You have launched claude in your home directory. For the best experience, launch it in a project directory instead.")
		}
	}
	entries := releasenotes.All(changelog)
	for i := len(entries) - 1; i >= max(0, len(entries)-3); i-- {
		entry := entries[i]
		for _, note := range entry.Notes {
			if len(value.ReleaseNotes) < 3 {
				value.ReleaseNotes = append(value.ReleaseNotes, note)
			}
		}
	}
	last := ""
	if global != nil {
		last = global.String("lastReleaseNotesSeen")
	}
	hasRelease := terminalExportHasRelease(changelog, value.Version, last)
	value.FullLogo = value.Onboarding || hasRelease || nativeEnvTruthy(environment["CLAUDE_CODE_FORCE_FULL_LOGO"])
	value.Opus1mNotice = !nativeEnvTruthy(environment["CLAUDE_CODE_DISABLE_1M_CONTEXT"]) && subscription != "pro" && (!subscriber || subscription != "")
	for _, key := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		if nativeEnvTruthy(environment[key]) {
			value.Opus1mNotice = false
		}
	}
	if global != nil {
		seen = 0
		_ = json.Unmarshal(global.Get("opus1mMergeNoticeSeenCount"), &seen)
		value.OpusNoticeSeenCount = seen
		if seen >= 6 {
			value.Opus1mNotice = false
		}
	}
	value.Status, err = m.terminalExportStatusContext(ctx, scope, global)
	if err == nil {
		err = m.populateTerminalExportBusinessNotices(global, value)
	}
	if err == nil {
		err = m.populateTerminalExportMarketing(global, subscriber, subscription, value)
	}
	return err
}

// These are the mounted original Logo effects. Capture precedes writing the
// file/clipboard, so a failed destination still consumes the render impression.
func (m *MainPrint) CommitTerminalExportRenderEffects(ctx context.Context, value tui.ConversationExportPresentation) error {
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.closed {
			return cooperative.ErrClosed
		}
		changed := false
		if err := m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			next := current.Clone()
			if current.String("lastReleaseNotesSeen") != m.options.Version {
				next.Set("lastReleaseNotesSeen", quoteMain(m.options.Version))
				changed = true
			}
			if value.Opus1mNotice {
				var count float64
				_ = json.Unmarshal(current.Get("opus1mMergeNoticeSeenCount"), &count)
				if count < value.OpusNoticeSeenCount+1 {
					next.Set("opus1mMergeNoticeSeenCount", rawMain(value.OpusNoticeSeenCount+1))
				}
			}
			if value.ResetPasses {
				next.Set("passesUpsellSeenCount", []byte(`0`))
				next.Set("hasVisitedPasses", []byte(`false`))
				next.Set("passesLastSeenRemaining", rawMain(value.PassesRemaining))
			}
			if !value.FullLogo || !value.Onboarding {
				key := ""
				if value.GuestPasses {
					key = "passesUpsellSeenCount"
				} else if value.OverageCreditUpsell {
					key = "overageCreditUpsellSeenCount"
				}
				if key != "" {
					var count float64
					_ = json.Unmarshal(next.Get(key), &count)
					next.Set(key, rawMain(count+1))
				}
			}
			return &next, nil
		}); err != nil {
			return err
		}
		if changed && value.Onboarding {
			key, err := m.originalProjectConfigKey(scope)
			if err != nil {
				return err
			}
			return m.Global.SaveProject(key, func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
				next := current.Clone()
				var count float64
				_ = json.Unmarshal(current.Get("projectOnboardingSeenCount"), &count)
				next.Set("projectOnboardingSeenCount", rawMain(count+1))
				return &next, nil
			})
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}
