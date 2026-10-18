package teamspawn

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitSettingsModeRetainsAutoOnlyFallbackAndCachedPaneDetection(t *testing.T) {
	home := t.TempDir()
	missing := filepath.Join(home, "missing-bin")
	_ = os.MkdirAll(missing, 0700)
	p, err := NewPaneRuntime(home, os.Getpid(), map[string]string{"TERM_PROGRAM": "iTerm.app", "PATH": missing})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := p.SelectBackend("auto", false, false)
	if err != nil || backend != "in-process" || !p.InProcessEnabled("auto", false) {
		t.Fatal("actual unavailable native executables did not produce auto fallback", backend, err)
	}
	p.SetMode("tmux")
	if p.InProcessEnabled("tmux", false) {
		t.Fatal("explicit UI tmux mode inherited the auto-only fallback")
	}
	if backend, err = p.SelectBackend("tmux", false, false); err == nil || backend != "" {
		t.Fatal("cached auto fallback bypassed real explicit-backend failure", backend, err)
	}
	p.SetMode("auto")
	if backend, err = p.SelectBackend("auto", false, false); err != nil || backend != "in-process" {
		t.Fatal("returning to auto lost original fallback", backend, err)
	}
	bin := filepath.Join(home, "native-bin")
	_ = os.MkdirAll(bin, 0700)
	if err = os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nprintf '%s\\n' 'actual private tmux version'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	p, err = NewPaneRuntime(home, os.Getpid(), map[string]string{"PATH": bin})
	if err != nil {
		t.Fatal(err)
	}
	backend, err = p.SelectBackend("tmux", false, false)
	if err != nil || backend != "tmux" {
		t.Fatal(backend, err)
	}
	p.SetMode("auto")
	if backend, err = p.SelectBackend("auto", false, false); err != nil || backend != "in-process" {
		t.Fatal("auto environment predicate was overridden by cached pane detection", backend, err)
	}
	if err = os.Remove(filepath.Join(bin, "tmux")); err != nil {
		t.Fatal(err)
	}
	p.SetMode("tmux")
	if backend, err = p.SelectBackend("tmux", false, false); err != nil || backend != "tmux" {
		t.Fatal("explicit UI mode cleared the actual successful detection cache", backend, err)
	}
}
