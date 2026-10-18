package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"
)

func clipboardPNG(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := png.Encode(&output, image.NewNRGBA(image.Rect(0, 0, 5, 4))); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestClipboardShortcutAddsRealPillAndSubmissionKeepsOnlyReferencedImages(t *testing.T) {
	state := NewState(nil, 80, 24)
	state.Editor.Set("Inspect ")
	data := clipboardPNG(t)
	model := &terminalModel{state: state, ctx: context.Background(), options: Options{ClipboardImage: func(context.Context) (*PastedContent, error) {
		return &PastedContent{Content: base64.StdEncoding.EncodeToString(data), MediaType: "image/png"}, nil
	}}}
	shortcut := state.HandleKey(Key{Name: "ctrl+v"}, time.Now())
	if len(shortcut) != 1 || shortcut[0].name != "chat:imagePaste" {
		t.Fatal("default image shortcut did not resolve to clipboard action", shortcut)
	}
	message := model.actions(shortcut)().(clipboardImageMsg)
	model.applyClipboardImage(message)
	if state.Editor.Text() != "Inspect [Image #1]" || len(state.PastedContents) != 1 {
		t.Fatal("clipboard image did not reach editable prompt", state.Editor.Text())
	}
	actions := state.submit()
	if len(actions) != 1 || actions[0].input.PastedContents[1].Content != base64.StdEncoding.EncodeToString(data) {
		t.Fatal("image bytes missing from actual submission")
	}
	state.FinishTurn(actions[0].input.TurnID, nil)
	model.applyClipboardImage(message)
	state.Editor.Set("Image removed")
	actions = state.submit()
	if len(actions) != 1 || len(actions[0].input.PastedContents) != 0 {
		t.Fatal("removed image pill was still submitted")
	}
}

func TestClipboardCompletionAfterQuitCannotMutateDraft(t *testing.T) {
	state := NewState(nil, 80, 24)
	state.Editor.Set("draft")
	state.Closing = true
	model := &terminalModel{state: state, ctx: context.Background()}
	model.applyClipboardImage(clipboardImageMsg{image: &PastedContent{Content: "encoded"}})
	if state.Editor.Text() != "draft" || len(state.PastedContents) != 0 {
		t.Fatal("late clipboard completion changed closed terminal")
	}
}

func TestClipboardPhysicalPlatformProducersUseExplicitEnvironmentAndCleanup(t *testing.T) {
	data := clipboardPNG(t)
	for _, platform := range []string{"darwin", "linux", "windows"} {
		t.Run(platform, func(t *testing.T) {
			environment := []string{"HOME=" + t.TempDir(), "PATH=/private/fixture/bin"}
			var staging string
			calls := 0
			value, err := readClipboardImage(context.Background(), platform, environment, func(_ context.Context, env []string, name string, args ...string) ([]byte, error) {
				calls++
				if strings.Join(env, "|") != strings.Join(environment, "|") {
					t.Fatal("clipboard process inherited another environment")
				}
				switch platform {
				case "darwin":
					if name != "osascript" || len(args) != 8 {
						t.Fatal(name, args)
					}
					parts := strings.Split(args[3], `"`)
					staging = parts[1]
					return nil, os.WriteFile(staging, data, 0600)
				case "linux":
					if calls == 1 {
						return nil, errors.New("xclip unavailable")
					}
					if name != "wl-paste" {
						t.Fatal(name, args)
					}
					return data, nil
				default:
					if name != "powershell" || args[0] != "-NoProfile" || args[1] != "-STA" {
						t.Fatal(name, args)
					}
					return []byte(base64.StdEncoding.EncodeToString(data) + "\r\n"), nil
				}
			})
			if err != nil || value == nil || value.MediaType != "image/png" || value.Content != base64.StdEncoding.EncodeToString(data) {
				t.Fatal("platform image bytes lost", value, err)
			}
			if staging != "" {
				if _, err := os.Stat(staging); !os.IsNotExist(err) {
					t.Fatal("clipboard staging file survived producer", err)
				}
			}
		})
	}
}

func TestClipboardCancellationStopsBeforeFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	count := 0
	value, err := readClipboardImage(ctx, "linux", nil, func(context.Context, []string, string, ...string) ([]byte, error) {
		count++
		cancel()
		return nil, context.Canceled
	})
	if value != nil || !errors.Is(err, context.Canceled) || count != 1 {
		t.Fatal("canceled clipboard continued fallback", value, err, count)
	}
}
