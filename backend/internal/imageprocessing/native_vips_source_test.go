//go:build darwin || linux

package imageprocessing

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestNativeCodecBytesAgainstActualSharpSource(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("fixture native library is macOS arm64")
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	library, _ := filepath.Abs("../../../node_modules/@img/sharp-libvips-darwin-arm64/lib/libvips-cpp.8.17.3.dylib")
	processor, err := OpenVips(library)
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{}
	for _, alpha := range []uint8{255, 137} {
		source := image.NewNRGBA(image.Rect(0, 0, 123, 83))
		for y := 0; y < 83; y++ {
			for x := 0; x < 123; x++ {
				source.SetNRGBA(x, y, color.NRGBA{uint8(x * 7), uint8(y * 5), uint8(x + y), alpha})
			}
		}
		var data bytes.Buffer
		if err = png.Encode(&data, source); err != nil {
			t.Fatal(err)
		}
		for _, format := range []string{"png", "jpeg", "webp"} {
			for _, width := range []int{123, 31} {
				rows = append(rows, map[string]any{"data": base64.StdEncoding.EncodeToString(data.Bytes()), "width": width, "height": width * 83 / 123, "format": format, "quality": 85})
			}
		}
	}
	for _, kind := range []string{"gray", "gray16", "rgb16", "rgba16"} {
		var source image.Image
		switch kind {
		case "gray":
			pixels := image.NewGray(image.Rect(0, 0, 123, 83))
			for y := 0; y < 83; y++ {
				for x := 0; x < 123; x++ {
					pixels.SetGray(x, y, color.Gray{uint8(x*7 + y*3)})
				}
			}
			source = pixels
		case "gray16":
			pixels := image.NewGray16(image.Rect(0, 0, 123, 83))
			for y := 0; y < 83; y++ {
				for x := 0; x < 123; x++ {
					pixels.SetGray16(x, y, color.Gray16{uint16(x*719 + y*317)})
				}
			}
			source = pixels
		default:
			pixels := image.NewNRGBA64(image.Rect(0, 0, 123, 83))
			alpha := uint16(65535)
			if kind == "rgba16" {
				alpha = 35017
			}
			for y := 0; y < 83; y++ {
				for x := 0; x < 123; x++ {
					pixels.SetNRGBA64(x, y, color.NRGBA64{uint16(x * 719), uint16(y * 317), uint16((x + y) * 239), alpha})
				}
			}
			source = pixels
		}
		var data bytes.Buffer
		if err := png.Encode(&data, source); err != nil {
			t.Fatal(err)
		}
		for _, format := range []string{"png", "jpeg", "webp"} {
			for _, width := range []int{123, 31} {
				rows = append(rows, map[string]any{"data": base64.StdEncoding.EncodeToString(data.Bytes()), "width": width, "height": width * 83 / 123, "format": format, "quality": 85})
			}
		}
	}
	for _, profile := range []string{"p3", "cmyk"} {
		for _, format := range []string{"png", "jpeg", "webp"} {
			for _, width := range []int{123, 31} {
				rows = append(rows, map[string]any{"data": rows[0]["data"], "profile": profile, "width": width, "height": width * 83 / 123, "format": format, "quality": 85})
			}
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-image-codec.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_IMAGE_CODEC_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(rows)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var expected []struct {
		Metadata   Metadata `json:"metadata"`
		Data       string   `json:"data"`
		DecodedPNG string   `json:"decodedPng"`
		InputData  string   `json:"inputData"`
	}
	if err = json.Unmarshal(raw, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal(err, string(raw))
	}
	for i, row := range rows {
		data, _ := base64.StdEncoding.DecodeString(row["data"].(string))
		if expected[i].InputData != "" {
			data, _ = base64.StdEncoding.DecodeString(expected[i].InputData)
		}
		metadata, err := processor.Metadata(data)
		if err != nil || metadata != expected[i].Metadata {
			t.Errorf("case%d metadata Go%v Source%v err%v", i, metadata, expected[i].Metadata, err)
		}
		encoded, err := processor.Encode(data, row["width"].(int), row["height"].(int), row["format"].(string), row["quality"].(int))
		if err != nil {
			t.Errorf("case%d encode %v", i, err)
			continue
		}
		source, _ := base64.StdEncoding.DecodeString(expected[i].Data)
		if !bytes.Equal(encoded, source) {
			if row["format"] == "webp" {
				decoded, err := processor.Encode(encoded, 0, 0, "png", 0)
				wantDecoded, _ := base64.StdEncoding.DecodeString(expected[i].DecodedPNG)
				goPixels, goErr := png.Decode(bytes.NewReader(decoded))
				wantPixels, wantErr := png.Decode(bytes.NewReader(wantDecoded))
				difference := 0
				if err == nil && goErr == nil && wantErr == nil {
					for y := goPixels.Bounds().Min.Y; y < goPixels.Bounds().Max.Y; y++ {
						for x := goPixels.Bounds().Min.X; x < goPixels.Bounds().Max.X; x++ {
							if color.NRGBA64Model.Convert(goPixels.At(x, y)) != color.NRGBA64Model.Convert(wantPixels.At(x, y)) {
								difference++
							}
						}
					}
				}
				t.Logf("case%d WebP decoded PNG equal=%v pixel differences=%d errors=%v/%v/%v", i, bytes.Equal(decoded, wantDecoded), difference, err, goErr, wantErr)
			}
			// Preserve immutable physical outputs for diagnosing codec parameters.
			_ = os.WriteFile(filepath.Join(home, "go-"+row["format"].(string)), encoded, 0600)
			_ = os.WriteFile(filepath.Join(home, "source-"+row["format"].(string)), source, 0600)
			t.Errorf("case%d %s bytes Go%d Source%d", i, row["format"], len(encoded), len(source))
		}
	}
}
