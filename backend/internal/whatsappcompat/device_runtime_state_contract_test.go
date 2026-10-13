package whatsappcandidate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

// These are the concrete stores supplied to the real WhatsApp client. They
// exercise event-consumer state transitions, address isolation and restart
// scope, rather than calling generated protobuf accessors for counters.
func TestActualDeviceContactChatAndPrivacyStateIsAddressScopedAndEphemeral(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	device, native, err := OpenNodeDevice(dir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	person := types.NewJID("111", types.DefaultUserServer)
	otherDevice := types.NewADJID("111", 0, 7)
	otherPerson := types.NewJID("222", types.DefaultUserServer)
	changed, previous, err := device.Contacts.PutPushName(ctx, otherDevice, "original push name")
	if err != nil || !changed || previous != "" {
		t.Fatal("initial push-name event", changed, previous, err)
	}
	changed, previous, err = device.Contacts.PutPushName(ctx, person, "original push name")
	if err != nil || changed || previous != "original push name" {
		t.Fatal("same-person device caused duplicate push-name change", changed, previous, err)
	}
	changed, previous, err = device.Contacts.PutBusinessName(ctx, person, "shop")
	if err != nil || !changed || previous != "" {
		t.Fatal("business event lost push-name distinction", changed, previous, err)
	}
	if err = device.Contacts.PutAllContactNames(ctx, []store.ContactEntry{{JID: otherDevice, FirstName: "First", FullName: "Full Name"}, {JID: otherPerson, FirstName: "Second", FullName: "Other Person"}}); err != nil {
		t.Fatal(err)
	}
	if err = device.Contacts.PutManyRedactedPhones(ctx, []store.RedactedPhoneEntry{{JID: person, RedactedPhone: "***111"}}); err != nil {
		t.Fatal(err)
	}
	contact, err := device.Contacts.GetContact(ctx, otherDevice)
	if err != nil || !contact.Found || contact.PushName != "original push name" || contact.BusinessName != "shop" || contact.FullName != "Full Name" || contact.RedactedPhone != "***111" {
		t.Fatalf("merged event consumer data %+v %v", contact, err)
	}
	contacts, err := device.Contacts.GetAllContacts(ctx)
	if err != nil || len(contacts) != 2 || contacts[person] != contact {
		t.Fatal("contact aggregation crossed address identity", contacts, err)
	}
	until := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	if err = device.ChatSettings.PutMutedUntil(ctx, otherDevice, until); err != nil {
		t.Fatal(err)
	}
	if err = device.ChatSettings.PutPinned(ctx, person, true); err != nil {
		t.Fatal(err)
	}
	if err = device.ChatSettings.PutArchived(ctx, person, true); err != nil {
		t.Fatal(err)
	}
	settings, err := device.ChatSettings.GetChatSettings(ctx, otherDevice)
	if err != nil || !settings.Found || !settings.Pinned || !settings.Archived || !settings.MutedUntil.Equal(until) {
		t.Fatal("chat mutation replaced earlier state", settings, err)
	}
	if err = device.PrivacyTokens.PutPrivacyTokens(ctx, store.PrivacyToken{User: otherDevice, Token: []byte{0, 1, 255}, Timestamp: until}); err != nil {
		t.Fatal(err)
	}
	token, err := device.PrivacyTokens.GetPrivacyToken(ctx, person)
	if err != nil || token == nil || !bytes.Equal(token.Token, []byte{0, 1, 255}) || token.User != otherDevice || !token.Timestamp.Equal(until) {
		t.Fatal("privacy token event lost identity/data", token, err)
	}
	wrong, err := device.PrivacyTokens.GetPrivacyToken(ctx, otherPerson)
	if err != nil || wrong != nil {
		t.Fatal("privacy token crossed user", wrong, err)
	}
	// Cancellation never changes the state a subsequent event consumer sees.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err = device.Contacts.PutPushName(canceled, person, "must-not-replace"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = device.ChatSettings.PutPinned(canceled, person, false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = device.PrivacyTokens.PutPrivacyTokens(canceled, store.PrivacyToken{User: person, Token: []byte("bad")}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	retained, _ := device.Contacts.GetContact(ctx, person)
	retainedSettings, _ := device.ChatSettings.GetChatSettings(ctx, person)
	retainedToken, _ := device.PrivacyTokens.GetPrivacyToken(ctx, person)
	if retained != contact || retainedSettings != settings || !bytes.Equal(retainedToken.Token, token.Token) {
		t.Fatal("canceled consumer mutated current event state")
	}
	if len(native.aux) != 4 {
		t.Fatalf("unexpected cache namespace expansion %v", native.aux)
	}
	cold, _, err := OpenNodeDevice(dir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	coldContact, err := cold.Contacts.GetContact(ctx, person)
	if err != nil || coldContact.Found {
		t.Fatal("ephemeral contacts leaked across a cold client", coldContact, err)
	}
	coldSettings, err := cold.ChatSettings.GetChatSettings(ctx, person)
	if err != nil || coldSettings.Found {
		t.Fatal("ephemeral local chat settings became auth-directory data", coldSettings, err)
	}
	coldToken, err := cold.PrivacyTokens.GetPrivacyToken(ctx, person)
	if err != nil || coldToken != nil {
		t.Fatal("ephemeral privacy token became persisted auth state", coldToken, err)
	}
}

func TestActualDeviceMessageSecretAndDecryptBufferRetainOnlyTheirOwnedState(t *testing.T) {
	ctx := context.Background()
	device, native, err := OpenNodeDevice(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	chat := types.NewJID("123-456", types.GroupServer)
	sender := types.NewJID("111", types.DefaultUserServer)
	wrongSender := types.NewJID("222", types.DefaultUserServer)
	if err = device.MsgSecrets.PutMessageSecrets(ctx, []store.MessageSecretInsert{{Chat: chat, Sender: sender, ID: "message-1", Secret: []byte{0, 3, 255}}}); err != nil {
		t.Fatal(err)
	}
	secret, author, err := device.MsgSecrets.GetMessageSecret(ctx, chat, sender, "message-1")
	if err != nil || !bytes.Equal(secret, []byte{0, 3, 255}) || author != sender {
		t.Fatal("actual secret missing", secret, author, err)
	}
	secret, author, err = device.MsgSecrets.GetMessageSecret(ctx, chat, wrongSender, "message-1")
	if err != nil || len(secret) != 0 || !author.IsEmpty() {
		t.Fatal("secret available to mismatched sender", secret, author, err)
	}
	secret, author, err = device.MsgSecrets.GetMessageSecret(ctx, chat, types.EmptyJID, "message-1")
	if err != nil || len(secret) != 3 || author != sender {
		t.Fatal("sender resolution lost original author", secret, author, err)
	}
	id := sha256.Sum256([]byte("actual encrypted frame identity"))
	server := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err = device.EventBuffer.PutBufferedEvent(ctx, id, []byte("decrypted pending event"), server); err != nil {
		t.Fatal(err)
	}
	buffer, err := device.EventBuffer.GetBufferedEvent(ctx, id)
	if err != nil || buffer == nil || string(buffer.Plaintext) != "decrypted pending event" || !buffer.ServerTime.Equal(server) || buffer.InsertTime.IsZero() {
		t.Fatal("decrypt-buffer receipt incomplete", buffer, err)
	}
	if err = device.EventBuffer.ClearBufferedEventPlaintext(ctx, id); err != nil {
		t.Fatal(err)
	}
	cleared, err := device.EventBuffer.GetBufferedEvent(ctx, id)
	if err != nil || cleared == nil || len(cleared.Plaintext) != 0 || cleared.ServerTime != buffer.ServerTime || cleared.InsertTime != buffer.InsertTime {
		t.Fatal("clear lost replay hash/time identity", cleared, err)
	}
	oldID := sha256.Sum256([]byte("legacy expired hash"))
	native.aux["buffer:"+hex.EncodeToString(oldID[:])] = nodeJSON(store.BufferedEvent{Plaintext: []byte("expired"), InsertTime: time.Now().Add(-15 * 24 * time.Hour)})
	if err = device.EventBuffer.DeleteOldBufferedHashes(ctx); err != nil {
		t.Fatal(err)
	}
	old, err := device.EventBuffer.GetBufferedEvent(ctx, oldID)
	if err != nil || old != nil {
		t.Fatal("expired hash still blocks incoming frame", old, err)
	}
	current, err := device.EventBuffer.GetBufferedEvent(ctx, id)
	if err != nil || current == nil {
		t.Fatal("hash expiry removed current duplicate protection", current, err)
	}
	alt := types.NewJID("111", types.HiddenUserServer)
	if err = device.EventBuffer.AddOutgoingEvent(ctx, alt, "outgoing-1", "protobuf", []byte("original outgoing frame")); err != nil {
		t.Fatal(err)
	}
	format, plain, err := device.EventBuffer.GetOutgoingEvent(ctx, sender, alt, "outgoing-1")
	if err != nil || format != "protobuf" || string(plain) != "original outgoing frame" {
		t.Fatal("PN/LID outgoing retry could not find same message", format, plain, err)
	}
	native.aux["outgoing:"+alt.String()+":expired"] = nodeJSON(map[string]any{"Format": "protobuf", "Plaintext": []byte("old"), "InsertTime": time.Now().Add(-25 * time.Hour)})
	if err = device.EventBuffer.DeleteOldOutgoingEvents(ctx); err != nil {
		t.Fatal(err)
	}
	format, plain, err = device.EventBuffer.GetOutgoingEvent(ctx, alt, types.EmptyJID, "expired")
	if err != nil || format != "" || len(plain) != 0 {
		t.Fatal("expired retransmission remained available", format, plain, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	called := false
	if err = device.EventBuffer.DoDecryptionTxn(canceled, func(context.Context) error { called = true; return nil }); !errors.Is(err, context.Canceled) || called {
		t.Fatal("canceled decryption began transaction", called, err)
	}
	failure := errors.New("physical event-handler failure")
	if err = device.EventBuffer.DoDecryptionTxn(ctx, func(context.Context) error { called = true; return failure }); !errors.Is(err, failure) || !called {
		t.Fatal("decryption consumer failure was swallowed", called, err)
	}
}

func TestActualAllStoresCancellationAndLIDColdIdentityDoNotMutateOtherNamespaces(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	_, native, err := OpenNodeDevice(dir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	person := types.NewJID("111", types.DefaultUserServer)
	lid := types.NewJID("999", types.HiddenUserServer)
	other := types.NewJID("222", types.DefaultUserServer)
	otherLID := types.NewJID("888", types.HiddenUserServer)
	if err = native.PutLIDMapping(ctx, lid, person); err != nil {
		t.Fatal(err)
	}
	if err = native.PutManyLIDMappings(ctx, []store.LIDMapping{{LID: otherLID, PN: other}}); err != nil {
		t.Fatal(err)
	}
	resolvedPN, err := native.GetPNForLID(ctx, lid)
	if err != nil || resolvedPN != person {
		t.Fatal("compound store lost hidden-user phone identity", resolvedPN, err)
	}
	resolvedLID, err := native.GetLIDForPN(ctx, person)
	if err != nil || resolvedLID != lid {
		t.Fatal("compound store lost reverse identity", resolvedLID, err)
	}
	resolved, err := native.GetManyLIDsForPNs(ctx, []types.JID{person, other})
	if err != nil || resolved[person] != lid || resolved[other] != otherLID {
		t.Fatal("batched identity resolution lost a peer", resolved, err)
	}
	_, cold, err := OpenNodeDevice(dir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	resolvedLID, err = cold.GetLIDForPN(ctx, person)
	if err != nil || resolvedLID != lid {
		t.Fatal("compound identity store failed cold old-file restoration", resolvedLID, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	checks := []struct {
		name string
		call func() error
	}{
		{"contact batch", func() error {
			return native.PutAllContactNames(canceled, []store.ContactEntry{{JID: person, FullName: "must-not-write"}})
		}},
		{"redacted phone batch", func() error {
			return native.PutManyRedactedPhones(canceled, []store.RedactedPhoneEntry{{JID: person, RedactedPhone: "must-not-write"}})
		}},
		{"contact read", func() error { _, e := native.GetAllContacts(canceled); return e }},
		{"message-secret batch", func() error {
			return native.PutMessageSecrets(canceled, []store.MessageSecretInsert{{Chat: person, Sender: other, ID: "never", Secret: []byte{1}}})
		}},
		{"buffer clear", func() error { return native.ClearBufferedEventPlaintext(canceled, [32]byte{}) }},
		{"buffer prune", func() error { return native.DeleteOldBufferedHashes(canceled) }},
		{"outgoing prune", func() error { return native.DeleteOldOutgoingEvents(canceled) }},
		{"latest app-state identity", func() error { _, e := native.GetLatestAppStateSyncKeyID(canceled); return e }},
		{"app-state version", func() error { return native.PutAppStateVersion(canceled, "never", 1, [128]byte{}) }},
		{"mutation MACs", func() error { return native.PutAppStateMutationMACs(canceled, "never", 1, nil) }},
		{"delete mutation MACs", func() error { return native.DeleteAppStateMutationMACs(canceled, "never", nil) }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if e := check.call(); !errors.Is(e, context.Canceled) {
				t.Fatalf("canceled store consumer did not propagate cancellation: %v", e)
			}
		})
	}
	if len(native.aux) != 0 {
		t.Fatal("canceled state request wrote an event cache namespace", native.aux)
	}
	if e := native.ClearBufferedEventPlaintext(ctx, [32]byte{}); e != nil {
		t.Fatal("already absent decrypt plaintext is not an error", e)
	}
}
