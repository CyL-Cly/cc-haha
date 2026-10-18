package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
)

var ErrManagedPolicyConsentDeclined = errors.New("managed settings rejected")

type ManagedPolicyConsentSource interface {
	BindManagedPolicyConsent(context.Context, Emit) ([]*PermissionRequest, func(), error)
}

func ManagedPolicyPermission(id string, names []string) *PermissionRequest {
	return &PermissionRequest{ID: id, ToolName: "ManagedSettingsSecurity", Title: "Managed settings require approval", Description: "Your organization has configured managed settings that could allow execution of arbitrary code or interception of your prompts and responses.\n\nSettings requiring approval:\n· " + strings.Join(names, "\n· ") + "\n\nOnly accept if you trust your organization's IT administration and expect these settings to be configured.", Choices: []PermissionChoice{
		{Key: "1", Label: "Yes, I trust these settings", Decision: PermissionDecision{Allowed: true}},
		{Key: "2", Label: "No, exit Claude Code", Decision: PermissionDecision{Allowed: false}},
	}}
}

// Non-model Commander actions have no REPL Session. Their native dialog still
// uses the same permission renderer and actual keyboard consumer as the REPL.
// Input/output must be the factory's explicit process/fixture descriptors.
type managedPolicyDialog struct {
	state    *State
	allowed  bool
	answered bool
}

func (m *managedPolicyDialog) Init() tea.Cmd { return nil }
func (m *managedPolicyDialog) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.state.Width, m.state.Height = message.Width, message.Height
	case tea.KeyPressMsg:
		for _, action := range m.state.HandleKey(terminalKey(message), time.Now()) {
			if action.kind == actionPermission {
				m.allowed, m.answered = action.decision.Allowed, true
				return m, tea.Quit
			}
			if action.kind == actionQuit {
				m.answered = true
				return m, tea.Quit
			}
		}
	}
	return m, nil
}
func (m *managedPolicyDialog) View() tea.View { return m.state.View("Claude Code", false) }
func PromptManagedSettings(ctx context.Context, input io.Reader, output io.Writer, id string, names []string) (allowed bool, err error) {
	if ctx == nil || input == nil || output == nil {
		return false, errors.New("managed settings dialog requires explicit terminal input/output")
	}
	terminals, err := captureTerminals(input, output)
	if err != nil {
		return false, err
	}
	defer func() {
		for _, terminal := range terminals {
			err = errors.Join(err, term.Restore(terminal.file.Fd(), terminal.state))
		}
	}()
	model := &managedPolicyDialog{state: NewState(nil, 80, 24)}
	model.state.Apply(Event{Kind: PermissionAsked, Permission: ManagedPolicyPermission(id, names)})
	// Use the existing terminal Run boundary: cancellation asks Tea to quit
	// normally and joins its input reader before restoring a real terminal.
	program := tea.NewProgram(model, tea.WithContext(context.WithoutCancel(ctx)), tea.WithoutSignalHandler(), tea.WithInput(input), tea.WithOutput(output), tea.WithWindowSize(80, 24))
	stop, joined := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case <-ctx.Done():
			program.Quit()
		case <-stop:
		}
	}()
	_, err = program.Run()
	close(stop)
	<-joined
	if ctx.Err() != nil {
		return false, errors.Join(err, context.Cause(ctx))
	}
	if err != nil {
		return false, err
	}
	if !model.answered {
		return false, io.EOF
	}
	return model.allowed, nil
}
