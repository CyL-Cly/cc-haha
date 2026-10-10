package imruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"
)

func TestWhatsAppImageSourceFileProducerCompletionBoundary(t *testing.T) {
	library, err := filepath.Abs("../../../node_modules/@img/sharp-libvips-darwin-arm64/lib/libvips-cpp.8.17.3.dylib")
	if err != nil {
		t.Fatal(err)
	}
	codec, err := imageprocessing.BundledCodec(library)
	if err != nil {
		t.Fatal(err)
	}
	pixels := image.NewNRGBA(image.Rect(0, 0, 64, 1))
	for x := 0; x < 64; x++ {
		pixels.SetNRGBA(x, 0, color.NRGBA{uint8(x * 7), 0, uint8(x), 137})
	}
	var input bytes.Buffer
	if err := png.Encode(&input, pixels); err != nil {
		t.Fatal(err)
	}
	data, err := codec.Encode(input.Bytes(), 64, 1, "jpeg", 80)
	if err != nil {
		t.Fatal(err)
	}
	got, err := WhatsAppImageThumbnail(codec, data)
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Result   json.RawMessage `json:"result"`
		Warnings []string        `json:"warnings"`
		Ready    []struct {
			File         string `json:"file"`
			Finished     bool   `json:"writableFinished"`
			Closed       bool   `json:"closed"`
			BytesWritten int    `json:"bytesWritten"`
			FD           *int   `json:"fd"`
		} `json:"ready"`
		Disk []struct {
			File   string `json:"file"`
			Length int    `json:"length"`
			SHA    string `json:"sha256"`
		} `json:"disk"`
		InputSHA  string                      `json:"inputSHA256"`
		DirectSHA string                      `json:"directSHA256"`
		Unrelated struct{ Width, Height int } `json:"unrelated"`
		Buffer    struct{ Width, Height int } `json:"buffer"`
	}
	encoded := whatsAppOriginal(t, "native-whatsapp-image-file-producer.fixture.ts", map[string]any{"data": base64.StdEncoding.EncodeToString(data), "mime": "image/jpeg"})
	if err := json.Unmarshal(encoded, &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Ready) != 1 || len(source.Disk) != 1 {
		t.Fatalf("expected one owned SDK producer, Source=%s", encoded)
	}
	ready, disk := source.Ready[0], source.Disk[0]
	inputSHA, jpegSHA := sha256.Sum256(data), sha256.Sum256(got.JPEG)
	if !ready.Finished || !ready.Closed || ready.FD != nil || ready.BytesWritten != len(data) || disk.Length != len(data) || disk.File != ready.File || disk.SHA != hex.EncodeToString(inputSHA[:]) || source.InputSHA != disk.SHA {
		t.Fatalf("metadata entered before actual SDK file completed, Source=%s", encoded)
	}
	want := object(source.Result)
	var sourceDimensions struct{ Width, Height int }
	if err := json.Unmarshal(source.Result, &sourceDimensions); err != nil {
		t.Fatal(err)
	}
	if len(source.Warnings) != 0 || source.DirectSHA != hex.EncodeToString(jpegSHA[:]) || !bytes.Equal(got.JPEG, nodeBase64(want.String("jpegThumbnail"))) || sourceDimensions.Width != 64 || sourceDimensions.Height != 1 {
		t.Fatalf("actual Sharp/SDK media changed at completed file boundary, Source=%s", encoded)
	}
	if source.Unrelated.Width != 64 || source.Unrelated.Height != 1 || source.Buffer.Width != 64 || source.Buffer.Height != 1 {
		t.Fatalf("producer boundary affected unrelated file or Buffer, Source=%s", encoded)
	}
}
