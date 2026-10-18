package builtinwrite

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/lspruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

func TestWriteLSPRequiresActualSharedLedgerAndZeroServerManagerKeepsFileResult(t *testing.T) {
	s, owner, _, _, home := fixture(t)
	foreignOwner := cooperative.New()
	defer foreignOwner.Close()
	foreignRefs := &ownerasync.NativeReferences{}
	foreignBinding, err := foreignRefs.Bind(foreignOwner)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := lspruntime.New(lspruntime.Options{Owner: foreignOwner, Binding: foreignBinding, CWD: home, PID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Close(context.Background())
	foreign.Configure(nil, nil)
	options := s.options
	options.LSP = foreign
	if _, err := New(options); err == nil {
		t.Fatal("Write accepted a foreign LSP ledger")
	}
	manager, err := lspruntime.New(lspruntime.Options{Owner: owner, Binding: s.options.NativeBinding, CWD: home, PID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	manager.Configure(nil, nil)
	options.LSP = manager
	options.HostState = func(*cooperative.Scope) (Host, error) { return Host{NonInteractive: true, LSPEnabled: true}, nil }
	actual, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "actual-write.txt")
	run(t, owner, func(scope *cooperative.Scope) error {
		_, err := actual.Call(context.Background(), input(path, "actual zero-server write\n"), scope)
		return err
	})
	if err := manager.JoinNotifications(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "actual zero-server write\n" {
		t.Fatal("LSP changed actual Write result", err, string(data))
	}
}
