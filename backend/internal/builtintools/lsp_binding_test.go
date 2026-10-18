package builtintools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/lspruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

func TestEditLSPRequiresActualSharedLedgerAndZeroServerManagerKeepsFileResult(t *testing.T) {
	s, owner, home, _ := fixture(t)
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
		t.Fatal("Edit accepted a foreign LSP ledger")
	}
	manager, err := lspruntime.New(lspruntime.Options{Owner: owner, Binding: s.options.NativeBinding, CWD: home, PID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	manager.Configure(nil, nil)
	options.LSP = manager
	actual, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "actual-edit.txt")
	if err := os.WriteFile(path, []byte("old\nline\n"), 0600); err != nil {
		t.Fatal(err)
	}
	call(t, actual, owner, "Read", input(`{"file_path":"actual-edit.txt"}`))
	call(t, actual, owner, "Edit", input(`{"file_path":"actual-edit.txt","old_string":"old","new_string":"new"}`))
	if err := manager.JoinNotifications(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new\nline\n" {
		t.Fatal("LSP changed actual Edit result", err, string(data))
	}
}
