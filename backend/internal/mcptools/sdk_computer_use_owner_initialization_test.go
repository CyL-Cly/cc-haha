package mcptools

import (
	"context"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"testing"
)

// The exact old CU production compiles with this regression. It fails because
// its first real module is already marked linked/evaluated inside the caller.
func TestComputerUseColdModuleAdmissionDefersNamespaceUntilRealOwnerInitialization(t *testing.T) {
	s, refs := inactiveComputerUseService(t)
	if err := s.options.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for key := computerUseCleanupModule; key < computerUseModuleCount; key++ {
			entry, err := s.computerUseModuleEntry(scope, key)
			if err != nil {
				return err
			}
			if entry.record.linked || entry.record.evaluated {
				t.Error("cold namespace was pre-marked linked/evaluated before real Owner initialization")
			}
			if err := entry.record.check(scope); !errors.Is(err, cooperative.ErrRestricted) {
				t.Error("unevaluated namespace admitted exports", err)
			}
			one, err := s.importComputerUseModule(scope, key)
			if err != nil {
				return err
			}
			two, err := s.importComputerUseModule(scope, key)
			if err != nil {
				return err
			}
			if one == two || one.Identity() == two.Identity() {
				return errors.New("cold imports shared a public Promise")
			}
			first, err := one.Await(scope)
			if err != nil {
				return err
			}
			second, err := two.Await(scope)
			if err != nil {
				return err
			}
			if first != second || first != entry.record.namespace || s.inactiveComputerUse.modules[key] != entry || !entry.record.linked || !entry.record.evaluated {
				return errors.New("cold shared initialization did not produce one canonical namespace")
			}
			warm, err := s.importComputerUseModule(scope, key)
			if err != nil {
				return err
			}
			third, err := warm.Await(scope)
			if err != nil {
				return err
			}
			if third != first || s.inactiveComputerUse.modules[key] != entry {
				return errors.New("warm import replaced initialized module")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n, _ := refs.Snapshot(); n != 0 {
		t.Fatal("module initialization manufactured physical work", n)
	}
}
func TestComputerUseCancelledModuleAdmissionDoesNotPoisonNextImporter(t *testing.T) {
	s, refs := inactiveComputerUseService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.options.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		cancel()
		if _, err := s.computerUseModuleEntry(scope, computerUseCleanupModule); !errors.Is(err, context.Canceled) {
			t.Error("cancelled construction admitted", err)
		}
		if s.inactiveComputerUse.modules[computerUseCleanupModule] != nil {
			t.Error("failed unadmitted placeholder remained cached")
		}
		return nil
	}); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := s.options.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		pending, err := s.importComputerUseModule(scope, computerUseCleanupModule)
		if err != nil {
			return err
		}
		namespace, err := pending.Await(scope)
		if err != nil {
			return err
		}
		if namespace != s.inactiveComputerUse.modules[computerUseCleanupModule].record.namespace {
			return fmt.Errorf("fresh legitimate importer lost namespace")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n, _ := refs.Snapshot(); n != 0 {
		t.Fatal("cancelled initialization manufactured physical work", n)
	}
}
