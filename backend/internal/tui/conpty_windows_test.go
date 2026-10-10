//go:build windows

package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// This lane creates a real native pseudoconsole around the same fixture used
// by Unix PTY tests. Cross compilation alone does not execute this test.
func TestActualConPTYUnicodePasteApprovalCancellationResizeAndRestore(t *testing.T) {
	testConPTY(t, false)
}

func TestActualConPTYContextCancellationRestoresConsole(t *testing.T) {
	testConPTY(t, true)
}

func testConPTY(t *testing.T, cancelContext bool) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err = os.Mkdir(filepath.Join(home, ".fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, ".fixture", "tui-pty-home"), []byte("disposable TUI PTY home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inputRead, inputWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer inputRead.Close()
	defer inputWrite.Close()
	outputRead, outputWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer outputRead.Close()
	defer outputWrite.Close()
	var console windows.Handle
	if err = windows.CreatePseudoConsole(windows.Coord{X: 80, Y: 24}, windows.Handle(inputRead.Fd()), windows.Handle(outputWrite.Fd()), 0, &console); err != nil {
		t.Fatalf("native CreatePseudoConsole failed (requires Windows 10 1809 or newer): %v", err)
	}
	var captured lockedBuffer
	drainDone := make(chan struct{})
	go func() { defer close(drainDone); _, _ = io.Copy(&captured, outputRead) }()
	defer func() {
		// The output channel stays drained during ClosePseudoConsole, including
		// older Windows versions whose teardown waits for the last frame.
		windows.ClosePseudoConsole(console)
		outputRead.Close()
		select {
		case <-drainDone:
		case <-time.After(5 * time.Second):
			t.Error("ConPTY output reader did not stop")
		}
	}()
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		t.Fatal(err)
	}
	defer attributes.Delete()
	// Microsoft passes the opaque HPCON itself as lpValue, not &HPCON. Use
	// uintptr for that native handle so Go never treats it as a heap pointer.
	update := windows.NewLazySystemDLL("kernel32.dll").NewProc("UpdateProcThreadAttribute")
	result, _, callErr := update.Call(uintptr(unsafe.Pointer(attributes.List())), 0, windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, uintptr(console), unsafe.Sizeof(console), 0, 0)
	if result == 0 {
		t.Fatalf("pseudoconsole startup attribute: %v", callErr)
	}
	startup := windows.StartupInfoEx{ProcThreadAttributeList: attributes.List()}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	application, err := windows.UTF16PtrFromString(binary)
	if err != nil {
		t.Fatal(err)
	}
	command, err := windows.UTF16FromString(windows.EscapeArg(binary) + " -test.run=^TestTerminalFixtureProcess$ -test.count=1")
	if err != nil {
		t.Fatal(err)
	}
	// Supply only disposable state and required OS paths; never inherit saved
	// provider configuration, credentials or the developer's real home.
	entries := []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "APPDATA=" + filepath.Join(home, "AppData", "Roaming"), "LOCALAPPDATA=" + filepath.Join(home, "AppData", "Local"), "TEMP=" + home, "TMP=" + home, "TERM=xterm-256color", "TZ=UTC", "TUI_PTY_HELPER=1"}
	for _, key := range []string{"SystemRoot", "WINDIR", "PATH"} {
		if value := os.Getenv(key); value != "" {
			entries = append(entries, key+"="+value)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return strings.ToUpper(entries[i]) < strings.ToUpper(entries[j]) })
	environment := utf16.Encode([]rune(strings.Join(entries, "\x00") + "\x00\x00"))
	var process windows.ProcessInformation
	if err = windows.CreateProcess(application, &command[0], nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT, &environment[0], nil, &startup.StartupInfo, &process); err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process.Process)
	defer windows.CloseHandle(process.Thread)
	defer func() {
		if status, _ := windows.WaitForSingleObject(process.Process, 0); status != windows.WAIT_OBJECT_0 {
			_ = windows.TerminateProcess(process.Process, 1)
			_, _ = windows.WaitForSingleObject(process.Process, 5000)
		}
	}()
	// The system owns its channel references after child creation. Closing
	// these parent references lets the reader observe broken-pipe EOF.
	inputRead.Close()
	outputWrite.Close()
	deadline := time.Now().Add(20 * time.Second)
	eventsPath := filepath.Join(home, ".fixture", "events.jsonl")
	readEvents := func() []map[string]json.RawMessage {
		data, _ := os.ReadFile(eventsPath)
		rows := strings.Split(string(data), "\n")
		var events []map[string]json.RawMessage
		for _, row := range rows[:len(rows)-1] {
			var event map[string]json.RawMessage
			if json.Unmarshal([]byte(row), &event) == nil {
				events = append(events, event)
			}
		}
		return events
	}
	hasEvent := func(kind string) bool {
		for _, event := range readEvents() {
			if string(event["kind"]) == fmt.Sprintf("%q", kind) {
				return true
			}
		}
		return false
	}
	wait := func(predicate func() bool, description string) {
		t.Helper()
		for !predicate() {
			if time.Now().After(deadline) {
				t.Fatalf("ConPTY deadline waiting for %s\n%s", description, captured.String())
			}
			if status, _ := windows.WaitForSingleObject(process.Process, 0); status == windows.WAIT_OBJECT_0 {
				t.Fatalf("ConPTY child exited while waiting for %s\n%s", description, captured.String())
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	write := func(value string) {
		t.Helper()
		done := make(chan error, 1)
		go func() { _, err := io.WriteString(inputWrite, value); done <- err }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Until(deadline)):
			inputWrite.Close()
			t.Fatal("ConPTY input write deadline")
		}
	}
	wait(func() bool { return strings.Contains(captured.String(), "Go TUI fixture") }, "initial view")
	if cancelContext {
		write("context-cancel\r")
	} else {
		write("\x1b[200~你好👨‍👩‍👧‍👦\n第二行\tX\x1b[201~")
		wait(func() bool { return strings.Contains(captured.String(), "你好") }, "Unicode paste")
		if hasEvent("submit") {
			t.Fatal("bracketed paste submitted a turn")
		}
		write("\\\r第三行\r")
		wait(func() bool { return hasEvent("permission-ready") }, "tool permission")
		var submitted []Input
		for _, event := range readEvents() {
			if string(event["kind"]) == `"submit"` {
				var input Input
				if err = json.Unmarshal(event["input"], &input); err != nil {
					t.Fatal(err)
				}
				submitted = append(submitted, input)
			}
		}
		if len(submitted) != 1 || submitted[0].Text != "你好👨‍👩‍👧‍👦\n第二行    X\n第三行" {
			t.Fatalf("unexpected native submission: %+v", submitted)
		}
		write("y")
		wait(func() bool { return hasEvent("finished") && strings.Contains(captured.String(), "FILE_CREATED") }, "stream/tool completion")
		if err = windows.ResizePseudoConsole(console, windows.Coord{X: 40, Y: 10}); err != nil {
			t.Fatal(err)
		}
		wait(func() bool {
			for _, event := range readEvents() {
				if string(event["kind"]) == `"resize"` && string(event["width"]) == "40" && string(event["height"]) == "10" {
					return true
				}
			}
			return false
		}, "actual resize event")
		write("cancel\r")
		wait(func() bool { return strings.Contains(captured.String(), "partial_cancel") }, "partial stream")
		write("\x1b")
		wait(func() bool { return hasEvent("cancelled") }, "Escape cancellation")
		write("\x04\x04")
	}
	status, err := windows.WaitForSingleObject(process.Process, uint32(max(1, time.Until(deadline).Milliseconds())))
	if err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("ConPTY did not exit: status=%d error=%v\n%s", status, err, captured.String())
	}
	var exit uint32
	if err = windows.GetExitCodeProcess(process.Process, &exit); err != nil || exit != 0 {
		t.Fatalf("ConPTY exit %d: %v\n%s", exit, err, captured.String())
	}
	closed := 0
	restored := false
	for _, event := range readEvents() {
		if string(event["kind"]) == `"closed"` {
			closed++
		}
		if string(event["kind"]) == `"terminal-state-restored"` && string(event["restored"]) == "true" {
			restored = true
		}
	}
	if !restored || closed != 1 {
		t.Fatalf("native console state not restored exactly once: restored=%v closed=%d", restored, closed)
	}
	if cancelContext && !hasEvent("expected-context-error") {
		t.Fatal("context cancellation error was not propagated")
	}
	if err = os.WriteFile(filepath.Join(home, ".fixture", "terminal.raw"), []byte(captured.String()), 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
