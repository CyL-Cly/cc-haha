package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/computeruse"
)

func TestComputerUseDesktopRoutesUseGoConfigAndNativeBoundary(t *testing.T) {
	var factoryCalls atomic.Int32
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { factoryCalls.Add(1); return nil, nil })
	helper := filepath.Join(options.HomeDir, "private-native-helper")
	if err := os.WriteFile(helper, []byte("fixture external native boundary"), 0700); err != nil {
		t.Fatal(err)
	}
	service, err := computeruse.New(computeruse.Options{HomeDir: options.HomeDir, ConfigDir: options.ConfigDir, WorkDir: options.WorkDir, TempDir: options.HomeDir, Platform: "darwin", Arch: "arm64", AppRoots: []string{filepath.Join(options.HomeDir, "Applications")}, Environment: map[string]string{"CC_HAHA_CU_HELPER_PATH": helper}, Run: func(_ context.Context, file string, args []string, _ []*os.File) (computeruse.CommandResult, error) {
		if strings.HasSuffix(file, "sw_vers") {
			return computeruse.CommandResult{Stdout: "14.4"}, nil
		}
		if strings.HasSuffix(file, "uname") {
			return computeruse.CommandResult{Stdout: "23.4.0"}, nil
		}
		if file == helper && args[0] == "check_permissions" {
			return computeruse.CommandResult{Stdout: `{"ok":true,"result":{"accessibility":true,"screenRecording":false}}`}, nil
		}
		if file == helper && args[0] == "request-access" {
			return computeruse.CommandResult{Stdout: `{"ok":true,"result":{"accessibility":true,"screenRecording":true}}`}, nil
		}
		return computeruse.CommandResult{Stdout: `{"ok":false,"error":{"message":"native one-shot discovery is restricted"}}`}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	options.ComputerUse = service
	options.OnShutdown = service.Close
	server, host := testHost(t, options)
	before := requestJSON(t, host, "GET", "/api/computer-use/authorized-apps", "", 200)
	var config map[string]json.RawMessage
	_ = json.Unmarshal(before, &config)
	if string(config["enabled"]) != "false" {
		t.Fatalf("default=%s", before)
	}
	requestJSON(t, host, "PUT", "/api/computer-use/authorized-apps", `{"enabled":true,"authorizedApps":[]}`, 200)
	status := requestJSON(t, host, "GET", "/api/computer-use/status", "", 200)
	var value map[string]any
	_ = json.Unmarshal(status, &value)
	if value["engine"] != "macos-native" || value["cuHelper"].(map[string]any)["available"] != true || value["permissions"].(map[string]any)["screenRecording"] != false {
		t.Fatalf("status=%s", status)
	}
	requestJSON(t, host, "POST", "/api/computer-use/open-permission-card", "", 200)
	requestJSON(t, host, "POST", "/api/computer-use/request-access", `{"bundleId":"Any Application"}`, 410)
	requestJSON(t, host, "POST", "/api/computer-use/setup", "", 200)
	requestJSON(t, host, "POST", "/api/computer-use/open-settings", `{"pane":"invalid"}`, 400)
	requestJSON(t, host, "GET", "/api/computer-use/apps", "", 200)
	requestJSON(t, host, "GET", "/api/computer-use/app-icon", "", 400)
	requestJSON(t, host, "GET", "/api/computer-use/unknown", "", 404)
	if err = server.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if factoryCalls.Load() != 0 {
		t.Fatal("settings endpoints launched a hidden agent")
	}
	if _, err = service.Call(context.Background(), "any-session", "click", json.RawMessage(`{"app":"Any App"}`)); err == nil {
		t.Fatal("Server.Close failed to retire shared CU service")
	}
}
