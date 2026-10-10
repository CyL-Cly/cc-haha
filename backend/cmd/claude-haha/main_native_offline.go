//go:build offline_native_fixture

package main

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"os"
	"strings"
)

func newMainNativeBoundary() (*clibootstrap.NativeBoundary, error) {
	env := map[string]string{}
	for _, s := range os.Environ() {
		k, v, ok := strings.Cut(s, "=")
		if ok {
			env[k] = v
		}
	}
	cwd, e := os.Getwd()
	if e != nil {
		return nil, e
	}
	return clibootstrap.NewOfflineNativeBoundary(env, cwd)
}

func newNativeBoundaryForEnvironment(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
	return clibootstrap.NewOfflineNativeBoundary(env, cwd)
}

// fd3 is an explicit parent-owned observation stream. It is never read by Go
// business code and cannot supply state or control a producer's timing.
func writeNativeObservation(n *clibootstrap.NativeBoundary) {
	f := os.NewFile(3, "offline-native-observer")
	if f == nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(map[string]any{"buildIdentity": "same public main with offline_native_fixture external primitives", "nativeTrace": n.Trace, "fullCLI": false, "actualOSSecurity": "NOTRUN", "quota": 0})
	f.Write(append(b, '\n'))
}
