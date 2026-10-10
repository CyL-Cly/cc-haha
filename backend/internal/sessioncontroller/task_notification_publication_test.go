package sessioncontroller

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestOwnedTaskNotificationSubscriptionSnapshotsIdentityRetentionAndDisposal(t *testing.T) {
	c, _, owner := boundController(t)
	pending, err := NewPendingWriter(c.writer, owner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pending.Close(context.Background()) })
	var dispose func(*cooperative.Scope) error
	published := []string{}
	publish := func(scope *cooperative.Scope, command TaskNotificationCommand) (bool, error) {
		if !scope.BelongsTo(owner) {
			t.Fatal("publisher lost Owner proof")
		}
		published = append(published, text(command.Content))
		if len(command.Content) > 0 {
			command.Content[0] = 'x'
		}
		// Reentrant observation must not repeat the same queue-object bookend.
		if err := c.publishTaskNotificationOnOwner(scope, 0); err != nil {
			return true, err
		}
		return true, nil
	}
	if err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := c.ActivateOwned(scope, activation()); err != nil {
			return err
		}
		if err := c.EnqueueTaskNotificationOnOwner(scope, "", "initial"); err != nil {
			return err
		}
		var err error
		dispose, err = c.BindTaskNotificationsOnOwner(scope, publish)
		if err != nil {
			return err
		}
		if _, err = c.BindTaskNotificationsOnOwner(scope, publish); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal("active publisher overwritten", err)
		}
		if len(published) != 1 || len(c.queue) != 1 || text(c.queue[0].Content) != "initial" {
			t.Fatal("initial snapshot modified/dequeued command", published)
		}
		if err = c.EnqueueTaskNotificationOnOwner(scope, "", "new"); err != nil {
			return err
		}
		if len(published) != 2 || len(c.queue) != 2 || c.queue[0].Priority != "next" {
			t.Fatal("publication changed command order/priority", published)
		}
		if err = c.publishTaskNotificationOnOwner(scope, 0); err != nil {
			return err
		}
		if len(published) != 2 {
			t.Fatal("same binding duplicated snapshot")
		}
		if err = dispose(scope); err != nil {
			return err
		}
		if err = c.EnqueueTaskNotificationOnOwner(scope, "", "disposed"); err != nil {
			return err
		}
		if len(published) != 2 || len(c.queue) != 3 {
			t.Fatal("dispose lost command or published it")
		}
		// A fresh Source binder owns a fresh WeakSet and sees retained commands.
		dispose, err = c.BindTaskNotificationsOnOwner(scope, publish)
		if err != nil {
			return err
		}
		if len(published) != 5 || len(c.queue) != 3 {
			t.Fatal("fresh binding incorrectly reused old WeakSet", published)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err = foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := c.BindTaskNotificationsOnOwner(scope, publish); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal("foreign publisher accepted", err)
		}
		if err := dispose(scope); err == nil {
			t.Fatal("foreign disposal accepted")
		}
		if c.RootTaskNotificationsUseQueueOnOwner(scope) {
			t.Fatal("foreign Owner borrowed publication")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = owner.Run(context.Background(), dispose); err != nil {
		t.Fatal(err)
	}
}

func TestTaskNotificationForeignScopeDoesNotObserveLivePublisher(t *testing.T) {
	c, _, owner := boundController(t)
	foreign := cooperative.New()
	defer foreign.Close()
	publish := func(*cooperative.Scope, TaskNotificationCommand) (bool, error) { return false, nil }
	start, rootReady, foreignReady := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			close(rootReady)
			<-start
			for i := 0; i < 10000; i++ {
				dispose, err := c.BindTaskNotificationsOnOwner(scope, publish)
				if err != nil {
					return err
				}
				if err = dispose(scope); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
			close(foreignReady)
			<-start
			for i := 0; i < 10000; i++ {
				if _, err := c.BindTaskNotificationsOnOwner(scope, publish); !errors.Is(err, cooperative.ErrRestricted) {
					t.Error("foreign binding accepted", err)
				}
			}
			return nil
		}); err != nil {
			t.Error(err)
		}
	}()
	<-rootReady
	<-foreignReady
	close(start)
	wg.Wait()
}
