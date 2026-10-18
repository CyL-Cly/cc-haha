//go:build darwin

package clibootstrap

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
)

func TestNativeCommandMarketplaceReadKeepsActualPrivateFIFOAndNativeFileAuthority(t *testing.T) {
	m := nativeCommandInitFixture(t, "http://127.0.0.1:1")
	path := filepath.Join(m.Settings.plugins.directories.Primary, "known_marketplaces.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var actual plugincli.Result
	returned := make(chan error, 1)
	go func() {
		returned <- m.Run(ctx, func(scope *cooperative.Scope) error {
			var err error
			actual, err = m.PluginCommand(ctx, scope, plugincli.Command{Action: "marketplace-list", JSON: true})
			return err
		})
	}()
	// A blocking FIFO open has no observable descriptor until its reader and
	// writer rendezvous. Hold the real writer open without data before lsof.
	var file *os.File
	var err error
	for ctx.Err() == nil {
		file, err = os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.ENXIO) {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if file == nil {
		t.Fatal("actual FIFO reader did not rendezvous", ctx.Err())
	}
	defer func() { _, _ = file.Write([]byte(`{}`)); _ = file.Close() }()
	// Observe a genuine blocked kernel file read, independent of native Trace.
	// The old direct-read implementation also reaches this same descriptor.
	physicalPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	readEntered := false
	for ctx.Err() == nil {
		probe := exec.CommandContext(ctx, "/usr/sbin/lsof", "-a", "-p", strconv.Itoa(os.Getpid()), "-Ffn")
		data, _ := probe.Output()
		if strings.Contains(string(data), "n"+physicalPath+"\n") {
			readEntered = true
			break
		}
		select {
		case err := <-returned:
			t.Fatal("actual private FIFO read returned before writer", err)
		default:
		}
	}
	if !readEntered {
		t.Fatal("actual FIFO descriptor did not enter", ctx.Err())
	}
	m.Native.mu.Lock()
	sampledNative := false
	for _, event := range m.Native.Trace {
		if event.Operation == "readFile" && event.Path == path && event.Outcome == "native" {
			sampledNative = true
		}
	}
	m.Native.mu.Unlock()
	// Always release the real pipe before asserting so old-path RED is cleaned.
	_, writeError := file.Write([]byte(`{}`))
	closeError := file.Close()
	if writeError != nil || closeError != nil {
		t.Fatal(writeError, closeError)
	}
	select {
	case err := <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("actual marketplace read did not settle", ctx.Err())
	}
	if actual.ExitCode != 0 || actual.Stdout != "[]\n" || actual.Stderr != "" {
		t.Fatal("actual original read/formatter", actual)
	}
	if !sampledNative {
		t.Fatal("actual native command bypassed its native file authority for held known-marketplace read")
	}
	m.Close()
	if count, _ := m.nativeInitRefs.Snapshot(); count != 0 {
		t.Fatal("closed actual read retained native delivery references", count)
	}
	if err := m.Run(ctx, func(*cooperative.Scope) error { return nil }); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("closed native command retained caller", err)
	}
}

func TestNativeCommandMarketplaceReadRejectsSubstitutedFactoryAndForeignScope(t *testing.T) {
	m := nativeCommandInitFixture(t, "http://127.0.0.1:1")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	command := plugincli.Command{Action: "marketplace-list", JSON: true}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(ctx, func(scope *cooperative.Scope) error {
		if _, err := m.PluginCommand(ctx, scope, command); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign command file consumer admitted", err)
		}
		original := m.Owner
		m.Owner = foreign
		_, err := m.PluginCommand(ctx, scope, command)
		m.Owner = original
		if !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("substituted actual factory Owner fell back to finite consumer", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var retained *cooperative.Scope
	if err := m.Run(ctx, func(scope *cooperative.Scope) error {
		retained = scope
		original := m.Native
		m.Native = &NativeBoundary{NativeProcess: true}
		_, err := m.PluginCommand(ctx, scope, command)
		m.Native = original
		if !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("substituted Native field fell back to finite consumer", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PluginCommand(ctx, retained, command); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("retained file consumer Scope admitted", err)
	}
}
