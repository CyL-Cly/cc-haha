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
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestNativeImageNormalizationAndVisionBudgetAgainstActualSource(t *testing.T) {
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
	for _, size := range [][2]int{{12, 8}, {9001, 3}, {137, 9001}, {1500, 1200}} {
		source := image.NewNRGBA(image.Rect(0, 0, size[0], size[1]))
		var random uint32 = 0x98f74162
		for y := 0; y < size[1]; y++ {
			for x := 0; x < size[0]; x++ {
				random ^= random << 13
				random ^= random >> 17
				random ^= random << 5
				source.SetNRGBA(x, y, color.NRGBA{uint8(random), uint8(random >> 8), uint8(random >> 16), 255})
			}
		}
		var data bytes.Buffer
		if err = png.Encode(&data, source); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, map[string]any{"data": base64.StdEncoding.EncodeToString(data.Bytes()), "ext": "png", "path": "/private/human image.png"})
		if size[0] == 1500 {
			for _, format := range []string{"png", "jpeg", "webp"} {
				encoded := data.Bytes()
				if format != "png" {
					encoded, err = processor.Encode(encoded, 0, 0, format, 90)
					if err != nil {
						t.Fatal(err)
					}
				}
				for _, tokens := range []float64{1000, 32} {
					rows = append(rows, map[string]any{"data": base64.StdEncoding.EncodeToString(encoded), "ext": format, "tokens": tokens, "path": "/private/human image"})
				}
				for _, limit := range []int{200000, 5000, 1} {
					rows = append(rows, map[string]any{"data": base64.StdEncoding.EncodeToString(encoded), "ext": format, "maxBytes": limit})
				}
			}
		}
	}
	for _, data := range [][]byte{nil, {'x'}, {0x89, 'P', 'N', 'G', 1, 2, 3, 4}} {
		rows = append(rows, map[string]any{"data": base64.StdEncoding.EncodeToString(data), "ext": "webp"})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-image-normalization.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_IMAGE_NORMALIZATION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(rows)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var expected []json.RawMessage
	if err = json.Unmarshal(raw, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal(err, string(raw))
	}
	for i, row := range rows {
		data, _ := base64.StdEncoding.DecodeString(row["data"].(string))
		var value Result
		var err error
		if limit, ok := row["maxBytes"].(int); ok {
			value, err = Compress(processor, data, limit, "image/"+row["ext"].(string))
		} else if tokens, ok := row["tokens"].(float64); ok {
			value, err = Downsample(processor, data, len(data), row["ext"].(string), tokens)
		} else {
			value, err = Normalize(processor, data, len(data), row["ext"].(string))
		}
		var actual any
		if err != nil {
			actual = map[string]any{"error": err.Error()}
		} else {
			details := map[string]any{"data": base64.StdEncoding.EncodeToString(value.Buffer), "mediaType": value.MediaType}
			var metadata any
			if value.Dimensions != nil {
				details["dimensions"] = value.Dimensions
				if text := MetadataText(value.Dimensions, func() string { path, _ := row["path"].(string); return path }()); text != "" {
					metadata = text
				}
			}
			actual = map[string]any{"value": details, "metadata": metadata}
		}
		gotRaw, _ := json.Marshal(actual)
		var got, want any
		_ = json.Unmarshal(gotRaw, &got)
		_ = json.Unmarshal(expected[i], &want)
		if !reflect.DeepEqual(got, want) {
			var source struct {
				Value struct {
					Data       string
					Dimensions *Dimensions
					MediaType  string
				}
				Error string
			}
			_ = json.Unmarshal(expected[i], &source)
			sourceBytes, _ := base64.StdEncoding.DecodeString(source.Value.Data)
			goMetadata, _ := processor.Metadata(value.Buffer)
			sourceMetadata, _ := processor.Metadata(sourceBytes)
			goPixels, _, goErr := image.Decode(bytes.NewReader(value.Buffer))
			sourcePixels, _, sourceErr := image.Decode(bytes.NewReader(sourceBytes))
			samePixels := goErr == nil && sourceErr == nil && reflect.DeepEqual(goPixels, sourcePixels)
			different := 0
			if goErr == nil && sourceErr == nil && goPixels.Bounds() == sourcePixels.Bounds() {
				for y := 0; y < goPixels.Bounds().Dy(); y++ {
					for x := 0; x < goPixels.Bounds().Dx(); x++ {
						r, g, b, a := goPixels.At(x, y).RGBA()
						rr, gg, bb, aa := sourcePixels.At(x, y).RGBA()
						if r != rr || g != gg || b != bb || a != aa {
							different++
						}
					}
				}
			}
			t.Errorf("case%d ext%s tokens%v Go(%d,%s,%v,%v,%v) Source(%d,%s,%v,%s,%v)", i, row["ext"], row["tokens"], len(value.Buffer), value.MediaType, value.Dimensions, err, goMetadata, len(sourceBytes), source.Value.MediaType, source.Value.Dimensions, source.Error, sourceMetadata)
			t.Log("native/source pixels equal", samePixels, "different", different)
		}
	}
}
