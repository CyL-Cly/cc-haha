package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualPublicDoctorSubcommandScreenDismissAndCancelPhysicalPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PTY does not prove native Windows")
	}
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(root, "actual-public-doctor-go-cli")
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	if output, e := build.CombinedOutput(); e != nil {
		t.Fatal(e, string(output))
	}
	for _, dismiss := range []string{"enter", "escape", "early"} {
		t.Run(dismiss, func(t *testing.T) {
			home, _ := startupFlagHome(t, root, dismiss, false)
			memory := filepath.Join(home, "CLAUDE.md")
			content := bytes.Repeat([]byte("PHYSICAL_PUBLIC_DOCTOR_MEMORY "), 2000)
			if e := os.WriteFile(memory, content, 0600); e != nil {
				t.Fatal(e)
			}
			var model, requests, cancelled atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				model.Add(1)
				t.Error("no MCP public Doctor must not call provider", r.URL.Path)
				w.WriteHeader(500)
			}))
			defer provider.Close()
			releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if dismiss == "early" {
					if e := os.WriteFile(filepath.Join(home, "doctor-release-request-opened"), []byte("opened"), 0600); e != nil {
						t.Error(e)
					}
					<-r.Context().Done()
					cancelled.Add(1)
					return
				}
				fmt.Fprint(w, `[{"tag_name":"v3.0.1","assets":[{"name":"claude-haha-cli-manifest.json"}]}]`)
			}))
			defer releases.Close()
			process := exec.CommandContext(ctx, "/usr/bin/python3", "testdata/doctor_public.py", binary, home, dismiss)
			process.Env = append(resumeErrorEnv(home, root, provider.URL), "CC_HAHA_CLI_RELEASE_API="+releases.URL, "ENABLE_PID_BASED_VERSION_LOCKING=true", "TERM=xterm-256color")
			output, e := process.CombinedOutput()
			if e != nil || !bytes.Contains(output, []byte(`"restored": true`)) {
				t.Fatal("actual public Doctor PTY", e, string(output))
			}
			if model.Load() != 0 {
				t.Fatal("Doctor opened main model", model.Load())
			}
			if dismiss != "early" && requests.Load() != 2 {
				t.Fatal("actual latest/stable probes", requests.Load())
			}
			if dismiss == "early" && (requests.Load() == 0 || cancelled.Load() != requests.Load()) {
				t.Fatal("dismiss left release request alive", requests.Load(), cancelled.Load())
			}
			data, e := os.ReadFile(memory)
			if e != nil || !bytes.Equal(data, content) {
				t.Fatal("Doctor changed protected memory", e)
			}
		})
	}
}
