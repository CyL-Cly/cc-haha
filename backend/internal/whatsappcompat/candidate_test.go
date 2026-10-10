package whatsappcandidate

import (
	"go.mau.fi/libsignal/serialize"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"runtime"
	"testing"
)

func TestPinnedWhatsmeowAndSignalCompileWithGo1257(t *testing.T) {
	if runtime.Version() != "go1.25.7" {
		t.Fatalf("unexpected toolchain %s", runtime.Version())
	}
	client := whatsmeow.NewClient(&store.Device{}, nil)
	if client == nil || client.IsConnected() {
		t.Fatal("unstarted client claimed connected")
	}
	if serialize.NewProtoBufSerializer() == nil {
		t.Fatal("Signal serializer missing")
	}
	t.Log(runtime.Version(), "pinned native protocol compiled; no account/network socket")
}
