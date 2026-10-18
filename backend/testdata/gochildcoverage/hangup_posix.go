//go:build !windows

package gochildcoverage

import (
	"fmt"
	"os"
	"os/signal"
	"runtime/coverage"
	"sync"
	"syscall"
)

var hangupInstrumentation struct {
	sync.Mutex
	directory string
}

// A normal os.Exit runs Go's automatic coverage exit hook. Hold this lock
// through that complete real exit or through snapshot/reset/reraise; a second
// writer must not create a temporary file just before the signal kills it.
var nativeCoverageExit sync.Mutex

func InstallHangupSignal() error {
	directory := os.Getenv("GOCOVERDIR")
	if directory == "" {
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	directory, err = HangupDirectory(executable, directory)
	if err != nil {
		return err
	}
	hangupInstrumentation.Lock()
	defer hangupInstrumentation.Unlock()
	if installed := hangupInstrumentation.directory; installed != "" {
		if installed != directory {
			return fmt.Errorf("native fixture coverage cannot replace its attested process directory")
		}
		return nil
	}
	hangupInstrumentation.directory = directory
	hangup := make(chan os.Signal, 1)
	signal.Notify(hangup, syscall.SIGHUP)
	go func() {
		<-hangup
		nativeCoverageExit.Lock()
		// Write only genuine atomic runtime counters. Do not call business
		// Close, fabricate hits, or replace the original signal termination.
		if err := coverage.WriteCountersDir(directory); err != nil {
			fmt.Fprintln(os.Stderr, "native fixture actual coverage snapshot:", err)
		}
		signal.Stop(hangup)
		signal.Reset(syscall.SIGHUP)
		if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
			fmt.Fprintln(os.Stderr, "native fixture original hangup:", err)
		}
		// Do not unlock while native signal delivery is still pending. A normal
		// Exit racing that delivery must not start another coverage exit hook.
	}()
	return nil
}

// Exit is bound by the native fixture adapter only after successful process
// attestation. Without that opt-in it retains the ordinary direct os.Exit.
func Exit(code int) {
	hangupInstrumentation.Lock()
	active := hangupInstrumentation.directory != ""
	hangupInstrumentation.Unlock()
	if active {
		nativeCoverageExit.Lock()
	}
	os.Exit(code)
}
