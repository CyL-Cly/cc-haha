package tui

import (
	"context"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type teammatesChangedMsg struct{ tasks protocol.JSONObject }

func (m *terminalModel) teammateCommand(action TeammateAction) tea.Cmd {
	return func() tea.Msg {
		if !m.beginTurn() {
			return actionReturnedMsg{context.Canceled}
		}
		defer m.endTurn()
		host, ok := m.session.(TeammateSession)
		if !ok {
			return actionReturnedMsg{&UnsupportedAction{Action: action.Kind}}
		}
		return actionReturnedMsg{host.ControlTeammate(m.ctx, action)}
	}
}

func (m *terminalModel) bindTeammates() (func(), error) {
	source, ok := m.session.(TeammateSession)
	if !ok {
		return nil, nil
	}
	var mu sync.Mutex
	var latest protocol.JSONObject
	events := []Event{}
	wake := make(chan struct{}, 1)
	current, unsubscribe, err := source.SubscribeTeammates(m.ctx, func(tasks protocol.JSONObject) {
		mu.Lock()
		latest = tasks.Clone()
		mu.Unlock()
		select {
		case wake <- struct{}{}:
		default:
		}
	})
	if err != nil {
		return nil, err
	}
	removeEvents := source.SubscribeTeammateEvents(func(event Event) error {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
		select {
		case wake <- struct{}{}:
		default:
		}
		return nil
	})
	m.state.SetTeammateTasks(current)
	life, cancel := context.WithCancel(m.ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-life.Done():
				return
			case <-wake:
				mu.Lock()
				snapshot := latest.Clone()
				pending := events
				events = nil
				mu.Unlock()
				if snapshot.IsObject() {
					m.program.Send(teammatesChangedMsg{snapshot})
				}
				for _, event := range pending {
					m.program.Send(actionEventMsg{event: event})
				}
			}
		}
	}()
	return func() {
		if removeEvents != nil {
			removeEvents()
		}
		if unsubscribe != nil {
			unsubscribe()
		}
		cancel()
		<-done
	}, nil
}
