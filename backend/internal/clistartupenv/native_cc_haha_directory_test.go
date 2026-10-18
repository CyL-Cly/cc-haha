package clistartupenv

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeCcHahaDirectoryBindingsKeepFiniteGuardAndPropagateFailure(t *testing.T) {
	s, _ := newTestService(t, "{}", protocol.NewJSONObject(), true)
	options := s.options
	options.NativeCcHahaConfigDirectory = func() (string, error) { return options.ConfigHome, nil }
	if _, err := New(options); err == nil {
		t.Fatal("finite host accepted native directory callback")
	}
	options.Host.Profile = ResolvedPolicyReleaseProfile
	options.Host.ResolvePolicy = func() (*protocol.JSONObject, error) { return nil, nil }
	options.NativeCcHahaConfigDirectory = func() (string, error) { return "relative", nil }
	reader, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reader.ccHahaEnv(); err == nil {
		t.Fatal("native reader used unresolved process directory")
	}
	want := errors.New("physical native directory lookup failed")
	options.NativeCcHahaConfigDirectory = func() (string, error) { return "", want }
	reader, err = New(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reader.ccHahaEnv(); !errors.Is(err, want) {
		t.Fatal("directory error was swallowed", err)
	}
	// A reader callback never grants the corresponding settings assignment.
	options.NativeCcHahaConfigDirectory = func() (string, error) { return filepath.Join(options.ConfigHome, "next"), nil }
	reader, err = New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = reader.assign(protocol.NewJSONObject(protocol.JSONField{Name: "CLAUDE_CONFIG_DIR", Value: quote(filepath.Join(options.ConfigHome, "next"))})); err == nil {
		t.Fatal("directory reader bypassed the original host mutation guard")
	}
}
