package clibootstrap

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/terminaleditor"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (m *MainPrint) nativePlanCommand(ctx context.Context, scope *cooperative.Scope, args string) (string, bool, error) {
	if m == nil || !scope.BelongsTo(m.Owner) || !m.options.Interactive || m.options.Native == nil || m.agentChild || m.teamWorker != nil || m.plans == nil {
		return "", false, cooperative.ErrRestricted
	}
	permission, err := m.Controller.PlanModeContext(scope)
	if err != nil {
		return "", false, err
	}
	argument := protocol.TrimJavaScriptString(args)
	if permission.String("mode") != "plan" {
		if err = m.transitionNativePlan(scope, true); err != nil {
			return "", false, err
		}
		return "Enabled plan mode", argument != "" && argument != "open", nil
	}
	content, path, err := m.plans.Read(scope, "")
	if err != nil {
		return "", false, err
	}
	var plan string
	_ = json.Unmarshal(content, &plan)
	if plan == "" {
		return "Already in plan mode. No plan written yet.", false, nil
	}
	if words := strings.FieldsFunc(argument, modelCommandWhitespace); len(words) > 0 && words[0] == "open" {
		editor := m.terminalExternalEditor()
		if editor != "" {
			if m.options.EditTerminalFile == nil {
				return "", false, mainCap("terminal file editor handoff")
			}
			environment := m.options.Native.nativeEnvironmentSnapshot()
			cwd := m.state.Snapshot().CWD
			future, e := nativeSkillWork(m, scope, func() (string, error) { return m.options.EditTerminalFile(ctx, path, editor, cwd, environment) })
			if e != nil {
				return "", false, e
			}
			message, e := future.Await(scope)
			if e != nil {
				return "", false, e
			}
			if message != "" {
				return "Failed to open plan in editor: " + message, false, nil
			}
		}
		return "Opened plan in editor: " + path, false, nil
	}
	editorName := ""
	if editor := m.terminalExternalEditor(); editor != "" {
		editorName = terminaleditor.DisplayName(editor)
	}
	output := tui.TerminalPlanDisplay(path, plan, editorName)
	return output, false, nil
}

func (m *MainPrint) terminalExternalEditor() string {
	if m.nativeExternalEditor == nil {
		editor := terminaleditor.Select(m.options.Native.nativeEnvironmentSnapshot(), m.options.Platform, m.options.Native.stat, m.state.Snapshot().CWD)
		m.nativeExternalEditor = &editor
	}
	return *m.nativeExternalEditor
}

// TerminalPromptEditor samples the current session at the actual Ctrl-G handoff
// on the existing process Owner. Editor selection shares plan/keybindings' memo;
// execution stays in the terminal's existing tea.Exec handoff.
func (m *MainPrint) TerminalPromptEditor(ctx context.Context) (*tui.ExternalEditor, error) {
	if m == nil || m.Owner == nil {
		return nil, cooperative.ErrRestricted
	}
	var result *tui.ExternalEditor
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.options.Native == nil || !m.options.Interactive {
			return nil
		}
		editor := m.terminalExternalEditor()
		if editor != "" {
			result = &tui.ExternalEditor{Editor: editor, Directory: m.state.Snapshot().CWD, TempDir: m.options.Native.Temp, Environment: nativeSkillEnvironment(m.options.Native.nativeEnvironmentSnapshot())}
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}
