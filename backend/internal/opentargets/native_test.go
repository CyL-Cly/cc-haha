package opentargets

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNativeCommandLookupDetachedLaunchPNGAndFailedRasterizerCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX primitive harness; Windows native process contracts are cross-compiled separately")
	}
	home, row := fixture(t, "linux")
	bin := filepath.Join(home, "bin")
	marker := filepath.Join(home, "launched.txt")
	ownedFile(t, filepath.Join(bin, "code"), 0700)
	if err := os.WriteFile(filepath.Join(bin, "code"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$MARKER\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "which"), []byte("#!/bin/sh\nif [ \"$1\" = code ]; then printf '%s\\n' \"$CODE\"; else exit 1; fi\n"), 0700); err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), CWD: row.CWD, Platform: "linux", TempDir: filepath.Join(home, "tmp"), Environment: map[string]string{"HOME": home, "PATH": bin, "CODE": filepath.Join(bin, "code"), "MARKER": marker}})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.List(context.Background(), false)
	if err != nil || len(list.Targets) != 1 || list.Targets[0].ID != "vscode" {
		t.Fatalf("actual process lookup %+v %v", list, err)
	}
	file := filepath.Join(row.CWD, "target.txt")
	ownedFile(t, file, 0600)
	if _, err = s.Open(context.Background(), "vscode", file); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var content []byte
	for time.Now().Before(deadline) {
		content, err = os.ReadFile(marker)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil || string(content) != file+"\n" {
		t.Fatalf("actual detached child side effect=%q err=%v", content, err)
	}
	icon := filepath.Join(home, "native.png")
	ownedFile(t, icon, 0600)
	original, err := os.ReadFile(icon)
	if err != nil {
		t.Fatal(err)
	}
	data, err := s.primitives.ConvertIconToPNG(context.Background(), icon, 64)
	if err != nil || string(data) != string(original) {
		t.Fatalf("PNG native pass-through %q %v", data, err)
	}
	svg := filepath.Join(home, "native.svg")
	ownedFile(t, svg, 0600)
	if _, err = s.primitives.ConvertIconToPNG(context.Background(), svg, 32); err == nil {
		t.Fatal("missing real rasterizers reported successful conversion")
	}
	entries, err := os.ReadDir(filepath.Join(home, "tmp"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed conversion leaked temporary resource %v %v", entries, err)
	}
	if result, err := s.primitives.Launch(context.Background(), "unknown-command", nil); err != nil || result.Code != 1 {
		t.Fatalf("native spawn error projection %+v %v", result, err)
	}
	if _, err := s.run(context.Background(), "/usr/bin/head", []string{"-c", "1048577", "/dev/zero"}, nil, 2*time.Second); err == nil {
		t.Fatal("native command overflow bypassed Source 1 MiB buffer limit")
	}
}
func TestNativeWindowsIconPrimitiveUsesOwnedEnvironmentAndCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture replaces external PowerShell, not Windows business")
	}
	home, row := fixture(t, "win32")
	bin := filepath.Join(home, "bin")
	source := filepath.Join(home, "app.exe")
	ownedFile(t, source, 0600)
	marker := filepath.Join(home, "icon-request.txt")
	ownedFile(t, filepath.Join(bin, "powershell.exe"), 0700)
	script := "#!/bin/sh\nprintf '%s' \"$CC_HAHA_ICON_SOURCE\" > \"$MARKER\"\nprintf 'native extracted PNG' > \"$CC_HAHA_ICON_OUTPUT\"\n"
	if err := os.WriteFile(filepath.Join(bin, "powershell.exe"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), CWD: row.CWD, Platform: "win32", TempDir: filepath.Join(home, "tmp"), Environment: map[string]string{"HOME": home, "PATH": bin, "MARKER": marker}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := s.primitives.ConvertIconToPNG(context.Background(), source, 64)
	if err != nil || string(data) != "native extracted PNG" {
		t.Fatalf("actual icon primitive process %q %v", data, err)
	}
	request, err := os.ReadFile(marker)
	if err != nil || string(request) != source {
		t.Fatalf("icon source was not passed through environment %q %v", request, err)
	}
	entries, err := os.ReadDir(filepath.Join(home, "tmp"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("successful conversion leaked temp %v %v", entries, err)
	}
}
func TestActualDarwinPlistAndSipsIconConversion(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("actual macOS plutil/sips requires macOS")
	}
	home, row := fixture(t, "darwin")
	s, err := New(Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), CWD: row.CWD, Platform: "darwin", TempDir: filepath.Join(home, "tmp"), Environment: map[string]string{"HOME": home, "PATH": "/usr/bin:/bin"}})
	if err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(home, "Info.plist")
	ownedFile(t, plist, 0600)
	if err := os.WriteFile(plist, []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIconFile</key><string>ActualIcon</string></dict></plist>`), 0600); err != nil {
		t.Fatal(err)
	}
	if value := s.primitives.ReadPlistValue(context.Background(), plist, "CFBundleIconFile"); value == nil || *value != "ActualIcon" {
		t.Fatalf("actual plist extraction %v", value)
	}
	if value := s.primitives.ReadPlistValue(context.Background(), plist, "MissingKey"); value != nil {
		t.Fatalf("missing plist key invented %v", value)
	}
	source := filepath.Join(home, "input.icns")
	f, err := os.Create(source)
	if err != nil {
		t.Fatal(err)
	}
	picture := image.NewRGBA(image.Rect(0, 0, 32, 32))
	picture.Set(0, 0, color.RGBA{255, 0, 0, 255})
	if err = png.Encode(f, picture); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	data, err := s.primitives.ConvertIconToPNG(context.Background(), source, 16)
	if err != nil {
		t.Fatal(err)
	}
	decoded, format, err := image.Decode(strings.NewReader(string(data)))
	if err != nil || format != "png" || decoded.Bounds().Dx() != 16 || decoded.Bounds().Dy() != 16 {
		t.Fatalf("actual sips output format=%s err=%v", format, err)
	}
	entries, err := os.ReadDir(filepath.Join(home, "tmp"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("actual sips leaked temp %v %v", entries, err)
	}
}
