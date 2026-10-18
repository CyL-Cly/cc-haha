package tui

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

type queuedExpandedViewSession struct {
	*testSession
	owner   *cooperative.Owner
	values  []string
	failure error
}

func (s *queuedExpandedViewSession) EnqueueTerminalExpandedView(ctx context.Context, value string) (TerminalExpandedViewCommit, error) {
	return s.owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if !scope.BelongsTo(s.owner) {
			return cooperative.ErrRestricted
		}
		s.values = append(s.values, value)
		return s.failure
	})
}

func TestTerminalExpandedViewEnqueuesInInputOrderBeforeAsyncReceipts(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	held, release := make(chan struct{}), make(chan struct{})
	receipt, err := owner.SubmitIndependent(context.Background(), cooperative.Read, func(*cooperative.Scope) error {
		close(held)
		<-release
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	<-held
	session := &queuedExpandedViewSession{testSession: &testSession{}, owner: owner}
	m := &terminalModel{ctx: context.Background(), state: NewState(nil, 100, 36), session: session, expandedView: "none"}
	m.state.setTerminalExpandedView("tasks")
	first := m.syncTerminalExpandedView()
	m.state.setTerminalExpandedView("teammates")
	second := m.syncTerminalExpandedView()
	m.state.setTerminalExpandedView("none")
	third := m.syncTerminalExpandedView()
	close(release)
	if err = receipt.Wait(); err != nil {
		t.Fatal(err)
	}
	// Tea may schedule waiting commands in another order. The actual commits
	// were already admitted in the original input order.
	for _, wait := range []func() any{func() any { return third() }, func() any { return second() }, func() any { return first() }} {
		completed, ok := wait().(terminalExpandedViewReturnedMsg)
		if !ok || completed.err != nil {
			t.Fatal("Missing real commit completion", completed.err)
		}
	}
	m.turns.Wait()
	if !reflect.DeepEqual(session.values, []string{"tasks", "teammates", "none"}) {
		t.Fatal("Async receipt order changed committed input order", session.values)
	}
}

func TestTerminalExpandedViewCommitFailureAndCancelledTeardownProjection(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	failure := errors.New("actual expanded-view write failed")
	session := &queuedExpandedViewSession{testSession: &testSession{}, owner: owner, failure: failure}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &terminalModel{ctx: ctx, state: NewState(nil, 100, 36), session: session, expandedView: "none"}
	m.state.setTerminalExpandedView("tasks")
	m.Update(m.syncTerminalExpandedView()())
	if m.state.Status != failure.Error() {
		t.Fatal("Actual commit error did not reach the live terminal", m.state.Status)
	}
	cancel()
	m.Update(terminalExpandedViewReturnedMsg{context.Canceled})
	if m.state.Status != failure.Error() {
		t.Fatal("Retired commit result overwrote the terminal during cancellation", m.state.Status)
	}
	m.turns.Wait()
}
