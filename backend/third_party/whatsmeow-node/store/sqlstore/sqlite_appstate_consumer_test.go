package sqlstore_test

// Offline action-consumer tests, not app-state patch decoding or WhatsApp
// account acceptance. Existing helpers create synthetic devices in t.TempDir.
// DispatchAppState returns the production typed event and writes actual SQLite.
// No Client constructor, Connect, socket or real account/key/config is used.

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waServerSync"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

func appStateStorageClient(device *store.Device) *whatsmeow.Client {
	return &whatsmeow.Client{Store: device, Log: waLog.Noop}
}

func appStateStorageDevice(t *testing.T, container *sqlstore.Container, id types.JID) *store.Device {
	t.Helper()
	device, err := container.GetDevice(context.Background(), id)
	requireSQL(t, err)
	if device == nil || device.ChatSettings == nil || device.Contacts == nil {
		t.Fatal("cold-opened synthetic device lacks its real chat/contact SQL stores")
	}
	return device
}

func appStateSetMutation(index, jid string, timestamp int64, action *waSyncAction.SyncActionValue) appstate.Mutation {
	action.Timestamp = proto.Int64(timestamp)
	return appstate.Mutation{Operation: waServerSync.SyncdMutation_SET, Index: []string{index, jid}, Action: action}
}

func assertAppStateEventHeader(t *testing.T, gotJID types.JID, gotTime time.Time, gotFullSync bool, wantJID types.JID, wantTime time.Time, wantFullSync bool) {
	t.Helper()
	if gotJID != wantJID || !gotTime.Equal(wantTime) || gotFullSync != wantFullSync {
		t.Fatalf("typed event identity/time/fullSync: jid=%s time=%s fullSync=%v; want jid=%s time=%s fullSync=%v", gotJID, gotTime, gotFullSync, wantJID, wantTime, wantFullSync)
	}
}

func assertAppStateChat(t *testing.T, device *store.Device, jid types.JID, want types.LocalChatSettings) {
	t.Helper()
	got, err := device.ChatSettings.GetChatSettings(context.Background(), jid)
	requireSQL(t, err)
	if got.Found != want.Found || !got.MutedUntil.Equal(want.MutedUntil) || got.Pinned != want.Pinned || got.Archived != want.Archived {
		t.Fatalf("durable chat settings: got=%+v want=%+v", got, want)
	}
}

func assertAppStateContact(t *testing.T, device *store.Device, jid types.JID, want types.ContactInfo) {
	t.Helper()
	got, err := device.Contacts.GetContact(context.Background(), jid)
	requireSQL(t, err)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("contact names/cache/durable row: got=%+v want=%+v", got, want)
	}
}

func TestSQLiteAppStateMutePinArchiveTypedEventsAndColdDurability(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app-state-chat.db")
	container := openStore(t, path)
	device := savedDevice(t, container, "15550201")
	sibling := savedDevice(t, container, "15550202")
	id, siblingID := *device.ID, *sibling.ID
	chat := types.JID{User: "15550291", Server: "s.whatsapp.net"}
	const rawChat = "15550291@s.whatsapp.net"
	assertAppStateChat(t, device, chat, types.LocalChatSettings{})
	requireSQL(t, sibling.ChatSettings.PutPinned(ctx, chat, true))
	client := appStateStorageClient(device)
	mutation := appStateSetMutation("mute", rawChat, 1700000000123, &waSyncAction.SyncActionValue{
		MuteAction: &waSyncAction.MuteAction{Muted: proto.Bool(true), MuteEndTimestamp: proto.Int64(1700003600000)},
	})
	got := client.DangerousInternals().DispatchAppState(ctx, appstate.WAPatchRegularHigh, mutation, true)
	mute, ok := got.(*events.Mute)
	if !ok || mute.Action == nil || !mute.Action.GetMuted() || mute.Action.GetMuteEndTimestamp() != 1700003600000 {
		t.Fatalf("mute SET did not return its actual typed action: %#v", got)
	}
	assertAppStateEventHeader(t, mute.JID, mute.Timestamp, mute.FromFullSync, chat, time.Date(2023, 11, 14, 22, 13, 20, 123000000, time.UTC), true)
	finiteMute := time.Date(2023, 11, 14, 23, 13, 20, 0, time.UTC)
	assertAppStateChat(t, device, chat, types.LocalChatSettings{Found: true, MutedUntil: finiteMute})
	mutation = appStateSetMutation("pin_v1", rawChat, 1700000001123, &waSyncAction.SyncActionValue{PinAction: &waSyncAction.PinAction{Pinned: proto.Bool(true)}})
	got = client.DangerousInternals().DispatchAppState(ctx, appstate.WAPatchRegularLow, mutation, false)
	pin, ok := got.(*events.Pin)
	if !ok || pin.Action == nil || !pin.Action.GetPinned() {
		t.Fatalf("pin SET did not return its actual typed action: %#v", got)
	}
	assertAppStateEventHeader(t, pin.JID, pin.Timestamp, pin.FromFullSync, chat, time.Date(2023, 11, 14, 22, 13, 21, 123000000, time.UTC), false)
	assertAppStateChat(t, device, chat, types.LocalChatSettings{Found: true, MutedUntil: finiteMute, Pinned: true})
	mutation = appStateSetMutation("archive", rawChat, 1700000002123, &waSyncAction.SyncActionValue{ArchiveChatAction: &waSyncAction.ArchiveChatAction{Archived: proto.Bool(true)}})
	got = client.DangerousInternals().DispatchAppState(ctx, appstate.WAPatchRegularLow, mutation, true)
	archive, ok := got.(*events.Archive)
	if !ok || archive.Action == nil || !archive.Action.GetArchived() {
		t.Fatalf("archive SET did not return its actual typed action: %#v", got)
	}
	assertAppStateEventHeader(t, archive.JID, archive.Timestamp, archive.FromFullSync, chat, time.Date(2023, 11, 14, 22, 13, 22, 123000000, time.UTC), true)
	// A single inbound archive mutation must not overwrite unrelated columns.
	// BuildArchive's separate unpin mutation is outside this consumer test.
	assertAppStateChat(t, device, chat, types.LocalChatSettings{Found: true, MutedUntil: finiteMute, Pinned: true, Archived: true})
	requireSQL(t, container.Close())
	container = openStore(t, path)
	device = appStateStorageDevice(t, container, id)
	sibling = appStateStorageDevice(t, container, siblingID)
	assertAppStateChat(t, device, chat, types.LocalChatSettings{Found: true, MutedUntil: finiteMute, Pinned: true, Archived: true})
	assertAppStateChat(t, sibling, chat, types.LocalChatSettings{Found: true, Pinned: true})
	client = appStateStorageClient(device)
	mutation = appStateSetMutation("mute", rawChat, 1700000003123, &waSyncAction.SyncActionValue{MuteAction: &waSyncAction.MuteAction{Muted: proto.Bool(true), MuteEndTimestamp: proto.Int64(-1)}})
	got = client.DangerousInternals().DispatchAppState(ctx, appstate.WAPatchRegularHigh, mutation, false)
	mute, ok = got.(*events.Mute)
	if !ok || mute.Action == nil || !mute.Action.GetMuted() || mute.Action.GetMuteEndTimestamp() != -1 {
		t.Fatalf("indefinite mute lost its typed action: %#v", got)
	}
	assertAppStateEventHeader(t, mute.JID, mute.Timestamp, mute.FromFullSync, chat, time.Date(2023, 11, 14, 22, 13, 23, 123000000, time.UTC), false)
	assertAppStateChat(t, device, chat, types.LocalChatSettings{Found: true, MutedUntil: store.MutedForever, Pinned: true, Archived: true})
	requireSQL(t, container.Close())
	container = openStore(t, path)
	device = appStateStorageDevice(t, container, id)
	assertAppStateChat(t, device, chat, types.LocalChatSettings{Found: true, MutedUntil: store.MutedForever, Pinned: true, Archived: true})
	client = appStateStorageClient(device)
	mutation = appStateSetMutation("mute", rawChat, 1700000004123, &waSyncAction.SyncActionValue{MuteAction: &waSyncAction.MuteAction{Muted: proto.Bool(false), MuteEndTimestamp: proto.Int64(1700003600000)}})
	got = client.DangerousInternals().DispatchAppState(ctx, appstate.WAPatchRegularHigh, mutation, false)
	mute, ok = got.(*events.Mute)
	if !ok || mute.Action == nil || mute.Action.GetMuted() {
		t.Fatalf("unmute SET lost its typed false action: %#v", got)
	}
	assertAppStateEventHeader(t, mute.JID, mute.Timestamp, mute.FromFullSync, chat, time.Date(2023, 11, 14, 22, 13, 24, 123000000, time.UTC), false)
	assertAppStateChat(t, device, chat, types.LocalChatSettings{Found: true, Pinned: true, Archived: true})
	mutation = appStateSetMutation("pin_v1", rawChat, 1700000005123, &waSyncAction.SyncActionValue{PinAction: &waSyncAction.PinAction{Pinned: proto.Bool(false)}})
	got = client.DangerousInternals().DispatchAppState(ctx, appstate.WAPatchRegularLow, mutation, true)
	pin, ok = got.(*events.Pin)
	if !ok || pin.Action == nil || pin.Action.GetPinned() {
		t.Fatalf("unpin SET lost its typed false action: %#v", got)
	}
	assertAppStateEventHeader(t, pin.JID, pin.Timestamp, pin.FromFullSync, chat, time.Date(2023, 11, 14, 22, 13, 25, 123000000, time.UTC), true)
	assertAppStateChat(t, device, chat, types.LocalChatSettings{Found: true, Archived: true})
	mutation = appStateSetMutation("archive", rawChat, 1700000006123, &waSyncAction.SyncActionValue{ArchiveChatAction: &waSyncAction.ArchiveChatAction{Archived: proto.Bool(false)}})
	got = client.DangerousInternals().DispatchAppState(ctx, appstate.WAPatchRegularLow, mutation, false)
	archive, ok = got.(*events.Archive)
	if !ok || archive.Action == nil || archive.Action.GetArchived() {
		t.Fatalf("unarchive SET lost its typed false action: %#v", got)
	}
	assertAppStateEventHeader(t, archive.JID, archive.Timestamp, archive.FromFullSync, chat, time.Date(2023, 11, 14, 22, 13, 26, 123000000, time.UTC), false)
	assertAppStateChat(t, device, chat, types.LocalChatSettings{Found: true})
	requireSQL(t, container.Close())
	container = openStore(t, path)
	device = appStateStorageDevice(t, container, id)
	sibling = appStateStorageDevice(t, container, siblingID)
	assertAppStateChat(t, device, chat, types.LocalChatSettings{Found: true})
	assertAppStateChat(t, sibling, chat, types.LocalChatSettings{Found: true, Pinned: true})
}

func TestSQLiteAppStateContactSetRenameAndEmptyNamesAreTypedAndDurable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app-state-contact.db")
	container := openStore(t, path)
	device := savedDevice(t, container, "15550203")
	sibling := savedDevice(t, container, "15550204")
	id, siblingID := *device.ID, *sibling.ID
	contact := types.JID{User: "15550391", Server: "s.whatsapp.net"}
	const rawContact = "15550391@s.whatsapp.net"
	assertAppStateContact(t, device, contact, types.ContactInfo{}) // Warm the actual negative cache.
	requireSQL(t, sibling.Contacts.PutContactName(ctx, contact, "Sibling", "Sibling Person"))
	client := appStateStorageClient(device)
	mutation := appStateSetMutation("contact", rawContact, 1700000000123, &waSyncAction.SyncActionValue{ContactAction: &waSyncAction.ContactAction{FirstName: proto.String("Owned"), FullName: proto.String("Owned Person")}})
	got := client.DangerousInternals().DispatchAppState(ctx, appstate.WAPatchCriticalUnblockLow, mutation, true)
	event, ok := got.(*events.Contact)
	if !ok || event.Action == nil || event.Action.GetFirstName() != "Owned" || event.Action.GetFullName() != "Owned Person" {
		t.Fatalf("contact SET lost or swapped its typed names: %#v", got)
	}
	assertAppStateEventHeader(t, event.JID, event.Timestamp, event.FromFullSync, contact, time.Date(2023, 11, 14, 22, 13, 20, 123000000, time.UTC), true)
	assertAppStateContact(t, device, contact, types.ContactInfo{Found: true, FirstName: "Owned", FullName: "Owned Person"})
	requireSQL(t, container.Close())
	container = openStore(t, path)
	device = appStateStorageDevice(t, container, id)
	sibling = appStateStorageDevice(t, container, siblingID)
	assertAppStateContact(t, device, contact, types.ContactInfo{Found: true, FirstName: "Owned", FullName: "Owned Person"})
	assertAppStateContact(t, sibling, contact, types.ContactInfo{Found: true, FirstName: "Sibling", FullName: "Sibling Person"})
	_, _, err := device.Contacts.PutPushName(ctx, contact, "preserved push name")
	requireSQL(t, err)
	_, _, err = device.Contacts.PutBusinessName(ctx, contact, "preserved business name")
	requireSQL(t, err)
	client = appStateStorageClient(device)
	mutation = appStateSetMutation("contact", rawContact, 1700000001123, &waSyncAction.SyncActionValue{ContactAction: &waSyncAction.ContactAction{FirstName: proto.String("Renamed"), FullName: proto.String("Renamed Person")}})
	got = client.DangerousInternals().DispatchAppState(ctx, appstate.WAPatchCriticalUnblockLow, mutation, false)
	event, ok = got.(*events.Contact)
	if !ok || event.Action == nil || event.Action.GetFirstName() != "Renamed" || event.Action.GetFullName() != "Renamed Person" {
		t.Fatalf("contact rename did not return the new typed names: %#v", got)
	}
	assertAppStateEventHeader(t, event.JID, event.Timestamp, event.FromFullSync, contact, time.Date(2023, 11, 14, 22, 13, 21, 123000000, time.UTC), false)
	assertAppStateContact(t, device, contact, types.ContactInfo{Found: true, FirstName: "Renamed", FullName: "Renamed Person", PushName: "preserved push name", BusinessName: "preserved business name"})
	mutation = appStateSetMutation("contact", rawContact, 1700000002123, &waSyncAction.SyncActionValue{ContactAction: &waSyncAction.ContactAction{FirstName: proto.String(""), FullName: proto.String("")}})
	got = client.DangerousInternals().DispatchAppState(ctx, appstate.WAPatchCriticalUnblockLow, mutation, true)
	event, ok = got.(*events.Contact)
	if !ok || event.Action == nil || event.Action.GetFirstName() != "" || event.Action.GetFullName() != "" {
		t.Fatalf("explicit empty contact SET lost its typed action: %#v", got)
	}
	assertAppStateEventHeader(t, event.JID, event.Timestamp, event.FromFullSync, contact, time.Date(2023, 11, 14, 22, 13, 22, 123000000, time.UTC), true)
	assertAppStateContact(t, device, contact, types.ContactInfo{Found: true, PushName: "preserved push name", BusinessName: "preserved business name"})
	requireSQL(t, container.Close())
	container = openStore(t, path)
	device = appStateStorageDevice(t, container, id)
	sibling = appStateStorageDevice(t, container, siblingID)
	assertAppStateContact(t, device, contact, types.ContactInfo{Found: true, PushName: "preserved push name", BusinessName: "preserved business name"})
	assertAppStateContact(t, sibling, contact, types.ContactInfo{Found: true, FirstName: "Sibling", FullName: "Sibling Person"})
}

func TestSQLiteAppStateRemoveIsNotAnUnsetAndDoesNotChangeDurableState(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app-state-remove.db")
	container := openStore(t, path)
	device := savedDevice(t, container, "15550205")
	id := *device.ID
	chat := types.JID{User: "15550491", Server: "s.whatsapp.net"}
	missing := types.JID{User: "15550492", Server: "s.whatsapp.net"}
	requireSQL(t, device.ChatSettings.PutMutedUntil(ctx, chat, store.MutedForever))
	requireSQL(t, device.ChatSettings.PutPinned(ctx, chat, true))
	requireSQL(t, device.ChatSettings.PutArchived(ctx, chat, true))
	requireSQL(t, device.Contacts.PutContactName(ctx, chat, "Retained", "Retained Person"))
	client := appStateStorageClient(device)
	for _, raw := range []string{"15550491@s.whatsapp.net", "15550492@s.whatsapp.net"} {
		for _, index := range []string{"mute", "pin_v1", "archive", "contact"} {
			mutation := appstate.Mutation{Operation: waServerSync.SyncdMutation_REMOVE, Index: []string{index, raw}}
			if event := client.DangerousInternals().DispatchAppState(ctx, appstate.WAPatchRegularLow, mutation, true); event != nil {
				t.Fatalf("non-SET mutation emitted an action: index=%s jid=%s event=%#v", index, raw, event)
			}
			assertAppStateChat(t, device, chat, types.LocalChatSettings{Found: true, MutedUntil: store.MutedForever, Pinned: true, Archived: true})
			assertAppStateContact(t, device, chat, types.ContactInfo{Found: true, FirstName: "Retained", FullName: "Retained Person"})
			assertAppStateChat(t, device, missing, types.LocalChatSettings{})
			assertAppStateContact(t, device, missing, types.ContactInfo{})
		}
	}
	requireSQL(t, container.Close())
	container = openStore(t, path)
	device = appStateStorageDevice(t, container, id)
	assertAppStateChat(t, device, chat, types.LocalChatSettings{Found: true, MutedUntil: store.MutedForever, Pinned: true, Archived: true})
	assertAppStateContact(t, device, chat, types.ContactInfo{Found: true, FirstName: "Retained", FullName: "Retained Person"})
	assertAppStateChat(t, device, missing, types.LocalChatSettings{})
	assertAppStateContact(t, device, missing, types.ContactInfo{})
}
