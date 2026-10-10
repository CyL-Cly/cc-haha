package clibootstrap

import (
	"context"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"time"
)

func (m *MainPrint) configureNativeTeamPane(ctx context.Context, scope *cooperative.Scope, backend string) (string, error) {
	leader := m.teamLeader
	if !leader.panes.NeedsITermSetup() || m.options.TeamPaneSetup == nil {
		return backend, nil
	}
	callback := m.options.TeamPaneSetup
	defer callback(context.Background(), scope, teamspawn.PaneSetupPrompt{Step: "clear"})
	available, err := nativeSkillWork(m, scope, func() (bool, error) { return leader.panes.IsTmuxAvailable(), nil })
	if err != nil {
		return "", err
	}
	tmux, err := available.Await(scope)
	if err != nil {
		return "", err
	}
	work, err := nativeSkillWork(m, scope, func() (string, error) { return leader.panes.DetectPythonPackageManager(), nil })
	if err != nil {
		return "", err
	}
	manager, err := work.Await(scope)
	if err != nil {
		return "", err
	}
	step, detail := "initial", ""
	redetect := func() (string, error) {
		leader.panes.ResetBackendDetection()
		next, err := nativeSkillWork(m, scope, func() (string, error) {
			return leader.panes.SelectBackend(leader.mode, !m.options.Interactive, leader.preferTmux)
		})
		if err != nil {
			return "", err
		}
		return next.Await(scope)
	}
	save := func(key string) error {
		return m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			if string(current.Get(key)) == "true" {
				return current, nil
			}
			next := current.Clone()
			next.Set(key, rawMain(true))
			return &next, nil
		})
	}
	for {
		if err := context.Cause(ctx); err != nil {
			return "", err
		}
		prompt := teamspawn.PaneSetupPrompt{Step: step, Title: "iTerm2 Split Pane Setup", Description: detail}
		switch step {
		case "initial":
			prompt.Description = "To use native iTerm2 split panes for teammates, you need the it2 CLI tool.\nThis enables teammates to appear as split panes within your current window."
			description := "Requires Python (uvx, pipx, or pip)"
			if manager != "" {
				description = fmt.Sprintf("Uses %s to install the it2 CLI tool", manager)
			}
			prompt.Choices = []teamspawn.PaneSetupChoice{{Value: "install", Label: "Install it2 now", Description: description}}
		case "install-failed":
			manual := "pip install --user it2"
			if manager == "uvx" {
				manual = "uv tool install it2"
			} else if manager == "pipx" {
				manual = "pipx install it2"
			}
			prompt.Description = "Installation failed\n" + detail + "\nYou can try installing manually: " + manual
			prompt.Choices = []teamspawn.PaneSetupChoice{{Value: "retry", Label: "Try again", Description: "Retry the installation"}}
		case "failed":
			prompt.Description = "Verification failed\n" + detail + "\nMake sure:\n· Python API is enabled in iTerm2 preferences\n· You may need to restart iTerm2 after enabling"
			prompt.Choices = []teamspawn.PaneSetupChoice{{Value: "retry", Label: "Try again", Description: "Verify the connection again"}}
		case "api-instructions":
			prompt.Description = "✓ it2 installed successfully\nAlmost done! Enable the Python API in iTerm2:\n\n  iTerm2 → Settings → General → Magic → Enable Python API\n\nAfter enabling, you may need to restart iTerm2.\nPress Enter when ready to verify…"
			prompt.Choices = []teamspawn.PaneSetupChoice{{Value: "verify", Label: "Press Enter when ready to verify…"}}
		}
		if step != "api-instructions" {
			if tmux {
				description := "Falls back to tmux for teammate panes"
				if step == "initial" {
					description = "Opens teammates in a separate tmux session"
				}
				prompt.Choices = append(prompt.Choices, teamspawn.PaneSetupChoice{Value: "tmux", Label: "Use tmux instead", Description: description})
			}
			prompt.Choices = append(prompt.Choices, teamspawn.PaneSetupChoice{Value: "cancel", Label: "Cancel", Description: "Skip teammate spawning for now"})
		}
		choice, err := callback(ctx, scope, prompt)
		if err != nil {
			return "", err
		}
		if choice == "cancel" || choice == "cancelled" || choice == "" {
			return "", errors.New("Teammate spawn cancelled - iTerm2 setup required")
		}
		if choice == "tmux" && tmux {
			if err = save("preferTmuxOverIterm2"); err != nil {
				return "", err
			}
			leader.preferTmux = true
			return redetect()
		}
		if choice == "install" || choice == "retry" && step == "install-failed" {
			if manager == "" {
				step, detail = "failed", "No Python package manager found (uvx, pipx, or pip)"
				continue
			}
			if _, err = callback(ctx, scope, teamspawn.PaneSetupPrompt{Step: "installing", Title: prompt.Title, Description: "Installing it2 using " + manager + "…\nThis may take a moment."}); err != nil {
				return "", err
			}
			job, err := nativeSkillWork(m, scope, func() (teamspawn.PaneSetupResult, error) { return leader.panes.InstallITerm(manager), nil })
			if err != nil {
				return "", err
			}
			result, err := job.Await(scope)
			if err != nil {
				return "", err
			}
			if !result.Success {
				step, detail = "install-failed", result.Error
				continue
			}
			step, detail = "api-instructions", ""
			continue
		}
		if choice != "verify" && choice != "retry" {
			return "", cooperative.ErrRestricted
		}
		if _, err = callback(ctx, scope, teamspawn.PaneSetupPrompt{Step: "verifying", Title: prompt.Title, Description: "Verifying it2 can communicate with iTerm2…"}); err != nil {
			return "", err
		}
		job, err := nativeSkillWork(m, scope, func() (teamspawn.PaneSetupResult, error) { return leader.panes.VerifyITerm(), nil })
		if err != nil {
			return "", err
		}
		result, err := job.Await(scope)
		if err != nil {
			return "", err
		}
		if !result.Success {
			step, detail = "failed", result.Error
			continue
		}
		if err = save("iterm2It2SetupComplete"); err != nil {
			return "", err
		}
		if _, err = callback(ctx, scope, teamspawn.PaneSetupPrompt{Step: "success", Title: prompt.Title, Description: "✓ iTerm2 split pane support is ready\nTeammates will now appear as split panes."}); err != nil {
			return "", err
		}
		wait, err := nativeSkillWork(m, scope, func() (struct{}, error) {
			timer := time.NewTimer(1500 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
				return struct{}{}, nil
			case <-ctx.Done():
				return struct{}{}, context.Cause(ctx)
			}
		})
		if err != nil {
			return "", err
		}
		if _, err = wait.Await(scope); err != nil {
			return "", err
		}
		return redetect()
	}
}
