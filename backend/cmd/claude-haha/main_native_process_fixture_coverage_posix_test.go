//go:build native_process_fixture && !offline_native_fixture && !windows

package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/testdata/gochildcoverage"
)

func TestNativeFixtureCoverageAttestationAndOriginalHangupWaitStatus(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "actual-hangup-primitive")
	build := exec.CommandContext(ctx, "go", "build", "-p=1", "-o", binary, "../../testdata/coverage-hangup")
	gochildcoverage.Capture(t, build)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatal("actual non-test primitive build", err, string(out))
	}
	command := exec.CommandContext(ctx, binary)
	command.Env = os.Environ()
	gochildcoverage.Capture(t, command)
	directory := ""
	for _, value := range command.Env {
		if value, ok := strings.CutPrefix(value, "GOCOVERDIR="); ok {
			directory = value
		}
	}
	if directory == "" {
		t.Skip("actual atomic collector profile required for original-SIGHUP counter proof")
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill() }()
	reader := bufio.NewReader(stdout)
	var childOutput strings.Builder
	for {
		line, err := reader.ReadString('\n')
		childOutput.WriteString(line)
		if err != nil {
			t.Fatal("actual helper did not bind", childOutput.String(), err)
		}
		if strings.Contains(line, "ACTUAL_ATOMIC_HANGUP_READY") {
			break
		}
	}
	if err = command.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	err = command.Wait()
	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatal("original hangup must remain signal termination", err)
	}
	status, ok := exit.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGHUP {
		t.Fatal("actual original wait status changed", status)
	}
	counters, err := filepath.Glob(filepath.Join(directory, "covcounters.*"))
	if err != nil || len(counters) == 0 {
		t.Fatal("no genuine interrupted runtime counters", counters, err)
	}
	if _, err = nativeFixtureCoverageDirectory(binary, directory); err != nil {
		t.Fatal("actual original attestation rejected", err)
	}
	// Each negative starts from that actual compiler/runtime receipt and meta,
	// rather than a manufactured positive metadata shape.
	for _, profile := range []string{"unregistered-binary", "symlink-directory", "outside-raw-directory", "symlink-receipt", "non-atomic-metadata", "changed-source"} {
		t.Run(profile, func(t *testing.T) {
			sink := t.TempDir()
			copied := filepath.Join(sink, "raw", "child-negative")
			if err := os.MkdirAll(copied, 0700); err != nil {
				t.Fatal(err)
			}
			copyFile := func(source, target string) {
				bytes, err := os.ReadFile(source)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(target, bytes, 0600); err != nil {
					t.Fatal(err)
				}
			}
			manifestPath := filepath.Join(filepath.Dir(filepath.Dir(directory)), "manifest.json")
			copyFile(manifestPath, filepath.Join(sink, "manifest.json"))
			copyFile(filepath.Join(directory, "receipt.json"), filepath.Join(copied, "receipt.json"))
			metas, _ := filepath.Glob(filepath.Join(directory, "covmeta.*"))
			if len(metas) != 1 {
				t.Fatal(metas)
			}
			metaPath := filepath.Join(copied, filepath.Base(metas[0]))
			copyFile(metas[0], metaPath)
			path, actualBinary := copied, binary
			switch profile {
			case "unregistered-binary":
				actualBinary = filepath.Join(sink, "foreign")
				copyFile(binary, actualBinary)
				file, err := os.OpenFile(actualBinary, os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = io.WriteString(file, "unregistered byte mutation")
				_ = file.Close()
			case "symlink-directory":
				path = filepath.Join(sink, "raw", "child-link")
				if err := os.Symlink(copied, path); err != nil {
					t.Fatal(err)
				}
			case "outside-raw-directory":
				path = sink
			case "symlink-receipt":
				_ = os.Remove(filepath.Join(copied, "receipt.json"))
				if err := os.Symlink(filepath.Join(directory, "receipt.json"), filepath.Join(copied, "receipt.json")); err != nil {
					t.Fatal(err)
				}
			case "non-atomic-metadata":
				meta, _ := os.ReadFile(metaPath)
				meta[48] = 1 // reject edited set mode even with an atomic receipt
				_ = os.WriteFile(metaPath, meta, 0600)
			case "changed-source":
				manifest, _ := os.ReadFile(filepath.Join(sink, "manifest.json"))
				var value map[string]any
				_ = json.Unmarshal(manifest, &value)
				value["sourceSnapshot"].([]any)[0].(map[string]any)["sha256"] = strings.Repeat("0", 64)
				changed, _ := json.Marshal(value)
				_ = os.WriteFile(filepath.Join(sink, "manifest.json"), changed, 0600)
				// Keep the edited receipt's manifest digest internally consistent
				// so this negative reaches the actual source SHA comparison.
				receiptBytes, _ := os.ReadFile(filepath.Join(copied, "receipt.json"))
				var receipt map[string]any
				_ = json.Unmarshal(receiptBytes, &receipt)
				sum := sha256.Sum256(changed)
				receipt["manifestSha256"] = hex.EncodeToString(sum[:])
				receiptBytes, _ = json.Marshal(receipt)
				_ = os.WriteFile(filepath.Join(copied, "receipt.json"), receiptBytes, 0600)
			}
			if _, err := nativeFixtureCoverageDirectory(actualBinary, path); err == nil {
				t.Fatal("unattested instrumentation acquired signal hook")
			}
		})
	}
}

func TestNativeFixtureCoverageUninstrumentedAndNoOptInRetainOrdinarySignalBoundary(t *testing.T) {
	t.Setenv("GOCOVERDIR", "")
	if err := installNativeFixtureCoverageSignal(); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = nativeFixtureCoverageDirectory(executable, t.TempDir()); err == nil {
		t.Fatal("no atomic attestation acquired signal hook")
	}
}

func TestNativeFixtureCoverageNormalExitAndHangupDoNotLeaveInterruptedCounterFiles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "actual-concurrent-exit-primitive")
	build := exec.CommandContext(ctx, "go", "build", "-p=1", "-o", binary, "../../testdata/coverage-hangup")
	gochildcoverage.Capture(t, build)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatal("actual concurrent normal exit primitive build", err, string(out))
	}
	for index := 0; index < 32; index++ {
		command := exec.CommandContext(ctx, binary)
		command.Env = append(os.Environ(), "CC_HAHA_COVERAGE_NORMAL_ON_HANGUP=1")
		if index == 0 {
			command.Env = append(os.Environ(), "CC_HAHA_COVERAGE_NORMAL_EXIT=1")
		}
		gochildcoverage.Capture(t, command)
		directory := ""
		for _, entry := range command.Env {
			if value, ok := strings.CutPrefix(entry, "GOCOVERDIR="); ok {
				directory = value
			}
		}
		if directory == "" {
			t.Skip("actual atomic collector profile required for native normal-exit/SIGHUP race")
		}
		stdout, err := command.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		command.Stderr = os.Stderr
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err != nil || !strings.Contains(line, "ACTUAL_ATOMIC_HANGUP_READY") {
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatal("concurrent native helper did not bind actual metadata", line, err)
		}
		if index != 0 {
			if err := command.Process.Signal(syscall.SIGHUP); err != nil {
				t.Fatal(err)
			}
		}
		err = command.Wait()
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal("original normal code23 or native hangup lost", index, err)
		}
		status, ok := exit.Sys().(syscall.WaitStatus)
		normal := ok && status.Exited() && status.ExitStatus() == 23
		hangup := ok && status.Signaled() && status.Signal() == syscall.SIGHUP
		if !normal && !hangup || index == 0 && !normal {
			t.Fatal("actual original terminal waitstatus changed", index, status)
		}
		counters, err := filepath.Glob(filepath.Join(directory, "covcounters.*"))
		if err != nil || len(counters) == 0 {
			t.Fatal("actual exit lost genuine runtime counters", index, counters, err)
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if name != "receipt.json" && !strings.HasPrefix(name, "covmeta.") && !strings.HasPrefix(name, "covcounters.") {
				info, _ := entry.Info()
				t.Errorf("actual native exit left interrupted counter artifact iteration=%d waitstatus=%v name=%s size=%d", index, status, name, info.Size())
			}
		}
	}
}
