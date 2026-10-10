package binary

import (
	"errors"
	"testing"

	"go.mau.fi/whatsmeow/binary/token"
	"go.mau.fi/whatsmeow/types"
)

func TestInitializedBinaryErrorsInRealWireDecoderAndEncoder(t *testing.T) {
	for attempt := 0; attempt < 2; attempt++ {
		_, err := newDecoder([]byte{token.JIDPair, token.ListEmpty, token.ListEmpty}).read(true)
		if err != ErrInvalidJIDType || err.Error() != "invalid JID type" {
			t.Fatal("invalid JID", err)
		}
		_, err = Unmarshal([]byte{token.List8, 0, token.Binary8, 0})
		if err != ErrInvalidNode || err.Error() != "invalid node" {
			t.Fatal("empty node", err)
		}
		_, err = newDecoder([]byte{token.ListEmpty}).readAttributes(1)
		if !errors.Is(err, ErrNonStringKey) || err.Error() != "non-string key at position 1 (string): " {
			t.Fatal("attribute key", err)
		}
		_, err = newDecoder([]byte{244}).read(true)
		if !errors.Is(err, ErrInvalidToken) || err.Error() != "invalid token with tag 244 at position 1" {
			t.Fatal("invalid token", err)
		}
		func() {
			defer func() {
				err, ok := recover().(error)
				if !ok || !errors.Is(err, ErrInvalidType) || err.Error() != "unsupported payload type: struct {}" {
					t.Fatal("unsupported payload panic", err)
				}
			}()
			_, _ = Marshal(Node{Tag: "message", Content: struct{}{}})
			t.Fatal("unsupported payload was accepted")
		}()
	}
	node := Node{Tag: "message", Attrs: Attrs{"to": types.MetaAIJID}, Content: []byte("private binary payload")}
	raw, err := Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Unmarshal(raw[1:])
	if err != nil || decoded.Attrs["to"] != types.MetaAIJID || string(decoded.Content.([]byte)) != "private binary payload" {
		t.Fatal("real codec round trip", decoded, err)
	}
	if ErrInvalidNode == ErrInvalidJIDType || ErrInvalidType == ErrNonStringKey || ErrInvalidToken == ErrInvalidNode {
		t.Fatal("binary sentinels became aliases")
	}
}
