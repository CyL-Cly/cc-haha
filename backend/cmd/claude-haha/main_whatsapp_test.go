package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/server/adapterlauncher"
)

func TestPublicWhatsAppFactoryLoadsLegacyAuthAndFailsBeforeNetworkOnBadCredentials(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	authDir := filepath.Join(config, "whatsapp-auth", "legacy")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}
	credentials := []byte(`{"futureCredential":{"keep":true}}`)
	if err := os.WriteFile(filepath.Join(authDir, "creds.json"), credentials, 0600); err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"future":{"preserve":true},"whatsapp":{"authDir":"` + filepath.ToSlash(authDir) + `"}}`)
	if err := os.WriteFile(filepath.Join(config, "adapters.json"), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	var output, stderr bytes.Buffer
	code, err := adapterlauncher.Run(context.Background(), []string{"--app-root", "/private/fixture/app.asar", "--whatsapp"}, adapterlauncher.Options{
		Environment: map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "PWD": home, "DISABLE_TELEMETRY": "1"}, HomeDir: home, WorkDir: home,
		Stdout: &output, Stderr: &stderr, StartWhatsApp: startNativeWhatsApp,
	})
	if code != 1 || err == nil || !strings.Contains(err.Error(), "credential keypair") || output.String() != "[claude-sidecar] starting WhatsApp adapter\n" {
		t.Fatal("actual public factory did not reach credential consumer", code, err, output.String(), stderr.String())
	}
	for name, expected := range map[string][]byte{filepath.Join(authDir, "creds.json"): credentials, filepath.Join(config, "adapters.json"): legacy} {
		actual, err := os.ReadFile(name)
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatal("startup modified legacy account files", err)
		}
	}
}

func TestPublicAdaptersRouteUsesWhatsAppFactoryAfterTheExistingCredentialGate(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	authDir := filepath.Join(config, "whatsapp-auth", "default")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "creds.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(key, home)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	t.Setenv("WHATSAPP_AUTH_DIR", "")
	t.Setenv("CLAUDE_CODE_FORCE_RECOVERY_CLI", "1")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	priorOut, priorError := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = writer, writer
	defer func() { os.Stdout, os.Stderr = priorOut, priorError; _ = writer.Close() }()
	code := run(context.Background(), []string{"adapters", "--whatsapp"})
	_ = writer.Close()
	os.Stdout, os.Stderr = priorOut, priorError
	text, err := io.ReadAll(reader)
	if err != nil || code != 1 || !bytes.Contains(text, []byte("credential keypair")) || bytes.Contains(text, []byte("not migrated")) {
		t.Fatal("public main WhatsApp factory missing", code, err, string(text))
	}
}
