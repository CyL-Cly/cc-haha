package computeruse

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Only the external Swift/OS engine is replaced. Production Go resolution,
// file creation, fd 3 passing, AF_UNIX framing and process cleanup stay real.
func TestComputerUseFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_CU_NATIVE_FIXTURE") != "1" {
		return
	}
	args := []string{}
	for index, arg := range os.Args {
		if arg == "--" {
			args = os.Args[index+1:]
			break
		}
	}
	if len(args) == 0 {
		os.Exit(31)
	}
	log := func(value any) {
		raw, _ := json.Marshal(value)
		file, err := os.OpenFile(os.Getenv("CC_HAHA_CU_NATIVE_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			os.Exit(32)
		}
		_, _ = file.Write(append(raw, '\n'))
		_ = file.Close()
	}
	if args[0] == "attest_daemon_peer" {
		// Hermetic authentication boundary: prove the actual connected socket
		// was duplicated, while signed Security.framework remains the real
		// production verifier. This process never claims a real signature.
		file := os.NewFile(3, "peer")
		info, err := file.Stat()
		if err != nil || info.Mode()&os.ModeSocket == 0 {
			os.Exit(33)
		}
		var payload map[string]string
		_ = json.Unmarshal([]byte(args[2]), &payload)
		root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(payload["executablePath"]))))
		_ = root
		entries, _ := filepath.Glob(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".runtime", "*.sock.pid"))
		pid := 0
		for _, path := range entries {
			data, _ := os.ReadFile(path)
			value, _ := strconv.Atoi(trim(string(data)))
			if value > 0 {
				pid = value
			}
		}
		trusted := os.Getenv("CC_HAHA_CU_REJECT_PEER") != "1"
		log(map[string]any{"transport": "verifier", "fd": 3, "pid": pid, "trusted": trusted})
		fmt.Println(string(encode(map[string]any{"ok": true, "result": map[string]any{"trusted": trusted, "pid": pid}})))
		// Optional physical verifier-exit barrier for the carrier regression. The
		// successful stdout frame is emitted before the original deadline expires.
		if barrier := os.Getenv("CC_HAHA_CU_VERIFIER_EXIT_BARRIER"); barrier != "" {
			if err := os.WriteFile(os.Getenv("CC_HAHA_CU_NATIVE_LOG")+".phases", []byte("verifier-before-exit\n"), 0600); err != nil {
				os.Exit(36)
			}
			file, err := os.Open(barrier)
			if err != nil {
				os.Exit(37)
			}
			var value [1]byte
			_, err = file.Read(value[:])
			_ = file.Close()
			if err != nil {
				os.Exit(38)
			}
		}
		os.Exit(0)
	}
	if args[0] == "check_permissions" {
		fmt.Println(`{"ok":true,"result":{"accessibility":true,"screenRecording":true}}`)
		os.Exit(0)
	}
	if args[0] != "daemon" || len(args) != 3 || args[1] != "--socket" {
		log(map[string]string{"transport": "one-shot", "command": args[0]})
		fmt.Println(`{"ok":false,"error":{"code":"unauthorized_client","message":"fixture denies one-shot action"}}`)
		os.Exit(1)
	}
	socket := args[2]
	listener, err := net.Listen("unix", socket)
	if err != nil {
		os.Exit(34)
	}
	_ = os.WriteFile(socket+".pid", []byte(strconv.Itoa(os.Getpid())), 0600)
	connection, err := listener.Accept()
	if err != nil {
		os.Exit(35)
	}
	scanner := bufio.NewScanner(connection)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		var request object
		_ = json.Unmarshal(scanner.Bytes(), &request)
		command, _ := stringValue(request["cmd"])
		id, _ := stringValue(request["id"])
		payload, _ := decodeObject(request["payload"])
		log(map[string]any{"transport": "daemon", "pid": os.Getpid(), "request": request})
		mode, _ := stringValue(payload["fixtureMode"])
		if mode == "drop" {
			_ = connection.Close()
			_ = listener.Close()
			os.Exit(0)
		}
		if mode == "sleep" {
			time.Sleep(200 * time.Millisecond)
		}
		envelope := map[string]any{"id": id, "ok": true, "result": map[string]any{"command": command, "payload": payload, "sessionId": request["sessionId"], "turnId": request["turnId"]}}
		if command == "ping" {
			version := ProtocolVersion
			if os.Getenv("CC_HAHA_CU_BAD_PROTOCOL") == "1" {
				version = "old-protocol"
			}
			envelope["result"] = map[string]any{"protocolVersion": version, "supportsAbsoluteDeadlines": true, "supportsTurnEnd": true}
		}
		if mode == "not-trusted" {
			envelope = map[string]any{"id": id, "ok": false, "error": map[string]any{"message": "Accessibility permission not_trusted", "code": "not_trusted"}}
		}
		_, err = connection.Write(append(encode(envelope), '\n'))
		if err != nil || command == "shutdown" {
			break
		}
	}
	_ = connection.Close()
	_ = listener.Close()
	_ = os.Remove(socket)
	_ = os.Remove(socket + ".pid")
	os.Exit(0)
}
func daemonFixture(t *testing.T, overrides ...map[string]string) (*Service, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("native macOS AF_UNIX helper protocol")
	}
	root, err := os.MkdirTemp("/tmp", "ccu-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	options := Options{HomeDir: filepath.Join(root, "home"), ConfigDir: filepath.Join(root, "config"), WorkDir: filepath.Join(root, "work"), TempDir: filepath.Join(root, "tmp"), Platform: "darwin", Arch: "arm64", AppRoots: []string{filepath.Join(root, "apps")}, RequestTimeout: time.Second, ReadinessTimeout: 3 * time.Second}
	for _, path := range []string{options.HomeDir, options.ConfigDir, options.WorkDir, options.TempDir} {
		_ = os.MkdirAll(path, 0700)
	}
	log := filepath.Join(root, "native.jsonl")
	helper := filepath.Join(root, helperAppName, filepath.FromSlash(helperInner))
	_ = os.MkdirAll(filepath.Dir(helper), 0700)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(executable, "'", "'\"'\"'") + "' '-test.run=^TestComputerUseFixtureProcess$' -- \"$@\"\n"
	if err = os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	options.Environment = map[string]string{"HOME": options.HomeDir, "USERPROFILE": options.HomeDir, "CLAUDE_CONFIG_DIR": options.ConfigDir, "TMPDIR": options.TempDir, "PATH": "/usr/bin:/bin", "CC_HAHA_CU_HELPER_PATH": helper, "CC_HAHA_CU_NATIVE_FIXTURE": "1", "CC_HAHA_CU_NATIVE_LOG": log}
	for _, values := range overrides {
		for key, value := range values {
			options.Environment[key] = value
		}
	}
	// The carrier models a native Swift process. Keep race instrumentation,
	// while omitting Go race's artificial sleep after that process has exited.
	options.Environment["GORACE"] = "atexit_sleep_ms=0"
	native := nativeRunner(options)
	var children []*exec.Cmd
	var mu sync.Mutex
	waits := []chan error{}
	options.Run = func(ctx context.Context, file string, args []string, extra []*os.File) (CommandResult, error) {
		if file == "/usr/bin/uname" {
			return CommandResult{Stdout: "25.4.0"}, nil
		}
		if file == "/usr/bin/open" {
			if !reflect.DeepEqual(args[:3], []string{"-n", filepath.Join(root, helperAppName), "--args"}) {
				return CommandResult{Code: 1}, errors.New("wrong LaunchServices contract")
			}
			child := exec.Command(helper, args[3:]...)
			var childStderr commandBuffer
			child.Stderr = &childStderr
			child.Dir = options.WorkDir
			for key, value := range options.Environment {
				child.Env = append(child.Env, key+"="+value)
			}
			capturePublicGoCoverage(t, child)
			if err := child.Start(); err != nil {
				return CommandResult{Code: 1}, err
			}
			done := make(chan error, 1)
			mu.Lock()
			children = append(children, child)
			waits = append(waits, done)
			mu.Unlock()
			go func() {
				e := child.Wait()
				if e != nil {
					detail := childStderr.String()
					if len(detail) > 2048 {
						detail = detail[:2048]
					}
					t.Logf("private native fixture child exit=%v stderr=%q", e, detail)
				}
				done <- e
			}()
			return CommandResult{}, nil
		}
		return native(ctx, file, args, extra)
	}
	service := fixtureService(t, options)
	if err = service.UpdateConfig(context.Background(), json.RawMessage(`{"enabled":true,"authorizedApps":[],"grantFlags":{"systemKeyCombos":true}}`)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = service.Close(ctx)
		cancel()
		mu.Lock()
		defer mu.Unlock()
		for index, done := range waits {
			select {
			case <-done:
			case <-time.After(time.Second):
				_ = children[index].Process.Kill()
				<-done
			}
		}
	})
	return service, log
}
func daemonRecords(t *testing.T, path string) []map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]json.RawMessage{}
	for _, line := range strings.Split(trim(string(raw)), "\n") {
		var row map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &row) != nil {
			t.Fatalf("bad receipt %s", line)
		}
		rows = append(rows, row)
	}
	return rows
}
func TestNativeDaemonHandshakeSessionTurnOverlayAndCleanup(t *testing.T) {
	service, log := daemonFixture(t)
	ctx := context.Background()
	first, err := service.Call(ctx, "actual-session", "get_app_state", json.RawMessage(`{"app":"SecurityAgent","future":{"keep":17}}`))
	if err != nil {
		t.Fatal(err)
	}
	var actual object
	_ = json.Unmarshal(first, &actual)
	firstTurn, _ := stringValue(actual["turnId"])
	_, err = service.Call(ctx, "actual-session", "click", json.RawMessage(`{"app":"SecurityAgent","x":1,"y":2}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Call(ctx, "actual-session", "click", json.RawMessage(`{"pid":47,"app":"Any App","x":1,"y":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if err = service.EndTurn(ctx, "actual-session"); err != nil {
		t.Fatal(err)
	}
	next, err := service.Call(ctx, "actual-session", "get_app_state", json.RawMessage(`{"app":"SecurityAgent"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(next, &actual)
	nextTurn, _ := stringValue(actual["turnId"])
	if nextTurn == "" || firstTurn == nextTurn {
		t.Fatal("turn_end failed to retire the native snapshot owner")
	}
	if err = service.Close(ctx); err != nil {
		t.Fatal(err)
	}
	rows := daemonRecords(t, log)
	commands := []string{}
	verifier := false
	for _, row := range rows {
		transport, _ := stringValue(row["transport"])
		if transport == "one-shot" {
			t.Fatal("native action used one-shot fallback")
		}
		if transport == "verifier" {
			verifier = true
			continue
		}
		request, _ := decodeObject(row["request"])
		command, _ := stringValue(request["cmd"])
		commands = append(commands, command)
		if string(request["id"]) != string(request["requestId"]) || string(request["clientApiVersion"]) != `"`+ProtocolVersion+`"` || string(request["sessionId"]) != `"actual-session"` {
			t.Fatalf("wire=%s", encode(request))
		}
		var deadline int64
		_ = json.Unmarshal(request["deadlineUnixMilliseconds"], &deadline)
		if deadline <= 0 {
			t.Fatal("native absolute deadline missing")
		}
	}
	want := []string{"ping", "overlay_show", "get_app_state", "click", "overlay_show", "click", "turn_end", "overlay_show", "get_app_state", "shutdown"}
	if !verifier || !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands=%v verifier=%v", commands, verifier)
	}
	files, _ := filepath.Glob(filepath.Join(service.options.ConfigDir, ".runtime", "*.sock*"))
	if len(files) != 0 {
		t.Fatalf("native socket residue=%v", files)
	}
	if _, err = service.Call(ctx, "actual-session", "click", json.RawMessage(`{}`)); err == nil {
		t.Fatal("closed service recreated native daemon")
	}
}
func TestNativeDaemonDroppedMutationIsUnknownAndNeverReplayed(t *testing.T) {
	service, log := daemonFixture(t)
	_, err := service.Call(context.Background(), "actual-session", "click", json.RawMessage(`{"app":"Any App","fixtureMode":"drop"}`))
	var unknown *ResultUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("dropped mutation=%T %v", err, err)
	}
	rows := daemonRecords(t, log)
	count := 0
	for _, row := range rows {
		if transport, _ := stringValue(row["transport"]); transport == "one-shot" {
			t.Fatal("dropped mutation replayed through CLI")
		}
		request, _ := decodeObject(row["request"])
		if command, _ := stringValue(request["cmd"]); command == "click" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("mutation dispatched %d times", count)
	}
}
func TestNativeDaemonCancellationDrainsOwnedSocketAndCleanupDoesNotLaunch(t *testing.T) {
	service, log := daemonFixture(t)
	if err := service.EndTurn(context.Background(), "never-started"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("no-daemon cleanup launched a helper")
	}
	_, err := service.Call(context.Background(), "session", "check_permissions", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := service.Call(ctx, "session", "get_app_state", json.RawMessage(`{"app":"Any App","fixtureMode":"sleep"}`))
		done <- err
	}()
	<-started
	deadline := time.Now().Add(time.Second)
	for {
		raw, _ := os.ReadFile(log)
		if strings.Contains(string(raw), "sleep") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("mutation did not enter native wire")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		var unknown *ResultUnknownError
		if !errors.As(err, &unknown) {
			t.Fatalf("cancel=%T %v", err, err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel failed to interrupt owned native socket")
	}
	if err = service.EndTurn(context.Background(), "session"); err != nil {
		t.Fatal(err)
	}
	rows := daemonRecords(t, log)
	starts := 0
	for _, row := range rows {
		request, _ := decodeObject(row["request"])
		if command, _ := stringValue(request["cmd"]); command == "ping" {
			starts++
		}
	}
	if starts != 1 {
		t.Fatalf("cleanup restarted helper %d times", starts)
	}
}
func TestPrivateDaemonDirectoryRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	_ = os.Mkdir(target, 0700)
	link := filepath.Join(root, ".runtime")
	_ = os.Symlink(target, link)
	if err := privateRuntimeDirectory(link); err == nil {
		t.Fatal("runtime symlink accepted")
	}
}

func TestNativeDaemonRejectsPeerAndProtocolBeforeAnyAppCommand(t *testing.T) {
	for _, flag := range []string{"CC_HAHA_CU_REJECT_PEER", "CC_HAHA_CU_BAD_PROTOCOL"} {
		t.Run(flag, func(t *testing.T) {
			service, log := daemonFixture(t, map[string]string{flag: "1"})
			_, err := service.Call(context.Background(), "session", "click", json.RawMessage(`{"app":"Any App"}`))
			var unavailable *DaemonUnavailableError
			if !errors.As(err, &unavailable) {
				t.Fatalf("rejected %s=%T %v", flag, err, err)
			}
			for _, row := range daemonRecords(t, log) {
				if transport, _ := stringValue(row["transport"]); transport == "one-shot" {
					t.Fatal("authentication failure bypassed daemon")
				}
				request, _ := decodeObject(row["request"])
				command, _ := stringValue(request["cmd"])
				if command != "" && command != "ping" {
					t.Fatalf("untrusted/old daemon received %s", command)
				}
			}
		})
	}
}
func TestNativeDaemonTimeoutIsUnknownAndPreservesNoReplay(t *testing.T) {
	service, log := daemonFixture(t)
	service.options.RequestTimeout = 50 * time.Millisecond
	_, err := service.Call(context.Background(), "session", "type_text", json.RawMessage(`{"app":"Any App","text":"one","fixtureMode":"sleep"}`))
	var unknown *ResultUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("timeout=%T %v", err, err)
	}
	count := 0
	for _, row := range daemonRecords(t, log) {
		if transport, _ := stringValue(row["transport"]); transport == "one-shot" {
			t.Fatal("timed out mutation replayed")
		}
		request, _ := decodeObject(row["request"])
		if command, _ := stringValue(request["cmd"]); command == "type_text" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("timed out mutation sent %d times", count)
	}
}
func TestNativeDaemonGrantRefreshRestartsOnceUntilSuccess(t *testing.T) {
	service, log := daemonFixture(t)
	for range 3 {
		_, err := service.Call(context.Background(), "session", "click", json.RawMessage(`{"app":"Any App","fixtureMode":"not-trusted"}`))
		var native *NativeCommandError
		if !errors.As(err, &native) || native.Code != "not_trusted" {
			t.Fatalf("grant=%T %v", err, err)
		}
	}
	starts := 0
	for _, row := range daemonRecords(t, log) {
		request, _ := decodeObject(row["request"])
		if command, _ := stringValue(request["cmd"]); command == "ping" {
			starts++
		}
	}
	if starts != 2 {
		t.Fatalf("persistent missing grant thrashed %d daemon starts", starts)
	}
	if _, err := service.Call(context.Background(), "session", "check_permissions", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	_, _ = service.Call(context.Background(), "session", "click", json.RawMessage(`{"app":"Any App","fixtureMode":"not-trusted"}`))
	service.mu.Lock()
	client := service.clients["session"]
	service.mu.Unlock()
	client.mu.Lock()
	retired := client.connection == nil
	client.mu.Unlock()
	if !retired {
		t.Fatal("successful permission refresh did not rearm later grant restart")
	}
}
func TestNativeSessionRetirementAllowsSameSIDFreshDaemon(t *testing.T) {
	service, log := daemonFixture(t)
	if _, err := service.Call(context.Background(), "same-session", "get_app_state", json.RawMessage(`{"app":"Any App"}`)); err != nil {
		t.Fatal(err)
	}
	if err := service.CloseSession(context.Background(), "same-session"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Call(context.Background(), "same-session", "get_app_state", json.RawMessage(`{"app":"Any App"}`)); err != nil {
		t.Fatal(err)
	}
	starts := 0
	pids := map[string]bool{}
	for _, row := range daemonRecords(t, log) {
		request, _ := decodeObject(row["request"])
		if command, _ := stringValue(request["cmd"]); command == "ping" {
			starts++
			pids[string(row["pid"])] = true
		}
	}
	if starts != 2 || len(pids) != 2 {
		t.Fatalf("resume reused retired daemon starts=%d pids=%v", starts, pids)
	}
}
