package tui

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestClipboardTextSourceNativeSSHAndTmuxProducers(t *testing.T) {
	for _, fixture := range []struct {
		platform    string
		environment []string
		calls       [][]string
		output      string
	}{
		{"darwin", []string{"SSH_TTY=stale"}, [][]string{{"pbcopy"}}, "\x1b]52;c;aGk=\a"},
		{"darwin", []string{"SSH_CONNECTION=remote"}, nil, "\x1b]52;c;aGk=\a"},
		{"darwin", []string{"TERM=xterm-kitty"}, [][]string{{"pbcopy"}}, "\x1b]52;c;aGk=\x1b\\"},
		{"darwin", []string{"TMUX=private", "LC_TERMINAL=iTerm2"}, [][]string{{"pbcopy"}, {"tmux", "load-buffer", "-"}}, "\x1bPtmux;\x1b\x1b]52;c;aGk=\a\x1b\\"},
		{"windows", []string{"TMUX=private"}, [][]string{{"clip.exe"}, {"tmux", "load-buffer", "-w", "-"}}, "\x1bPtmux;\x1b\x1b]52;c;aGk=\a\x1b\\"},
	} {
		var calls [][]string
		var output bytes.Buffer
		execute := func(_ context.Context, env []string, text, name string, args ...string) error {
			if !reflect.DeepEqual(env, fixture.environment) || text != "hi" {
				t.Fatal("clipboard child lost explicit environment or stdin")
			}
			calls = append(calls, append([]string{name}, args...))
			return nil
		}
		if err := writeClipboardText(context.Background(), fixture.platform, fixture.environment, "hi", &output, execute, &textClipboardLinuxState{}); err != nil || !reflect.DeepEqual(calls, fixture.calls) || output.String() != fixture.output {
			t.Fatal(fixture.platform, err, calls, output.String())
		}
	}
	var calls []string
	linux := &textClipboardLinuxState{}
	execute := func(_ context.Context, _ []string, _, name string, _ ...string) error {
		calls = append(calls, name)
		if name == "wl-copy" {
			return errors.New("Wayland absent")
		}
		return nil
	}
	for i := 0; i < 2; i++ {
		if err := writeClipboardText(context.Background(), "linux", nil, "hi", &bytes.Buffer{}, execute, linux); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(calls, []string{"wl-copy", "xclip", "xclip"}) {
		t.Fatal("native Source Linux winner was not retained", calls)
	}
}

func TestClipboardTextActualPrivateChildReadsLiteralStdinAndCancellationSuppressesOSC(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix private native clipboard executable")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "literal-stdin")
	if err := os.WriteFile(filepath.Join(dir, "pbcopy"), []byte("#!/bin/sh\n/bin/cat > \"$CLIPBOARD_RECEIPT\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	text := "literal $(touch escaped) `command`\nUnicode 中文 😀"
	var output bytes.Buffer
	if err := WriteClipboardText(context.Background(), "darwin", []string{"PATH=" + dir, "HOME=" + dir, "CLIPBOARD_RECEIPT=" + file}, text, &output); err != nil {
		t.Fatal(err)
	}
	if contents, err := os.ReadFile(file); err != nil || string(contents) != text {
		t.Fatal("actual clipboard child did not receive literal stdin", err, string(contents))
	}
	ctx, cancel := context.WithCancel(context.Background())
	execute := func(context.Context, []string, string, string, ...string) error { cancel(); return context.Canceled }
	output.Reset()
	if err := writeClipboardText(ctx, "darwin", nil, text, &output, execute, &textClipboardLinuxState{}); !errors.Is(err, context.Canceled) || output.Len() != 0 {
		t.Fatal("cancelled clipboard wrote stale terminal sequence", err, output.String())
	}
}
