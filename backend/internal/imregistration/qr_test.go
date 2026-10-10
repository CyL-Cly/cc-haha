package imregistration

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"strings"
	"testing"
)

func TestSharedNativeQRDataURLUsesOriginalDisplaySize(t *testing.T) {
	text := "https://q.qq.com/qqbot/openclaw/connect.html?task_id=private&source=claude-code-haha&_wv=2"
	data, e := QRDataURL(text)
	if e != nil {
		t.Fatal(e)
	}
	p, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(data, "data:image/png;base64,"))
	if e != nil {
		t.Fatal(e)
	}
	img, e := png.Decode(bytes.NewReader(p))
	if e != nil || img.Bounds().Dx() != 220 || img.Bounds().Dy() != 220 {
		t.Fatal("actual native display QR", e)
	}
}
