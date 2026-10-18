package whatsappcandidate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"go.mau.fi/whatsmeow"
	wab "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.mau.fi/whatsmeow/util/keys"
)

func TestActualNoiseGroupQueriesNotificationsAndColdParticipantMapping(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	row := groupMetadataRows()[31]
	sourceMetadata := actualGroupMetadata(t, []groupSourceNode{row})[0]
	group := groupWire(t, row)
	groupJID := types.NewJID(row.Attrs["id"], types.GroupServer)
	actionRows := groupActionRows(groupJID.String())
	actionReceipts := actualGroupActions(t, actionRows)
	type groupActionQuery struct {
		request  json.RawMessage
		response groupSourceNode
	}
	actionQueries := make(chan groupActionQuery, 2)
	root, intermediate, static := keys.NewKeyPair(), keys.NewKeyPair(), keys.NewKeyPair()
	oldPin := whatsmeow.WACertPubKey
	whatsmeow.WACertPubKey = *root.Pub
	defer func() { whatsmeow.WACertPubKey = oldPin }()
	source := actualNodePeer(t, map[string]any{"op": "setup"})
	creds := nodeObj(nodeObj(source["b"])["creds"])
	creds["me"] = nodeJSON(map[string]any{"id": "222:3@s.whatsapp.net", "name": "private-group-owner", "lid": "99222:3@lid"})
	creds["registered"] = nodeJSON(true)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "creds.json"), nodeJSON(creds), 0600); err != nil {
		t.Fatal(err)
	}
	send := make(chan wab.Node, 8)
	acks := make(chan string, 8)
	eventsSeen := make(chan any, 8)
	connected := make(chan struct{}, 1)
	closed := make(chan struct{})
	peerErrors := make(chan error, 8)
	var queries atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(closed)
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			peerErrors <- err
			return
		}
		defer conn.CloseNow()
		wire, payload, err := acceptPrivateNoise(ctx, conn, root, intermediate, static)
		if err != nil {
			peerErrors <- err
			return
		}
		if payload.GetUsername() != 222 || payload.GetDevice() != 3 {
			peerErrors <- fmt.Errorf("private old credential login differs")
			return
		}
		if err = wire.Send(wab.Node{Tag: "success", Attrs: wab.Attrs{"lid": types.JID{User: "99222", Device: 3, Server: types.HiddenUserServer}, "t": int64(1700000000)}}); err != nil {
			peerErrors <- err
			return
		}
		peerCtx, peerCancel := context.WithCancel(ctx)
		defer peerCancel()
		writerDone := make(chan struct{})
		go func() {
			defer close(writerDone)
			for {
				select {
				case node := <-send:
					if err := wire.Send(node); err != nil {
						if peerCtx.Err() == nil {
							peerErrors <- err
						}
						return
					}
				case <-peerCtx.Done():
					return
				}
			}
		}()
		defer func() { peerCancel(); <-writerDone }()
		for {
			node, err := wire.Read()
			if err != nil {
				return
			}
			if node.Tag == "ack" {
				if class, _ := node.Attrs["class"].(string); class == "notification" {
					acks <- node.AttrGetter().String("id")
				}
				continue
			}
			if node.Tag != "iq" {
				continue
			}
			result := wab.Node{Tag: "iq", Attrs: wab.Attrs{"id": node.Attrs["id"], "type": "result", "from": types.ServerJID}}
			if node.Attrs["xmlns"] == "w:g2" {
				queries.Add(1)
				select {
				case expected := <-actionQueries:
					var original, actual any
					if json.Unmarshal(expected.request, &original) != nil || json.Unmarshal(nodeJSON(groupActionPhysicalNode(*node, true)), &actual) != nil || !reflect.DeepEqual(original, actual) {
						peerErrors <- fmt.Errorf("physical group action differs from installed original: Go %s Source %s", nodeJSON(actual), expected.request)
						return
					}
					result.Content = groupWire(t, expected.response).Content
				default:
					if query := node.GetChildByTag("query"); query.Tag == "query" {
						if node.Attrs["to"] != groupJID || query.Attrs["request"] != "interactive" {
							peerErrors <- fmt.Errorf("actual group query wire differs: %s", node.XMLString())
							return
						}
						result.Content = []wab.Node{group}
					} else if query := node.GetChildByTag("participating"); query.Tag == "participating" {
						if len(query.GetChildren()) != 2 || query.GetChildByTag("participants").Tag != "participants" || query.GetChildByTag("description").Tag != "description" {
							peerErrors <- fmt.Errorf("actual joined-group query wire differs: %s", node.XMLString())
							return
						}
						result.Content = []wab.Node{{Tag: "groups", Content: []wab.Node{{Tag: "future_group_list"}, group}}}
					} else {
						peerErrors <- fmt.Errorf("unexpected group IQ: %s", node.XMLString())
						return
					}
				}
			} else if node.GetChildByTag("count").Tag == "count" {
				result.Content = []wab.Node{{Tag: "count", Attrs: wab.Attrs{"value": "50"}}}
			}
			if err = wire.Send(result); err != nil {
				return
			}
		}
	}))
	defer peer.Close()
	n, err := NewNodeWhatsAppClient(ctx, NodeWhatsAppOptions{AuthDir: dir, HTTPTransport: waSocketRoute{peer}, DisableAutoReconnect: true, OnEvent: func(value any) {
		switch value.(type) {
		case *events.Connected:
			connected <- struct{}{}
		case *events.JoinedGroup, *events.GroupInfo:
			eventsSeen <- value
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close(context.Background())
	if err = n.Connect(ctx); err != nil {
		t.Fatal("real signed Noise connect", err)
	}
	select {
	case <-connected:
	case err := <-peerErrors:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("real Connected event missing")
	}
	info, err := n.Client.GetGroupInfo(ctx, groupJID)
	if err != nil || !reflect.DeepEqual(sharedGroupMetadata(info), sourceMetadata) {
		t.Fatal("actual physical group info differs from original decoder", err)
	}
	joined, err := n.Client.GetJoinedGroups(ctx)
	if err != nil || len(joined) != 1 || !reflect.DeepEqual(sharedGroupMetadata(joined[0]), sourceMetadata) {
		select {
		case cause := <-peerErrors:
			t.Fatal("actual joined-group query peer", cause, err)
		default:
		}
		t.Fatal("actual joined-group query lost metadata or unknown child filtering", err)
	}
	initial := []types.JID{types.NewJID("111", types.DefaultUserServer), types.NewJID("99222", types.HiddenUserServer), types.NewJID("99333", types.HiddenUserServer)}
	assertMembers := func(want []types.JID) {
		t.Helper()
		cached, err := n.Client.DangerousInternals().GetCachedGroupData(ctx, groupJID)
		if err != nil || !reflect.DeepEqual(cached.Members, want) || cached.AddressingMode != types.AddressingModeLID || !cached.CommunityAnnouncementGroup {
			t.Fatal("actual cache consumer differs", cached, err)
		}
		if queries.Load() != 2 {
			t.Fatal("notification/cache caused an extra physical group request", queries.Load())
		}
	}
	assertMembers(initial)
	waitAck := func(id string) {
		t.Helper()
		select {
		case got := <-acks:
			if got != id {
				t.Fatal("physical notification acknowledgement identity", got, id)
			}
		case err := <-peerErrors:
			t.Fatal(err)
		case <-ctx.Done():
			t.Fatal("physical notification acknowledgement missing", id)
		}
	}
	notify := func(id string, child wab.Node) any {
		t.Helper()
		send <- wab.Node{Tag: "notification", Attrs: wab.Attrs{"id": id, "type": "w:gp2", "from": groupJID, "participant": initial[1], "participant_pn": types.NewJID("222", types.DefaultUserServer), "t": int64(1700000003)}, Content: []wab.Node{child}}
		select {
		case value := <-eventsSeen:
			waitAck(id)
			return value
		case err := <-peerErrors:
			t.Fatal(err)
		case <-ctx.Done():
			t.Fatal("physical notification event missing", id)
		}
		return nil
	}
	added := types.NewJID("99444", types.HiddenUserServer)
	phone := types.NewJID("444", types.DefaultUserServer)
	change, ok := notify("add", wab.Node{Tag: "add", Attrs: wab.Attrs{"prev_v_id": "old", "v_id": "next", "reason": "invite"}, Content: []wab.Node{{Tag: "participant", Attrs: wab.Attrs{"jid": added, "phone_number": phone}}, {Tag: "future_participant"}}}).(*events.GroupInfo)
	if !ok || !reflect.DeepEqual(change.Join, []types.JID{added}) || change.PrevParticipantVersionID != "old" || change.ParticipantVersionID != "next" || change.JoinReason != "invite" {
		t.Fatal("actual participant-add event differs", change)
	}
	assertMembers(append(append([]types.JID{}, initial...), added))
	if _, ok = notify("duplicate", wab.Node{Tag: "add", Content: []wab.Node{{Tag: "participant", Attrs: wab.Attrs{"jid": added, "phone_number": phone}}}}).(*events.GroupInfo); !ok {
		t.Fatal("duplicate notification lost event")
	}
	assertMembers(append(append([]types.JID{}, initial...), added))
	if _, ok = notify("remove", wab.Node{Tag: "remove", Content: []wab.Node{{Tag: "participant", Attrs: wab.Attrs{"jid": added, "phone_number": phone}}}}).(*events.GroupInfo); !ok {
		t.Fatal("remove notification lost event")
	}
	assertMembers(initial)
	if _, ok = notify("create", wab.Node{Tag: "create", Attrs: wab.Attrs{"reason": "invite", "key": "private-create", "type": "new"}, Content: []wab.Node{group}}).(*events.JoinedGroup); !ok {
		t.Fatal("actual create notification did not expose joined group")
	}
	assertMembers(initial)
	// Malformed notifications are acknowledged without publishing a half-parsed
	// event, changing the populated participant cache, or issuing another IQ.
	for _, invalid := range []struct {
		id    string
		child wab.Node
	}{
		{"missing-group", wab.Node{Tag: "create", Content: []wab.Node{{Tag: "future_group"}}}},
		{"invalid-description", wab.Node{Tag: "description", Attrs: wab.Attrs{"id": "invalid-topic"}, Content: []wab.Node{{Tag: "body", Content: []wab.Node{{Tag: "not-a-text-body"}}}}}},
	} {
		send <- wab.Node{Tag: "notification", Attrs: wab.Attrs{"id": invalid.id, "type": "w:gp2", "from": groupJID, "t": int64(1700000004)}, Content: []wab.Node{invalid.child}}
		waitAck(invalid.id)
		select {
		case value := <-eventsSeen:
			t.Fatal("malformed notification published an event", value)
		default:
		}
		assertMembers(initial)
	}
	for i, action := range actionRows {
		if !t.Run("original-group-action-"+action.Name, func(t *testing.T) {
			receipt := actionReceipts[i]
			for j, encoded := range receipt.EncodedRequests {
				// The SDKs display the empty-user group-server JID differently
				// (@g.us versus g.us). Compare the actual original encoder bytes
				// through the same wire decoder used by the real Noise peer.
				if len(encoded) == 0 {
					t.Fatal("empty installed original group action frame")
				}
				plain, err := wab.Unpack(encoded)
				if err != nil {
					t.Fatal("installed original group action frame unpack", err)
				}
				original, err := wab.Unmarshal(plain)
				if err != nil {
					t.Fatal("installed original group action wire decode", err)
				}
				actionQueries <- groupActionQuery{request: nodeJSON(groupActionPhysicalNode(*original, true)), response: action.Responses[j]}
			}
			value, err := runGroupAction(ctx, n.Client, groupJID, action)
			if err != nil {
				select {
				case peerError := <-peerErrors:
					t.Fatal("physical original group action", peerError, err)
				default:
					t.Fatal("public group action", action.Name, err)
				}
			}
			if len(receipt.Value) == 0 {
				if value != nil {
					t.Fatal("void group action produced a result", value)
				}
				return
			}
			var expected, actual any
			if json.Unmarshal(receipt.Value, &expected) != nil || json.Unmarshal(nodeJSON(value), &actual) != nil || !reflect.DeepEqual(expected, actual) {
				t.Fatal("group action result differs from original", action.Name, expected, actual)
			}
		}) {
			t.FailNow() // Never enqueue more requests after a failed socket peer.
		}
	}
	if err = n.Close(ctx); err != nil {
		t.Fatal("private socket close/credential write join", err)
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("physical encrypted connection was not closed")
	}
	cold, coldStore, err := OpenNodeDevice(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := cold.LIDs.GetPNForLID(ctx, added); err != nil || got != phone {
		t.Fatal("actual notification's durable mapping missing after cold reopen", got, err)
	}
	if err = coldStore.WaitCredsSave(ctx); err != nil {
		t.Fatal(err)
	}
	if queries.Load() != int32(2+len(actionRows)) {
		t.Fatal("close/cold restore caused unexpected network request")
	}
}
