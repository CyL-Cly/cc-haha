package tui

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestManagedPolicyDialogActualStateKeyboardAndMissingDescriptors(t *testing.T) {
	for _, name := range []string{"accept", "reject", "escape"} {
		t.Run(name, func(t *testing.T) {
			request := ManagedPolicyPermission("owned", []string{"apiKeyHelper", "PRIVATE_ENV", "hooks"})
			state := NewState(nil, 80, 24)
			state.Apply(Event{Kind: PermissionAsked, Permission: request})
			rendered := state.View("Claude Code", false).Content
			if !strings.Contains(rendered, "Managed settings require approval") || !strings.Contains(rendered, "apiKeyHelper") || !strings.Contains(rendered, "No, exit Claude Code") {
				t.Fatal("actual renderer lost names/choice", rendered)
			}
			key := "1"
			if name == "reject" {
				key = "2"
			}
			if name == "escape" {
				key = "esc"
			}
			actions := state.HandleKey(Key{Name: key}, time.Now())
			if len(actions) != 1 || actions[0].kind != actionPermission || actions[0].permission != "owned" || actions[0].decision.Allowed != (name == "accept") {
				t.Fatal("actual permission keyboard decision", actions)
			}
		})
	}
	if _, err := PromptManagedSettings(context.Background(), nil, nil, "owned", nil); err == nil {
		t.Fatal("missing descriptors granted or fell back to real stdin")
	}
	model := &managedPolicyDialog{state: NewState(nil, 80, 24)}
	if _, cmd := model.Update(tea.WindowSizeMsg{Width: 92, Height: 30}); cmd != nil || model.state.Width != 92 || model.state.Height != 30 {
		t.Fatal("actual terminal resize lost")
	}
}

type managedMountSession struct {
	*testSession
	owner *cooperative.Owner
	order []string
	bound chan struct{}
}

func (s *managedMountSession) SubscribeForegroundTasks(ctx context.Context, listener func(bool)) (bool, func(), error) {
	err := s.owner.Run(ctx, func(*cooperative.Scope) error { s.order = append(s.order, "owner-mount"); return nil })
	return false, func() {}, err
}
func (s *managedMountSession) BindManagedPolicyConsent(ctx context.Context, emit Emit) ([]*PermissionRequest, func(), error) {
	s.order = append(s.order, "managed-bind")
	close(s.bound)
	return []*PermissionRequest{ManagedPolicyPermission("owned-managed", []string{"apiKeyHelper"})}, func() {}, nil
}
func TestManagedPolicyRunBindsAfterOwnerMountAndConsumesRealReject(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	owner := cooperative.New()
	defer owner.Close()
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	base := &testSession{permission: func(_ context.Context, id string, decision PermissionDecision) error {
		if id != "owned-managed" || decision.Allowed {
			return errors.New("wrong managed policy input decision")
		}
		return ErrManagedPolicyConsentDeclined
	}}
	session := &managedMountSession{testSession: base, owner: owner, bound: make(chan struct{})}
	var output lockedBuffer
	done := make(chan error, 1)
	go func() { done <- Run(ctx, session, testOptions(input, &output)) }()
	select {
	case <-session.bound:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// A user answers after the actual renderer has shown the prompt. The
	// subscription is installed before Program.Run; it is not a render ack.
	renderTick := time.NewTicker(time.Millisecond)
	defer renderTick.Stop()
	for !strings.Contains(output.String(), "Managed settings require approval") {
		select {
		case <-renderTick.C:
		case err := <-done:
			t.Fatal("terminal exited before rendering managed consent", err, output.String())
		case <-ctx.Done():
			t.Fatal("actual renderer never showed policy decision", ctx.Err(), output.String())
		}
	}
	if _, err := io.WriteString(writer, "2"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrManagedPolicyConsentDeclined) {
			t.Fatal("terminal rejection did not retain exit failure", err)
		}
	case <-ctx.Done():
		t.Fatal("real terminal reject did not return", ctx.Err())
	}
	if !reflect.DeepEqual(session.order, []string{"owner-mount", "managed-bind"}) || base.closed.Load() != 1 {
		t.Fatal("consent mount/cleanup sequence changed", session.order, base.closed.Load())
	}
	if !strings.Contains(output.String(), "Managed settings require approval") {
		t.Fatal("actual renderer never showed policy decision", output.String())
	}
}
