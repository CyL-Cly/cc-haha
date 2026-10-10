package mcpauth

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeBrowserExecutesOwnedChildWithExplicitEnvironmentAndValidURL(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX child contract; Windows has build verification")
	}
	s, home := authFixture(t)
	command := filepath.Join(home, "owned-browser")
	marker := filepath.Join(home, "browser-args")
	if e := os.WriteFile(command, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" > \"$MARKER\"\n"), 0700); e != nil {
		t.Fatal(e)
	}
	s.options.Environment = map[string]string{"BROWSER": command, "HOME": home, "MARKER": marker, "PATH": "/usr/bin:/bin"}
	for _, address := range []string{"javascript:bad", "invalid", "file:///owned"} {
		ok, e := s.defaultOpenBrowser(context.Background(), address)
		if ok || e != nil {
			t.Fatal("invalid browser URL accepted", address, e)
		}
	}
	if _, e := os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("invalid URL launched native child")
	}
	ok, e := s.defaultOpenBrowser(context.Background(), "https://owned.example/path?a=b")
	if !ok || e != nil {
		t.Fatal(ok, e)
	}
	content, e := os.ReadFile(marker)
	if e != nil || string(content) != "https://owned.example/path?a=b\n" {
		t.Fatal(string(content), e)
	}
	s.options.Environment["BROWSER"] = "owned-browser"
	s.options.Environment["PATH"] = home
	ok, e = s.defaultOpenBrowser(context.Background(), "https://owned.example/next")
	if !ok || e != nil {
		t.Fatal("explicit PATH lookup", ok, e)
	}
	s.options.Environment["BROWSER"] = "missing-browser"
	ok, e = s.defaultOpenBrowser(context.Background(), "https://owned.example/")
	if ok || e != nil {
		t.Fatal("missing browser invented launch", ok, e)
	}
}
func TestCallbackPortExplicitConfigEnvironmentAndOwnedRandomRange(t *testing.T) {
	s, _ := authFixture(t)
	config := record(field("oauth", map[string]any{"callbackPort": 32123}))
	port, e := s.callbackPort(config)
	if port != 32123 || e != nil {
		t.Fatal(port, e)
	}
	s.options.Environment["MCP_OAUTH_CALLBACK_PORT"] = "  +34567suffix"
	port, e = s.callbackPort(record())
	if port != 34567 || e != nil {
		t.Fatal(port, e)
	}
	s.options.Environment["MCP_OAUTH_CALLBACK_PORT"] = "invalid"
	s.options.RandomFloat = func() float64 { return 0.5 }
	port, e = s.callbackPort(record())
	if port < 49152 || port > 65535 || e != nil {
		t.Fatal(port, e)
	}
	s.options.Platform = "windows"
	port, e = s.callbackPort(record())
	if port < 39152 || port > 49151 || e != nil {
		t.Fatal(port, e)
	}
	if _, e = s.callbackPort(record(field("oauth", map[string]any{"callbackPort": "bad"}))); e == nil {
		t.Fatal("bad port accepted")
	}
	if !strings.Contains(s.callbackListenError(34567, os.ErrExist).Error(), "netstat -ano") {
		t.Fatal("Windows callback diagnostic lost")
	}
}
