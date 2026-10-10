package whatsappcandidate

import (
	"encoding/json"
	"testing"

	"go.mau.fi/whatsmeow"
	wab "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
)

func TestForkMessageOfflineCallbackTypesMatchActualOriginalQueueAdmission(t *testing.T) {
	// The actual binary wire carries strings; the public typed Node consumer also
	// accepts bool/int/null/container attributes. Verify Source callback admission
	// for both domains instead of claiming non-string attributes exist on wire.
	values := []any{nil, "", "0", "false", "1", false, true, 0, 1, -1, []any{}, map[string]any{}}
	source := originalForkEdges(t, map[string]any{"op": "offline", "values": values})
	var outcomes []string
	if err := json.Unmarshal(source["outcomes"], &outcomes); err != nil {
		t.Fatal(err)
	}
	device, _, err := OpenNodeDevice(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	id := types.NewJID("222", types.DefaultUserServer)
	device.ID = &id
	client := whatsmeow.NewClient(device, nil)
	defer client.Disconnect()
	for i, value := range values {
		node := &wab.Node{Tag: "message", Attrs: wab.Attrs{"id": "PRIVATE_OFFLINE", "from": types.NewJID("111", types.DefaultUserServer), "t": "1700000000", "offline": value}}
		info, err := client.DangerousInternals().ParseMessageInfo(node)
		if err != nil {
			t.Fatal(i, err)
		}
		got := "notify"
		if info.SourceOffline {
			got = "append"
		}
		if got != outcomes[i] {
			t.Fatalf("typed message offline %#v: native %s original %s", value, got, outcomes[i])
		}
	}
	// Absent property, unlike a present null, still follows the default branch.
	info, err := client.DangerousInternals().ParseMessageInfo(&wab.Node{Tag: "message", Attrs: wab.Attrs{"id": "PRIVATE_ABSENT", "from": types.NewJID("111", types.DefaultUserServer), "t": "1700000000"}})
	if err != nil || info.SourceOffline {
		t.Fatal("absent offline", info, err)
	}
}
