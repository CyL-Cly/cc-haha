package tui

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestTerminalMobileKeysAliasesAndNativeQRPayloadScan(t *testing.T) {
	for _, platform := range []string{"ios", "android"} {
		lines := terminalMobileLines(TerminalMobilePresentation(platform), 100)
		if !strings.Contains(strings.Join(lines, "\n"), map[string]string{"ios": "[iOS] / Android", "android": "iOS / [Android]"}[platform]) {
			t.Fatal(lines)
		}
		if runtime.GOOS != "darwin" {
			continue
		}
		scanner, err := exec.LookPath("osascript")
		if err != nil {
			t.Skip("Native QR scan requires macOS CoreImage/JavaScriptCore")
		}
		directory := t.TempDir()
		path := filepath.Join(directory, "actual-rendered-qr.png")
		var rows [][]rune
		for _, line := range lines[1:] {
			if line == "" {
				break
			}
			rows = append(rows, []rune(line[2:]))
		}
		width := len(rows[0])
		const scale = 8
		frame := image.NewRGBA(image.Rect(0, 0, width*scale, len(rows)*2*scale))
		for y := 0; y < frame.Bounds().Dy(); y++ {
			for x := 0; x < frame.Bounds().Dx(); x++ {
				glyph := rows[y/scale/2][x/scale]
				black := glyph == '█' || glyph == '▀' && y/scale%2 == 0 || glyph == '▄' && y/scale%2 == 1
				c := color.RGBA{255, 255, 255, 255}
				if black {
					c = color.RGBA{0, 0, 0, 255}
				}
				frame.SetRGBA(x, y, c)
			}
		}
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err = png.Encode(file, frame); err != nil {
			t.Fatal(err)
		}
		if err = file.Close(); err != nil {
			t.Fatal(err)
		}
		probe := filepath.Join(directory, "scan.js")
		const source = `ObjC.import('Foundation');ObjC.import('CoreImage');
function run(argv) {
 var image = $.CIImage.imageWithContentsOfURL($.NSURL.fileURLWithPath(argv[0]));
 var options = $.NSMutableDictionary.new;
 options.setObjectForKey($.CIDetectorAccuracyHigh, $.CIDetectorAccuracy);
 var context = $.CIContext.contextWithOptions($.NSDictionary.new);
 var detector = $.CIDetector.detectorOfTypeContextOptions($.CIDetectorTypeQRCode, context, options);
 var features = detector.featuresInImage(image), values = [];
 for (var i=0;i<features.count;i++) values.push(ObjC.unwrap(features.objectAtIndex(i).messageString));
 return values.join('\n');
}
`
		if err = os.WriteFile(probe, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, scanner, "-l", "JavaScript", probe, path)
		cmd.Env = append(os.Environ(), "HOME="+directory, "USERPROFILE="+directory, "TMPDIR="+directory)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Actual CoreImage scan %v %.3000s", err, output)
		}
		address := map[string]string{"ios": "https://apps.apple.com/app/claude-by-anthropic/id6473753684", "android": "https://play.google.com/store/apps/details?id=com.anthropic.claude"}[platform]
		if strings.TrimSpace(string(output)) != address || len(bytes.TrimSpace(output)) < 10 {
			t.Fatal("Actual rendered half-block QR does not scan to the Source URL", address, string(output))
		}
	}
	for _, key := range []Key{{Name: "tab"}, {Name: "left"}, {Name: "right"}} {
		s := NewState(nil, 100, 40)
		s.openLocalPanel(&LocalPanel{ID: "mobile", Kind: "mobile", RawInputKeys: []string{"tab", "left", "right", "q"}, Actions: []LocalPanelAction{{Key: "tab", Name: "mobile-switch"}, {Key: "left", Name: "mobile-switch"}, {Key: "right", Name: "mobile-switch"}, {Key: "q", Name: "mobile-close"}}, Presentation: TerminalMobilePresentation("ios")})
		actions := s.HandleKey(key, time.Now())
		if len(actions) != 1 || actions[0].kind != actionLocalPanel || actions[0].panel.Action != "mobile-switch" {
			t.Fatal(key, actions)
		}
		if actions = s.HandleKey(Key{Name: "enter"}, time.Now()); len(actions) != 0 || s.LocalPanel == nil {
			t.Fatal("Enter must not dismiss mobile", actions)
		}
	}
}
