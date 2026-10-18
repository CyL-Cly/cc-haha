package clibootstrap

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func nativeCommandInitFixture(t *testing.T, endpoint string) *MainCommand {
	t.Helper()
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"`+endpoint+`"},"futurePrivate":{"keep":true}}`)
	f.n.Environment["ANTHROPIC_BASE_URL"] = endpoint + "/launch"
	f.n.PreconnectHTTP = func(request *http.Request, client *http.Client) (*http.Response, error) {
		if request == nil || client == nil || request.Method != http.MethodHead || request.URL.String() != endpoint {
			t.Error("actual command preconnect escaped its exact loopback", request)
			return nil, os.ErrPermission
		}
		return client.Do(request)
	}
	m, err := NewMainCommand(MainCommandOptions{Arguments: []string{"plugin", "marketplace", "list", "--json"}, Native: f.n, Platform: runtime.GOOS, PID: os.Getpid(), Version: "private-command-init", NonInteractive: true, Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m
}

func TestNativeCommandInitLeafFactoryAdmissionAndHeldHEADClose(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	var heads atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodHead || request.Header.Get("Authorization") != "" || request.Header.Get("X-Api-Key") != "" || request.Header.Get("User-Agent") != "Bun/1.3.14" {
			t.Error("held real init HEAD changed its fetch contract", request.Method, request.Header)
		}
		if heads.Add(1) == 1 {
			close(started)
		}
		<-request.Context().Done()
		close(canceled)
	}))
	defer peer.Close()
	m := nativeCommandInitFixture(t, peer.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := m.Run(ctx, func(*cooperative.Scope) error { return nil }); err != nil || heads.Load() != 0 || m.nativeAPIPreconnect.fired {
		t.Fatal("leaf constructor/Run unexpectedly invoked real CLI init", err, heads.Load())
	}
	preCanceled, stop := context.WithCancel(ctx)
	stop()
	if err := m.BeginNativeAPIPreconnect(preCanceled); !errors.Is(err, context.Canceled) || m.nativeAPIPreconnect.fired {
		t.Fatal("pre-canceled caller acquired init effect", err)
	}
	if err := m.BeginNativeAPIPreconnect(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("actual held init HEAD not entered", ctx.Err())
	}
	if err := m.BeginNativeAPIPreconnect(ctx); err != nil {
		t.Fatal("same actual init once", err)
	}
	// An unrelated action finishes while the independent init HTTP is held.
	if err := m.Run(ctx, func(*cooperative.Scope) error { return nil }); err != nil {
		t.Fatal("held init HTTP blocked normal handler completion", err)
	}
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("normal command Close did not cancel/join real held HEAD", ctx.Err())
	}
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("real held HEAD socket survived closed command", ctx.Err())
	}
	if count, _ := m.nativeInitRefs.Snapshot(); count != 0 || heads.Load() != 1 {
		t.Fatal("normal command owned references/once unsettled", count, heads.Load())
	}
	if err := m.BeginNativeAPIPreconnect(ctx); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("closed command admitted late init", err)
	}
}

func TestNativeCommandInitRejectsManualForeignOwnerAndSubstitutedFactoryIdentities(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		t.Error("invalid command acquired real native init HTTP")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer peer.Close()
	m := nativeCommandInitFixture(t, peer.URL)
	ctx := context.Background()
	manual := &MainCommand{Owner: m.Owner, Native: m.Native, Settings: m.Settings}
	if err := manual.BeginNativeAPIPreconnect(ctx); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("manual facade forged actual init factory", err)
	}
	originalOwner := m.Owner
	foreign := cooperative.New()
	m.Owner = foreign
	if err := m.BeginNativeAPIPreconnect(ctx); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("foreign Owner replaced factory identity", err)
	}
	m.Owner = originalOwner
	foreign.Close()
	originalNative := m.Native
	m.Native = &NativeBoundary{NativeProcess: true}
	if err := m.BeginNativeAPIPreconnect(ctx); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("copied native boundary replaced factory identity", err)
	}
	m.Native = originalNative
	originalSettings := m.Settings
	other := nativeCommandInitFixture(t, peer.URL)
	m.Settings = other.Settings
	if err := m.BeginNativeAPIPreconnect(ctx); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("foreign settings service replaced factory identity", err)
	}
	m.Settings = originalSettings
	if m.nativeAPIPreconnect.fired {
		t.Fatal("rejected identities changed actual once gate")
	}
}
