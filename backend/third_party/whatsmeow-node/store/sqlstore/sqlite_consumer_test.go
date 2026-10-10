package sqlstore_test

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	_ "modernc.org/sqlite"
)

//go:embed testdata/sqlite-v8.sql
var sqliteV8 string

func requireSQL(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func sqliteAddress(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String() + "?_pragma=foreign_keys(1)"
}
func openStore(t *testing.T, path string) *sqlstore.Container {
	t.Helper()
	c, err := sqlstore.New(context.Background(), "sqlite", sqliteAddress(path), nil)
	requireSQL(t, err)
	t.Cleanup(func() { requireSQL(t, c.Close()) })
	return c
}
func savedDevice(t *testing.T, c *sqlstore.Container, user string) *store.Device {
	t.Helper()
	d := c.NewDevice()
	jid := types.NewADJID(user, 0, 1)
	d.ID = &jid
	d.LID = types.NewJID("900"+user, types.HiddenUserServer)
	d.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte("owned device details"), AccountSignature: bytes.Repeat([]byte{4}, 64), AccountSignatureKey: bytes.Repeat([]byte{5}, 32), DeviceSignature: bytes.Repeat([]byte{6}, 64)}
	d.Platform = "owned-sqlite"
	d.PushName = "before restart"
	requireSQL(t, d.Save(context.Background()))
	if !d.Initialized || d.Sessions == nil || d.Identities == nil || d.LIDs == nil {
		t.Fatal("saved device did not bind its actual stores")
	}
	return d
}
func assertSession(t *testing.T, d *store.Device, address string, want []byte) {
	t.Helper()
	got, err := d.Sessions.GetSession(context.Background(), address)
	requireSQL(t, err)
	if !bytes.Equal(got, want) {
		t.Fatalf("session %s: %q != %q", address, got, want)
	}
	has, err := d.Sessions.HasSession(context.Background(), address)
	requireSQL(t, err)
	if has != (want != nil) {
		t.Fatalf("session presence %s: %v", address, has)
	}
}
func assertTrust(t *testing.T, d *store.Device, address string, key [32]byte, want bool) {
	t.Helper()
	got, err := d.Identities.IsTrustedIdentity(context.Background(), address, key)
	requireSQL(t, err)
	if got != want {
		t.Fatalf("identity trust %s: %v != %v", address, got, want)
	}
}
func assertMapping(t *testing.T, lids store.LIDStore, pn, lid types.JID) {
	t.Helper()
	got, err := lids.GetLIDForPN(context.Background(), pn)
	requireSQL(t, err)
	if got != lid {
		t.Fatalf("PN mapping %s: %s != %s", pn, got, lid)
	}
	if !lid.IsEmpty() {
		back, err := lids.GetPNForLID(context.Background(), lid)
		requireSQL(t, err)
		if back != pn {
			t.Fatalf("reverse mapping %s: %s != %s", lid, back, pn)
		}
	}
}

func TestSQLiteForeignKeysAndLegacyUpgradePreserveStoredConsumers(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	requireSQL(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { requireSQL(t, db.Close()) })
	c := sqlstore.NewWithDB(db, "sqlite", nil)
	if err = c.Upgrade(ctx); err == nil || !strings.Contains(err.Error(), "foreign keys are not enabled") {
		t.Fatal("unsafe SQLite schema accepted", err)
	}
	var tables int
	requireSQL(t, db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='whatsmeow_version'").Scan(&tables))
	if tables != 0 {
		t.Fatal("foreign-key refusal wrote schema")
	}
	_, err = db.Exec("PRAGMA foreign_keys=ON")
	requireSQL(t, err)
	_, err = db.Exec(sqliteV8)
	requireSQL(t, err)
	// Seed a pre-upgrade device and its durable child rows using the old schema.
	jid := types.NewADJID("15550001", 0, 1)
	lid := types.NewJID("90015550001", types.HiddenUserServer)
	noise, identity, prekey := bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32), bytes.Repeat([]byte{3}, 32)
	_, err = db.Exec(`INSERT INTO whatsmeow_device (jid,lid,registration_id,noise_key,identity_key,signed_pre_key,signed_pre_key_id,signed_pre_key_sig,adv_key,adv_details,adv_account_sig,adv_account_sig_key,adv_device_sig,platform,push_name) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, jid, lid, 77, noise, identity, prekey, 1, bytes.Repeat([]byte{4}, 64), []byte("adv"), []byte("legacy details"), bytes.Repeat([]byte{5}, 64), bytes.Repeat([]byte{6}, 32), bytes.Repeat([]byte{7}, 64), "legacy", "legacy name")
	requireSQL(t, err)
	_, err = db.Exec(`INSERT INTO whatsmeow_sessions VALUES (?,?,?)`, jid, "peer:1", []byte("legacy encrypted session"))
	requireSQL(t, err)
	_, err = db.Exec(`INSERT INTO whatsmeow_contacts (our_jid,their_jid,full_name) VALUES (?,?,?)`, jid, "peer@s.whatsapp.net", "legacy contact")
	requireSQL(t, err)
	_, err = db.Exec(`INSERT INTO whatsmeow_privacy_tokens VALUES (?,?,?,?)`, jid, "peer@s.whatsapp.net", []byte("legacy token"), 123)
	requireSQL(t, err)
	var legacyName string
	requireSQL(t, db.QueryRow(`SELECT full_name FROM whatsmeow_contacts WHERE our_jid=? AND their_jid=?`, jid, "peer@s.whatsapp.net").Scan(&legacyName))
	if legacyName != "legacy contact" {
		t.Fatal("invalid pre-upgrade contact fixture", legacyName)
	}
	requireSQL(t, c.Upgrade(ctx))
	var version, compat int
	requireSQL(t, db.QueryRow("SELECT version,compat FROM whatsmeow_version").Scan(&version, &compat))
	if version != 13 || compat != 8 {
		t.Fatal("legacy schema not advanced compatibly", version, compat)
	}
	d, err := c.GetDevice(ctx, jid)
	requireSQL(t, err)
	if d == nil || d.LID != lid || d.RegistrationID != 77 || d.LIDMigrationTimestamp != 0 || d.PushName != "legacy name" || !bytes.Equal(d.NoiseKey.Priv[:], noise) || !bytes.Equal(d.IdentityKey.Priv[:], identity) || !bytes.Equal(d.SignedPreKey.Priv[:], prekey) {
		t.Fatal("upgrade changed legacy device identity", d)
	}
	assertSession(t, d, "peer:1", []byte("legacy encrypted session"))
	contact, err := d.Contacts.GetContact(ctx, types.NewJID("peer", types.DefaultUserServer))
	requireSQL(t, err)
	if contact.FullName != "legacy contact" {
		t.Fatal("upgrade lost contact", contact)
	}
	token, err := d.PrivacyTokens.GetPrivacyToken(ctx, types.NewJID("peer", types.DefaultUserServer))
	requireSQL(t, err)
	if token == nil || !bytes.Equal(token.Token, []byte("legacy token")) {
		t.Fatal("upgrade lost privacy token", token)
	}
	// New v9-v13 consumers must work without replacing the old rows.
	hash := [32]byte{1}
	requireSQL(t, d.EventBuffer.PutBufferedEvent(ctx, hash, []byte("decrypted event"), time.Unix(1730000000, 0)))
	event, err := d.EventBuffer.GetBufferedEvent(ctx, hash)
	requireSQL(t, err)
	if event == nil || !bytes.Equal(event.Plaintext, []byte("decrypted event")) {
		t.Fatal("upgraded event buffer unavailable", event)
	}
	chat := types.NewJID("group", types.GroupServer)
	requireSQL(t, d.EventBuffer.AddOutgoingEvent(ctx, chat, "owned-retry-id", "protobuf", []byte("outgoing event")))
	format, outgoing, err := d.EventBuffer.GetOutgoingEvent(ctx, chat, types.EmptyJID, "owned-retry-id")
	requireSQL(t, err)
	if format != "protobuf" || !bytes.Equal(outgoing, []byte("outgoing event")) {
		t.Fatal("upgraded retry receipt buffer unavailable", format, outgoing)
	}
	d.LIDMigrationTimestamp = 12345
	d.PushName = "after upgrade"
	requireSQL(t, d.Save(ctx))
	requireSQL(t, c.Upgrade(ctx)) // Reentry is a no-op and keeps migrated data.
	requireSQL(t, c.Close())
	reopened := openStore(t, path)
	d, err = reopened.GetDevice(ctx, jid)
	requireSQL(t, err)
	if d == nil || d.PushName != "after upgrade" || d.LIDMigrationTimestamp != 12345 || !bytes.Equal(d.IdentityKey.Priv[:], identity) {
		t.Fatal("upgraded device not durable", d)
	}
	assertSession(t, d, "peer:1", []byte("legacy encrypted session"))
	format, outgoing, err = d.EventBuffer.GetOutgoingEvent(ctx, chat, types.EmptyJID, "owned-retry-id")
	requireSQL(t, err)
	if format != "protobuf" || !bytes.Equal(outgoing, []byte("outgoing event")) {
		t.Fatal("upgraded retry receipt buffer lost on restart", format, outgoing)
	}
}

func TestSQLiteDeviceSessionIdentityMigrationDeletionAndRestoreAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.db")
	c := openStore(t, path)
	empty, err := c.GetFirstDevice(ctx)
	requireSQL(t, err)
	if empty.ID != nil || empty.Initialized {
		t.Fatal("empty container persisted an unpaired device")
	}
	if err = empty.Save(ctx); !errors.Is(err, sqlstore.ErrDeviceIDMustBeSet) {
		t.Fatal("unpaired save", err)
	}
	if err = empty.Delete(ctx); !errors.Is(err, sqlstore.ErrDeviceIDMustBeSet) {
		t.Fatal("unpaired delete", err)
	}
	a, b := savedDevice(t, c, "15550002"), savedDevice(t, c, "15550003")
	aid, bid := *a.ID, *b.ID
	noise, identity, signature := *a.NoiseKey.Priv, *a.IdentityKey.Priv, *a.SignedPreKey.Signature
	pn := types.NewJID("15551111", types.DefaultUserServer)
	lid := types.NewJID("9001111", types.HiddenUserServer)
	p1, p2, l1, l2 := pn.SignalAddressUser()+":1", pn.SignalAddressUser()+":2", lid.SignalAddressUser()+":1", lid.SignalAddressUser()+":2"
	key, wrong := [32]byte{1}, [32]byte{2}
	assertTrust(t, a, p1, wrong, true)
	requireSQL(t, a.Identities.PutIdentity(ctx, p1, key))
	requireSQL(t, a.Identities.PutIdentity(ctx, p2, key))
	requireSQL(t, a.Identities.PutIdentity(ctx, "unrelated:1", key))
	assertTrust(t, a, p1, key, true)
	assertTrust(t, a, p1, wrong, false)
	requireSQL(t, a.Sessions.PutManySessions(ctx, map[string][]byte{p1: []byte("first"), p2: []byte("second"), "unrelated:1": []byte("unrelated")}))
	requireSQL(t, b.Sessions.PutSession(ctx, p1, []byte("other device")))
	requireSQL(t, b.Identities.PutIdentity(ctx, p1, key))
	requireSQL(t, a.SenderKeys.PutSenderKey(ctx, "group", p1, []byte("sender ratchet")))
	many, err := a.Sessions.GetManySessions(ctx, []string{p1, p2, "missing:1"})
	requireSQL(t, err)
	if !reflect.DeepEqual(many, map[string][]byte{p1: []byte("first"), p2: []byte("second"), "missing:1": nil}) {
		t.Fatal("multi-device session batch", many)
	}
	requireSQL(t, a.Sessions.MigratePNToLID(ctx, pn, lid))
	requireSQL(t, a.Sessions.MigratePNToLID(ctx, pn, lid))
	assertSession(t, a, p1, nil)
	assertSession(t, a, p2, nil)
	assertSession(t, a, l1, []byte("first"))
	assertSession(t, a, l2, []byte("second"))
	assertTrust(t, a, p1, wrong, true)
	assertTrust(t, a, l1, wrong, false)
	old, err := a.SenderKeys.GetSenderKey(ctx, "group", p1)
	requireSQL(t, err)
	moved, err := a.SenderKeys.GetSenderKey(ctx, "group", l1)
	requireSQL(t, err)
	if old != nil || !bytes.Equal(moved, []byte("sender ratchet")) {
		t.Fatal("sender key migration", old, moved)
	}
	assertSession(t, a, "unrelated:1", []byte("unrelated"))
	assertSession(t, b, p1, []byte("other device"))
	a.PushName = "profile update"
	requireSQL(t, a.Save(ctx))
	all, err := c.GetAllDevices(ctx)
	requireSQL(t, err)
	if len(all) != 2 {
		t.Fatal("paired devices list", len(all))
	}
	requireSQL(t, c.Close())
	c = openStore(t, path)
	a, err = c.GetDevice(ctx, aid)
	requireSQL(t, err)
	b, err = c.GetDevice(ctx, bid)
	requireSQL(t, err)
	if a == nil || b == nil || a.PushName != "profile update" || *a.NoiseKey.Priv != noise || *a.IdentityKey.Priv != identity || *a.SignedPreKey.Signature != signature {
		t.Fatal("restart changed paired identity")
	}
	assertSession(t, a, l1, []byte("first"))
	assertTrust(t, a, l1, wrong, false)
	requireSQL(t, a.Sessions.DeleteSession(ctx, l2))
	requireSQL(t, a.Identities.DeleteIdentity(ctx, l2))
	assertSession(t, a, l2, nil)
	assertSession(t, a, l1, []byte("first"))
	assertTrust(t, a, l2, wrong, true)
	assertTrust(t, a, l1, wrong, false)
	requireSQL(t, a.Sessions.DeleteAllSessions(ctx, lid.SignalAddressUser()))
	requireSQL(t, a.Identities.DeleteAllIdentities(ctx, lid.SignalAddressUser()))
	assertSession(t, a, l1, nil)
	assertTrust(t, a, l1, wrong, true)
	assertSession(t, a, "unrelated:1", []byte("unrelated"))
	assertTrust(t, a, "unrelated:1", wrong, false)
	// A paired device deletion cascades remaining rows without touching siblings.
	requireSQL(t, a.Delete(ctx))
	if a.ID != nil || !a.LID.IsEmpty() {
		t.Fatal("deleted device retained its pairing identity")
	}
	gone, err := c.GetDevice(ctx, aid)
	requireSQL(t, err)
	if gone != nil {
		t.Fatal("deleted device loaded")
	}
	assertSession(t, a, "unrelated:1", nil)
	assertTrust(t, a, "unrelated:1", wrong, true)
	assertSession(t, b, p1, []byte("other device"))
	assertTrust(t, b, p1, wrong, false)
	restored := savedDevice(t, c, aid.User)
	assertSession(t, restored, "unrelated:1", nil)
	assertTrust(t, restored, "unrelated:1", wrong, true)
	requireSQL(t, c.Close())
	c = openStore(t, path)
	restored, err = c.GetDevice(ctx, aid)
	requireSQL(t, err)
	if restored == nil {
		t.Fatal("repaired pairing not durable")
	}
	assertSession(t, restored, "unrelated:1", nil)
	sibling, err := c.GetDevice(ctx, bid)
	requireSQL(t, err)
	assertSession(t, sibling, p1, []byte("other device"))
}

func TestSQLiteLIDMappingColdBatchWarmConcurrentAndDurableRebind(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lids.db")
	c := openStore(t, path)
	d := savedDevice(t, c, "15550004")
	pn, lid := types.NewJID("15552222", types.DefaultUserServer), types.NewJID("9002222", types.HiddenUserServer)
	pn2, lid2 := types.NewJID("15553333", types.DefaultUserServer), types.NewJID("9003333", types.HiddenUserServer)
	assertMapping(t, d.LIDs, pn, types.EmptyJID) // Negative cache must be replaced by a later real mapping.
	requireSQL(t, d.LIDs.PutLIDMapping(ctx, lid, pn))
	requireSQL(t, d.LIDs.PutLIDMapping(ctx, lid, pn))
	assertMapping(t, d.LIDs, pn, lid)
	invalid := types.NewJID("group", types.GroupServer)
	if err := d.LIDs.PutLIDMapping(ctx, invalid, pn); err == nil {
		t.Fatal("invalid single mapping accepted")
	}
	if _, err := d.LIDs.GetLIDForPN(ctx, invalid); err == nil {
		t.Fatal("non-PN lookup accepted")
	}
	if _, err := d.LIDs.GetPNForLID(ctx, pn); err == nil {
		t.Fatal("non-LID lookup accepted")
	}
	requireSQL(t, d.LIDs.PutManyLIDMappings(ctx, []store.LIDMapping{{LID: lid, PN: pn}, {LID: lid2, PN: pn2}, {LID: lid2, PN: pn2}, {LID: invalid, PN: pn}}))
	requireSQL(t, c.Close())
	c = openStore(t, path)
	// A fresh container reads the real SQL batch before its cache is filled.
	many, err := c.LIDMap.GetManyLIDsForPNs(ctx, []types.JID{pn, pn2, types.NewJID("absent", types.DefaultUserServer), invalid})
	requireSQL(t, err)
	if !reflect.DeepEqual(many, map[types.JID]types.JID{pn: lid, pn2: lid2}) {
		t.Fatal("cold LID SQL batch", many)
	}
	pnDevice, lidDevice := pn, lid
	pnDevice.Device = 3
	lidDevice.Device = 3
	assertMapping(t, c.LIDMap, pnDevice, lidDevice)
	requireSQL(t, c.LIDMap.FillCache(ctx))
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 20; n++ {
				if err := c.LIDMap.PutLIDMapping(ctx, lid, pn); err != nil {
					t.Error(err)
					return
				}
				got, err := c.LIDMap.GetPNForLID(ctx, lidDevice)
				if err != nil || got != pnDevice {
					t.Error("concurrent cached mapping", got, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	replacement := types.NewJID("9004444", types.HiddenUserServer)
	requireSQL(t, c.LIDMap.PutLIDMapping(ctx, replacement, pn))
	assertMapping(t, c.LIDMap, pn, replacement)
	requireSQL(t, c.Close())
	c = openStore(t, path)
	assertMapping(t, c.LIDMap, pn, replacement)
	removed, err := c.LIDMap.GetPNForLID(ctx, lid)
	requireSQL(t, err)
	if !removed.IsEmpty() {
		t.Fatal("durable rebind retained old LID database row", removed)
	}
	requireSQL(t, c.LIDMap.FillCache(ctx))
	assertMapping(t, c.LIDMap, pn2, lid2)
	assertMapping(t, c.LIDMap, types.NewJID("absent", types.DefaultUserServer), types.EmptyJID)
}
