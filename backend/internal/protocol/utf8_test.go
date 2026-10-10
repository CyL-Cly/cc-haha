package protocol

import (
	"bytes"
	"testing"
)

func TestJavaScriptUTF8ReplacementAndBOMPreservation(t *testing.T) {
	for _, fixture := range []struct {
		input []byte
		want  string
	}{
		{nil, ""},
		{[]byte("ascii 中文 é 😀 \ufeff"), "ascii 中文 é 😀 \ufeff"},
		{[]byte("\ufeff\ufeffdata"), "\ufeff\ufeffdata"},
		{[]byte{0x80, 0xbf, 0xff, 0xfe}, "\ufffd\ufffd\ufffd\ufffd"},
		{[]byte{0xc2}, "\ufffd"},
		{[]byte{0xc2, 'x'}, "\ufffdx"},
		{[]byte{0xe2, 0x82}, "\ufffd"},
		{[]byte{0xe2, 0x82, 'x'}, "\ufffdx"},
		{[]byte{0xe0, 0x80, 0x80}, "\ufffd\ufffd\ufffd"},
		{[]byte{0xed, 0xa0, 0x80}, "\ufffd\ufffd\ufffd"},
		{[]byte{0xf0, 0x80, 0x80, 0x80}, "\ufffd\ufffd\ufffd\ufffd"},
		{[]byte{0xf4, 0x90, 0x80, 0x80}, "\ufffd\ufffd\ufffd\ufffd"},
		{[]byte{0xf4, 0x8f, 0x80, 'x'}, "\ufffdx"},
		{[]byte{0xf4, 0x8f, 0xbf, 0xbf}, "\U0010ffff"},
		{[]byte{0xc0, 0xaf}, "\ufffd\ufffd"},
	} {
		before := bytes.Clone(fixture.input)
		actual := DecodeJavaScriptUTF8(fixture.input)
		if string(actual) != fixture.want || !bytes.Equal(fixture.input, before) {
			t.Errorf("decode %x = %q, want %q; input now %x", before, actual, fixture.want, fixture.input)
		}
		if len(actual) > 0 {
			actual[0] = 'X'
			if !bytes.Equal(fixture.input, before) {
				t.Fatal("decoded output aliases caller bytes")
			}
		}
	}
}
