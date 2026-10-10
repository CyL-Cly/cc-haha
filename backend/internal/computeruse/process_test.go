package computeruse

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestClosingServiceCancelsActualPrivateNativeProcessTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macOS native helper process groups")
	}
	options := fixtureOptions(t)
	childFile := filepath.Join(options.HomeDir, "child.pid")
	options.Environment["CC_HAHA_CU_CHILD_PID_FILE"] = childFile
	script := filepath.Join(options.HomeDir, "private-permission-supervisor")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n/bin/sleep 60 &\nprintf '%s' \"$!\" > \"$CC_HAHA_CU_CHILD_PID_FILE\"\nwait\n"), 0700); err != nil {
		t.Fatal(err)
	}
	service := fixtureService(t, options)
	done := make(chan error, 1)
	go func() { _, err := service.run(context.Background(), script, nil, nil); done <- err }()
	deadline := time.Now().Add(3 * time.Second)
	childPID := 0
	for childPID == 0 {
		raw, _ := os.ReadFile(childFile)
		childPID, _ = strconv.Atoi(string(raw))
		if time.Now().After(deadline) {
			t.Fatal("private native worker did not start")
		}
		time.Sleep(time.Millisecond)
	}
	if !processAlive(childPID) {
		t.Fatal("private worker never ran")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled native command reported success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("owned native supervisor did not finish on Close")
	}
	// A killed worker can briefly retain a zombie pid while launchd reaps it;
	// it must never retain a running process or stdio after the owner closes.
	probe := exec.Command("/bin/ps", "-o", "stat=", "-p", strconv.Itoa(childPID))
	probe.Env = []string{"HOME=" + options.HomeDir, "PATH=/usr/bin:/bin"}
	state, _ := probe.Output()
	if trim(string(state)) != "" && !strings.HasPrefix(trim(string(state)), "Z") {
		t.Fatalf("native child still running after owner cleanup: %s", state)
	}
}
