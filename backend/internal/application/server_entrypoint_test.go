package application

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestActualServerEntrypointOwnsPrivateListenerStaticFilesAndShutdown(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	home := o.Environment["HOME"]
	dist := filepath.Join(home, "h5-dist")
	if err := os.MkdirAll(dist, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("<html>PRIVATE_SERVER_ENTRYPOINT</html>"), 0600); err != nil {
		t.Fatal(err)
	}
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reservation.Addr().(*net.TCPAddr).Port
	if err := reservation.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	var nativeCalls atomic.Int64
	version := "private-entrypoint-" + filepath.Base(home)
	go func() {
		result <- RunServer(ctx, []string{"--host", "127.0.0.1", "--port", strconv.Itoa(port), "--h5-dist", dist, "--app-root", home, "--system-prompt", "private system", "--model", "private-model", "--tools", "Read", "--permission-mode", "default", "--thinking", "disabled"}, o.Environment, home, version, io.Discard, func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
			nativeCalls.Add(1)
			if env["HOME"] != home || env["CLAUDE_CONFIG_DIR"] != o.Environment["CLAUDE_CONFIG_DIR"] || cwd != home {
				t.Error("unscoped native server factory")
			}
			native, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
			if err == nil {
				native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }
			}
			return native, err
		})
	}()
	joined := false
	defer func() {
		cancel()
		if !joined {
			select {
			case <-result:
			case <-time.After(5 * time.Second):
				t.Error("private RunServer cleanup did not join")
			}
		}
	}()
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(10 * time.Second)
	for {
		res, err := client.Get(base + "/health")
		if err == nil {
			var body map[string]any
			err = json.NewDecoder(res.Body).Decode(&body)
			res.Body.Close()
			_, stampErr := time.Parse(time.RFC3339Nano, fmt.Sprint(body["timestamp"]))
			if err != nil || res.StatusCode != 200 || stampErr != nil || body["status"] != "ok" {
				t.Fatalf("actual private Go health: %v %v", body, err)
			}
			break
		}
		select {
		case err := <-result:
			joined = true
			t.Fatalf("actual RunServer ended before ready: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("actual RunServer never became ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
	identity, err := client.Get(base + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	var identityBody map[string]any
	err = json.NewDecoder(identity.Body).Decode(&identityBody)
	identity.Body.Close()
	if err != nil || identity.StatusCode != 200 || identityBody["version"] != version {
		t.Fatalf("actual private Go server identity: %v %v", identityBody, err)
	}
	res, err := client.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || res.StatusCode != 200 || !strings.Contains(string(body), "PRIVATE_SERVER_ENTRYPOINT") {
		t.Fatalf("actual Go H5 static entrypoint: %d %s %v", res.StatusCode, body, err)
	}
	if nativeCalls.Load() == 0 {
		t.Fatal("server bypassed actual native factory")
	}
	cancel()
	select {
	case err := <-result:
		joined = true
		if err != nil {
			t.Fatal("actual RunServer shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("actual RunServer did not join shutdown")
	}
	if _, err := client.Get(base + "/health"); err == nil {
		t.Fatal("private listener survived shutdown")
	}
	listener, err := net.Listen("tcp", base[len("http://"):])
	if err != nil {
		t.Fatal("private listener not released", err)
	}
	listener.Close()
}

func TestActualServerEntrypointRejectsInvalidArgumentsAndBusyPortBeforeNativeBoot(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	for _, args := range [][]string{{"--port"}, {"--port", "-1"}, {"--port", "65536"}, {"--port", "bad"}, {"--thinking", "bad"}, {"--unknown", "value"}} {
		err := RunServer(context.Background(), args, o.Environment, o.WorkDir, o.Version, io.Discard, func(map[string]string, string, string) (*clibootstrap.NativeBoundary, error) {
			t.Error("invalid arguments reached native boot")
			return nil, os.ErrPermission
		})
		if err == nil {
			t.Fatal("accepted invalid server arguments", args)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	err = RunServer(context.Background(), []string{"--port", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)}, o.Environment, o.WorkDir, o.Version, io.Discard, func(map[string]string, string, string) (*clibootstrap.NativeBoundary, error) {
		t.Error("port conflict reached native boot")
		return nil, os.ErrPermission
	})
	if err == nil {
		t.Fatal("accepted occupied server port")
	}
}
