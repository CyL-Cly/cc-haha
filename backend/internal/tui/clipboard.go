package tui

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"
)

type clipboardImageMsg struct {
	image *PastedContent
	err   error
}

func (m *terminalModel) clipboardImageCommand() tea.Cmd {
	return func() tea.Msg {
		if !m.beginTurn() {
			return clipboardImageMsg{err: context.Canceled}
		}
		defer m.endTurn()
		reader := m.options.ClipboardImage
		if reader == nil {
			reader = func(ctx context.Context) (*PastedContent, error) {
				return readClipboardImage(ctx, runtime.GOOS, m.options.Environment, clipboardCommand)
			}
		}
		image, err := reader(m.ctx)
		return clipboardImageMsg{image, err}
	}
}

func (m *terminalModel) applyClipboardImage(msg clipboardImageMsg) {
	if m.ctx.Err() != nil || m.state.Closing {
		return
	}
	if msg.image != nil && msg.err == nil {
		m.state.PasteImage(*msg.image)
		return
	}
	text := "No image found in clipboard. Use the image paste shortcut to paste images."
	for _, item := range m.options.Environment {
		if strings.HasPrefix(item, "SSH_CONNECTION=") || strings.HasPrefix(item, "SSH_CLIENT=") || strings.HasPrefix(item, "SSH_TTY=") {
			text = "No image found in clipboard. You're SSH'd; try scp?"
			break
		}
	}
	m.state.Apply(Event{Kind: Notice, Text: text, NoticeTimeout: time.Second})
}

type clipboardExecutor func(context.Context, []string, string, ...string) ([]byte, error)

// Only explicit host environment reaches clipboard children. Commands use
// argument arrays and a private staging path, never shell interpolation.
func clipboardCommand(ctx context.Context, environment []string, name string, args ...string) ([]byte, error) {
	path := ""
	for _, item := range environment {
		if strings.HasPrefix(item, "PATH=") {
			path = strings.TrimPrefix(item, "PATH=")
		}
	}
	if filepath.IsAbs(name) {
		path = filepath.Dir(name)
	}
	for _, dir := range filepath.SplitList(path) {
		candidate := filepath.Join(dir, name)
		if runtime.GOOS == "windows" && filepath.Ext(candidate) == "" {
			candidate += ".exe"
		}
		info, err := os.Stat(candidate)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		command := exec.CommandContext(ctx, candidate, args...)
		command.Env = append([]string(nil), environment...)
		return command.Output()
	}
	return nil, exec.ErrNotFound
}

func readClipboardImage(ctx context.Context, platform string, environment []string, execute clipboardExecutor) (*PastedContent, error) {
	var data []byte
	var err error
	switch platform {
	case "darwin":
		directory, failure := os.MkdirTemp("", "cc-haha-clipboard-")
		if failure != nil {
			return nil, failure
		}
		defer os.RemoveAll(directory)
		path := filepath.Join(directory, "image.png")
		_, err = execute(ctx, environment, "osascript", "-e", "set png_data to (the clipboard as «class PNGf»)", "-e", "set fp to open for access POSIX file \""+path+"\" with write permission", "-e", "write png_data to fp", "-e", "close access fp")
		if err == nil {
			data, err = os.ReadFile(path)
		}
	case "linux":
		for _, command := range [][]string{{"xclip", "-selection", "clipboard", "-t", "image/png", "-o"}, {"wl-paste", "--type", "image/png"}, {"xclip", "-selection", "clipboard", "-t", "image/bmp", "-o"}, {"wl-paste", "--type", "image/bmp"}} {
			data, err = execute(ctx, environment, command[0], command[1:]...)
			if err == nil && len(data) > 0 {
				break
			}
			if context.Cause(ctx) != nil {
				return nil, context.Cause(ctx)
			}
		}
	case "windows", "win32":
		var encoded []byte
		encoded, err = execute(ctx, environment, "powershell", "-NoProfile", "-STA", "-Command", "$img = Get-Clipboard -Format Image; if ($img) { $stream = New-Object System.IO.MemoryStream; try { $img.Save($stream, [System.Drawing.Imaging.ImageFormat]::Png); [Convert]::ToBase64String($stream.ToArray()) } finally { $stream.Dispose(); $img.Dispose() } }")
		if err == nil {
			data, err = base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
		}
	default:
		return nil, nil
	}
	if context.Cause(ctx) != nil {
		return nil, context.Cause(ctx)
	}
	if err != nil || len(data) == 0 {
		return nil, nil
	}
	codec, _ := imageprocessing.BundledCodec("")
	format := imageprocessing.DetectFormat(data)
	if len(data) >= 2 && string(data[:2]) == "BM" {
		if codec == nil {
			return nil, nil
		}
		data, err = codec.Encode(data, 0, 0, "png", 0)
		if err != nil {
			return nil, nil
		}
		format = "png"
	}
	result, err := imageprocessing.Normalize(codec, data, len(data), format)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, nil
	}
	return &PastedContent{Type: "image", Content: base64.StdEncoding.EncodeToString(result.Buffer), MediaType: "image/" + result.MediaType, Filename: "Pasted image"}, nil
}
