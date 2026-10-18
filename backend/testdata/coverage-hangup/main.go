package main

import (
	"fmt"
	// Match the actual tagged CLI's business-package metadata volume. The
	// concurrent exit probe executes no application behavior or model request.
	_ "github.com/NanmiCoder/cc-haha/backend/internal/application"
	"github.com/NanmiCoder/cc-haha/backend/testdata/gochildcoverage"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	var normal chan os.Signal
	if os.Getenv("CC_HAHA_COVERAGE_NORMAL_ON_HANGUP") == "1" {
		normal = make(chan os.Signal, 1)
		signal.Notify(normal, syscall.SIGHUP)
	}
	if err := gochildcoverage.InstallHangupSignal(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("ACTUAL_ATOMIC_HANGUP_READY")
	if os.Getenv("CC_HAHA_COVERAGE_NORMAL_EXIT") == "1" {
		gochildcoverage.Exit(23)
	}
	if normal != nil {
		<-normal
		gochildcoverage.Exit(23)
	}
	select {}
}
