package storage

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestNodeCompatibleUTF8Replacement(t *testing.T) {
	for _, fixture := range []struct {
		input []byte
		want  string
	}{
		{[]byte("中文 😀"), "中文 😀"},
		{[]byte("\ufeff\ufefffile"), "\ufeff\ufefffile"},
		{[]byte{0xff, 0xfe}, "\ufffd\ufffd"},
		{[]byte{0xe2, 0x82}, "\ufffd"},
		{[]byte{0xe2, 0x82, 'A'}, "\ufffdA"},
		{[]byte{0xed, 0xa0, 0x80}, "\ufffd\ufffd\ufffd"},
		{[]byte{0xf0, 0x80, 0x80, 0x80}, "\ufffd\ufffd\ufffd\ufffd"},
		{[]byte{0xf4, 0x90, 0x80, 0x80}, "\ufffd\ufffd\ufffd\ufffd"},
		{[]byte{0xc0, 0xaf}, "\ufffd\ufffd"},
		{[]byte{0xc2}, "\ufffd"},
	} {
		if got := string(DecodeUTF8(fixture.input)); got != fixture.want {
			t.Errorf("decode %x = %q, want %q", fixture.input, got, fixture.want)
		}
	}
}

func TestInvalidUTF8InsideJSONStringRemainsReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	raw := append([]byte(`{"future":"`), 0xff, 0xfe, 0xe2, 0x82)
	raw = append(raw, []byte(`"}`)...)
	writeFixture(t, path, raw)
	value, err := readObject(quietStore(), path, Object{})
	var text string
	decodeErr := json.Unmarshal(value["future"], &text)
	if err != nil || decodeErr != nil || text != "\ufffd\ufffd\ufffd" {
		t.Fatalf("replacement JSON = %q, %v, %v", text, err, decodeErr)
	}
	assertFile(t, path, raw)
}
