package imruntime

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"
)

func TestWhatsAppNativeThumbnailBytesAndDimensionsActualSharp(t *testing.T) {
	library, e := filepath.Abs("../../../node_modules/@img/sharp-libvips-darwin-arm64/lib/libvips-cpp.8.17.3.dylib")
	if e != nil {
		t.Fatal(e)
	}
	codec, e := imageprocessing.BundledCodec(library)
	if e != nil {
		t.Fatal(e)
	}
	for _, size := range []image.Point{{123, 83}, {7, 5}, {1, 1}, {64, 33}, {64, 31}, {64, 1}} {
		for _, alpha := range []uint8{255, 137, 0} {
			pixels := image.NewNRGBA(image.Rect(0, 0, size.X, size.Y))
			for y := 0; y < size.Y; y++ {
				for x := 0; x < size.X; x++ {
					pixels.SetNRGBA(x, y, color.NRGBA{uint8(x * 7), uint8(y * 3), uint8(x + y), alpha})
				}
			}
			var buf bytes.Buffer
			png.Encode(&buf, pixels)
			for _, format := range []string{"png", "jpeg", "webp"} {
				data := buf.Bytes()
				if format != "png" {
					data, e = codec.Encode(data, size.X, size.Y, format, 80)
					if e != nil {
						t.Fatal(e)
					}
				}
				want := object(whatsAppOriginal(t, "native-whatsapp-media-metadata.fixture.ts", map[string]any{"data": base64.StdEncoding.EncodeToString(data), "mime": "image/" + format}))
				got, e := WhatsAppImageThumbnail(codec, data)
				if e != nil {
					t.Fatalf("%dx%d alpha%d %s nativeErr=%v actualSource=%s", size.X, size.Y, alpha, format, e, raw(want))
				}
				if !bytes.Equal(got.JPEG, nodeBase64(want.String("jpegThumbnail"))) || int(got.Width) != size.X || int(got.Height) != size.Y {
					t.Fatalf("actual Sharp %dx%d alpha%d %s differs bytes=%t", size.X, size.Y, alpha, format, bytes.Equal(got.JPEG, nodeBase64(want.String("jpegThumbnail"))))
				}
			}
		}
	}
}
