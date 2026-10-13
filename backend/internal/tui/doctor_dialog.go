package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
)

type doctorLoadedMsg struct {
	text    string
	failure error
}
type doctorDialogModel struct {
	state     *State
	ctx       context.Context
	load      func(context.Context) (string, error)
	loaded    chan struct{}
	result    doctorLoadedMsg
	failure   error
	dismissed bool
}

func (m *doctorDialogModel) Init() tea.Cmd {
	return func() tea.Msg {
		select {
		case <-m.loaded:
			return m.result
		case <-m.ctx.Done():
			return nil
		}
	}
}
func (m *doctorDialogModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.state.Width, m.state.Height = max(1, msg.Width), max(1, msg.Height)
	case doctorLoadedMsg:
		m.failure = msg.failure
		if msg.failure != nil {
			msg.text = "Diagnostics\n\nFailed to load diagnostics: " + msg.failure.Error()
		}
		m.state.Doctor = &DoctorPanel{Text: msg.text}
	case tea.KeyPressMsg:
		actions := m.state.handleDoctorKey(terminalKey(msg), time.Now())
		if m.state.Doctor == nil || len(actions) > 0 && m.state.Closing {
			m.dismissed = true
			return m, tea.Quit
		}
	}
	return m, nil
}
func (m *doctorDialogModel) View() tea.View {
	if m.state.Doctor == nil {
		return tea.NewView("")
	}
	return tea.NewView(strings.Join(m.state.Doctor.lines(m.state.Width, m.state.Height), "\n"))
}

// RunDoctorDialog is the non-ASK public doctor screen. Its source is loaded
// while the panel is already visible, so an unavailable peer cannot trap the
// user. All report producers are cancelled and joined before terminal restore.
func RunDoctorDialog(ctx context.Context, load func(context.Context) (string, error), options Options) (err error) {
	if ctx == nil || load == nil || options.Input == nil || options.Output == nil {
		return errors.New("Doctor requires explicit context/report/terminal I/O")
	}
	terminals, e := captureTerminals(options.Input, options.Output)
	if e != nil {
		return e
	}
	defer func() {
		for _, terminal := range terminals {
			err = errors.Join(err, term.Restore(terminal.file.Fd(), terminal.state))
		}
	}()
	life, cancel := context.WithCancel(ctx)
	defer cancel()
	state := NewState(nil, max(options.Width, 80), max(options.Height, 24))
	state.Doctor = &DoctorPanel{Text: "Diagnostics\n\nLoading diagnostics..."}
	if options.Keybindings != nil {
		state.SetKeybindings(options.Keybindings)
	}
	model := &doctorDialogModel{state: state, ctx: life, load: load, loaded: make(chan struct{})}
	go func() {
		text, failure := load(life)
		model.result = doctorLoadedMsg{text, failure}
		close(model.loaded)
	}()
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(options.Input), tea.WithOutput(options.Output), tea.WithEnvironment(options.Environment), tea.WithWindowSize(state.Width, state.Height))
	_, err = program.Run()
	cancel()
	// A diagnostic may own stdio/HTTP work; escape is not an orphan shortcut.
	<-model.loaded
	if err == nil && !model.dismissed {
		err = model.failure
	}
	if ctx.Err() != nil {
		err = errors.Join(err, context.Cause(ctx))
	}
	return err
}
