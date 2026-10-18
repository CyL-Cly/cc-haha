package whatsappcandidate

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// The original protobufjs AppStateSyncKeyData JSON producer writes int64
// timestamps as decimal strings. Native replacements historically used JSON
// numbers; both representations must survive a cold auth-directory migration.
func appStateTimestamp(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	var number int64
	if json.Unmarshal(raw, &number) == nil {
		return number, nil
	}
	var decimal string
	if json.Unmarshal(raw, &decimal) != nil {
		return 0, errors.New("invalid original app-state timestamp")
	}
	number, err := strconv.ParseInt(decimal, 10, 64)
	if err != nil {
		return 0, errors.New("invalid original app-state timestamp")
	}
	return number, nil
}

// App state versions and keys use the original Baileys key categories. Names,
// presence and decrypted-event bookkeeping are runtime caches, matching the
// original adapter's non-history-sync process lifetime.
func (n *NodeDeviceStore) PutAppStateSyncKey(ctx context.Context, id []byte, key store.AppStateSyncKey) error {
	var fp waE2E.AppStateSyncKeyFingerprint
	if e := proto.Unmarshal(key.Fingerprint, &fp); e != nil {
		return e
	}
	// The pinned Node schema uses rawId while the Go protobuf generated field
	// JSON tag is rawID. Write the original auth JSON spelling, or an original
	// peer silently loses the raw-id discriminator after the native update.
	fingerprint := nodeObject{}
	if fp.RawID != nil {
		fingerprint["rawId"] = nodeJSON(*fp.RawID)
	}
	if fp.CurrentIndex != nil {
		fingerprint["currentIndex"] = nodeJSON(*fp.CurrentIndex)
	}
	if len(fp.DeviceIndexes) > 0 {
		fingerprint["deviceIndexes"] = nodeJSON(fp.DeviceIndexes)
	}
	return n.putKey(ctx, "app-state-sync-key", base64.StdEncoding.EncodeToString(id), nodeObject{"keyData": nodeBuffer(key.Data), "fingerprint": nodeJSON(fingerprint), "timestamp": nodeJSON(key.Timestamp)})
}
func (n *NodeDeviceStore) GetAppStateSyncKey(ctx context.Context, id []byte) (*store.AppStateSyncKey, error) {
	raw, e := n.getKey(ctx, "app-state-sync-key", base64.StdEncoding.EncodeToString(id))
	if e != nil || len(raw) == 0 {
		return nil, e
	}
	o := nodeObj(raw)
	var fp waE2E.AppStateSyncKeyFingerprint
	if e = json.Unmarshal(o["fingerprint"], &fp); e != nil && len(o["fingerprint"]) > 0 {
		return nil, e
	}
	b, e := proto.Marshal(&fp)
	if e != nil {
		return nil, e
	}
	timestamp, e := appStateTimestamp(o["timestamp"])
	if e != nil {
		return nil, e
	}
	return &store.AppStateSyncKey{Data: nodeBytes(o["keyData"]), Fingerprint: b, Timestamp: timestamp}, nil
}
func (n *NodeDeviceStore) GetLatestAppStateSyncKeyID(ctx context.Context) ([]byte, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Signal.credsMu.RLock()
	defer n.Signal.credsMu.RUnlock()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	return nodeBytes(n.Signal.Creds["myAppStateKeyId"]), nil
}
func (n *NodeDeviceStore) GetAllAppStateSyncKeys(ctx context.Context) ([]*store.AppStateSyncKey, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids, e := n.keysByCategory("app-state-sync-key")
	if e != nil {
		return nil, e
	}
	out := make([]*store.AppStateSyncKey, 0, len(ids))
	for _, id := range ids {
		raw, e := n.getKey(ctx, "app-state-sync-key", id)
		if e != nil {
			return nil, e
		}
		o := nodeObj(raw)
		var fp waE2E.AppStateSyncKeyFingerprint
		if e = json.Unmarshal(o["fingerprint"], &fp); e != nil {
			return nil, e
		}
		b, e := proto.Marshal(&fp)
		if e != nil {
			return nil, e
		}
		timestamp, e := appStateTimestamp(o["timestamp"])
		if e != nil {
			return nil, e
		}
		out = append(out, &store.AppStateSyncKey{Data: nodeBytes(o["keyData"]), Timestamp: timestamp, Fingerprint: b})
	}
	return out, nil
}
func (n *NodeDeviceStore) PutAppStateVersion(ctx context.Context, name string, version uint64, hash [128]byte) error {
	raw, e := n.getKey(ctx, "app-state-sync-version", name)
	if e != nil {
		return e
	}
	o := nodeObj(raw)
	o["version"] = nodeJSON(version)
	o["hash"] = nodeBuffer(hash[:])
	if len(o["indexValueMap"]) == 0 {
		o["indexValueMap"] = nodeJSON(nodeObject{})
	}
	return n.putKey(ctx, "app-state-sync-version", name, o)
}
func (n *NodeDeviceStore) GetAppStateVersion(ctx context.Context, name string) (uint64, [128]byte, error) {
	raw, e := n.getKey(ctx, "app-state-sync-version", name)
	o := nodeObj(raw)
	var hash [128]byte
	b := nodeBytes(o["hash"])
	if len(raw) > 0 && len(b) != 128 {
		return 0, hash, errors.New("invalid original app state hash")
	}
	copy(hash[:], b)
	return uint64(nodeNumber(o["version"])), hash, e
}
func (n *NodeDeviceStore) DeleteAppStateVersion(ctx context.Context, name string) error {
	return n.putKey(ctx, "app-state-sync-version", name, nil)
}
func (n *NodeDeviceStore) PutAppStateMutationMACs(ctx context.Context, name string, version uint64, values []store.AppStateMutationMAC) error {
	raw, e := n.getKey(ctx, "app-state-sync-version", name)
	if e != nil {
		return e
	}
	o := nodeObj(raw)
	m := nodeObj(o["indexValueMap"])
	for _, v := range values {
		m[base64.StdEncoding.EncodeToString(v.IndexMAC)] = nodeJSON(nodeObject{"valueMac": nodeBuffer(v.ValueMAC)})
	}
	o["indexValueMap"] = nodeJSON(m)
	return n.putKey(ctx, "app-state-sync-version", name, o)
}
func (n *NodeDeviceStore) DeleteAppStateMutationMACs(ctx context.Context, name string, ids [][]byte) error {
	raw, e := n.getKey(ctx, "app-state-sync-version", name)
	if e != nil {
		return e
	}
	o := nodeObj(raw)
	m := nodeObj(o["indexValueMap"])
	for _, id := range ids {
		delete(m, base64.StdEncoding.EncodeToString(id))
	}
	o["indexValueMap"] = nodeJSON(m)
	return n.putKey(ctx, "app-state-sync-version", name, o)
}
func (n *NodeDeviceStore) GetAppStateMutationMAC(ctx context.Context, name string, id []byte) ([]byte, error) {
	raw, e := n.getKey(ctx, "app-state-sync-version", name)
	o := nodeObj(nodeObj(nodeObj(raw)["indexValueMap"])[base64.StdEncoding.EncodeToString(id)])
	return nodeBytes(o["valueMac"]), e
}

func (n *NodeDeviceStore) cachedGet(ctx context.Context, key string, value any) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	v, ok := n.aux[key]
	if !ok {
		return nil
	}
	return json.Unmarshal(v, value)
}
func (n *NodeDeviceStore) cachedSet(ctx context.Context, key string, value any) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	if value == nil {
		delete(n.aux, key)
	} else {
		n.aux[key] = nodeJSON(value)
	}
	return nil
}
func (n *NodeDeviceStore) contactName(ctx context.Context, jid types.JID, value string, business bool) (bool, string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return false, "", e
	}
	key := "contact:" + jid.ToNonAD().String()
	var v types.ContactInfo
	_ = json.Unmarshal(n.aux[key], &v)
	old := v.PushName
	if business {
		old = v.BusinessName
		v.BusinessName = value
	} else {
		v.PushName = value
	}
	v.Found = true
	n.aux[key] = nodeJSON(v)
	return old != value, old, nil
}
func (n *NodeDeviceStore) PutPushName(ctx context.Context, jid types.JID, value string) (bool, string, error) {
	return n.contactName(ctx, jid, value, false)
}
func (n *NodeDeviceStore) PutBusinessName(ctx context.Context, jid types.JID, value string) (bool, string, error) {
	return n.contactName(ctx, jid, value, true)
}
func (n *NodeDeviceStore) PutContactName(ctx context.Context, jid types.JID, firstName, fullName string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	key := "contact:" + jid.ToNonAD().String()
	var v types.ContactInfo
	_ = json.Unmarshal(n.aux[key], &v)
	v.Found = true
	v.FullName = fullName
	v.FirstName = firstName
	n.aux[key] = nodeJSON(v)
	return nil
}
func (n *NodeDeviceStore) PutAllContactNames(ctx context.Context, contacts []store.ContactEntry) error {
	for _, v := range contacts {
		if e := n.PutContactName(ctx, v.JID, v.FirstName, v.FullName); e != nil {
			return e
		}
	}
	return nil
}
func (n *NodeDeviceStore) PutManyRedactedPhones(ctx context.Context, values []store.RedactedPhoneEntry) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	for _, v := range values {
		key := "contact:" + v.JID.ToNonAD().String()
		var c types.ContactInfo
		_ = json.Unmarshal(n.aux[key], &c)
		c.Found = true
		c.RedactedPhone = v.RedactedPhone
		n.aux[key] = nodeJSON(c)
	}
	return nil
}
func (n *NodeDeviceStore) GetContact(ctx context.Context, jid types.JID) (types.ContactInfo, error) {
	var v types.ContactInfo
	e := n.cachedGet(ctx, "contact:"+jid.ToNonAD().String(), &v)
	return v, e
}
func (n *NodeDeviceStore) GetAllContacts(ctx context.Context) (map[types.JID]types.ContactInfo, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	out := map[types.JID]types.ContactInfo{}
	for key, raw := range n.aux {
		if len(key) > 8 && key[:8] == "contact:" {
			jid, e := types.ParseJID(key[8:])
			if e != nil {
				return nil, e
			}
			var c types.ContactInfo
			if e = json.Unmarshal(raw, &c); e != nil {
				return nil, e
			}
			out[jid] = c
		}
	}
	return out, nil
}
func (n *NodeDeviceStore) chatSetting(ctx context.Context, jid types.JID, apply func(*types.LocalChatSettings)) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	key := "chat:" + jid.ToNonAD().String()
	var v types.LocalChatSettings
	_ = json.Unmarshal(n.aux[key], &v)
	v.Found = true
	apply(&v)
	n.aux[key] = nodeJSON(v)
	return nil
}
func (n *NodeDeviceStore) PutMutedUntil(ctx context.Context, jid types.JID, until time.Time) error {
	return n.chatSetting(ctx, jid, func(v *types.LocalChatSettings) { v.MutedUntil = until })
}
func (n *NodeDeviceStore) PutPinned(ctx context.Context, jid types.JID, pinned bool) error {
	return n.chatSetting(ctx, jid, func(v *types.LocalChatSettings) { v.Pinned = pinned })
}
func (n *NodeDeviceStore) PutArchived(ctx context.Context, jid types.JID, archived bool) error {
	return n.chatSetting(ctx, jid, func(v *types.LocalChatSettings) { v.Archived = archived })
}
func (n *NodeDeviceStore) GetChatSettings(ctx context.Context, jid types.JID) (types.LocalChatSettings, error) {
	var v types.LocalChatSettings
	e := n.cachedGet(ctx, "chat:"+jid.ToNonAD().String(), &v)
	return v, e
}
func (n *NodeDeviceStore) PutMessageSecrets(ctx context.Context, values []store.MessageSecretInsert) error {
	for _, v := range values {
		if e := n.PutMessageSecret(ctx, v.Chat, v.Sender, v.ID, v.Secret); e != nil {
			return e
		}
	}
	return nil
}
func (n *NodeDeviceStore) PutMessageSecret(ctx context.Context, chat, sender types.JID, id types.MessageID, secret []byte) error {
	return n.cachedSet(ctx, "secret:"+chat.String()+":"+id, map[string]any{"secret": secret, "sender": sender})
}
func (n *NodeDeviceStore) GetMessageSecret(ctx context.Context, chat, sender types.JID, id types.MessageID) ([]byte, types.JID, error) {
	var v struct {
		Secret []byte
		Sender types.JID
	}
	e := n.cachedGet(ctx, "secret:"+chat.String()+":"+id, &v)
	if v.Sender != sender && !sender.IsEmpty() {
		return nil, types.EmptyJID, e
	}
	return v.Secret, v.Sender, e
}
func (n *NodeDeviceStore) PutPrivacyTokens(ctx context.Context, values ...store.PrivacyToken) error {
	for _, v := range values {
		if e := n.cachedSet(ctx, "privacy:"+v.User.ToNonAD().String(), v); e != nil {
			return e
		}
	}
	return nil
}
func (n *NodeDeviceStore) GetPrivacyToken(ctx context.Context, jid types.JID) (*store.PrivacyToken, error) {
	var v *store.PrivacyToken
	e := n.cachedGet(ctx, "privacy:"+jid.ToNonAD().String(), &v)
	return v, e
}
func (n *NodeDeviceStore) GetBufferedEvent(ctx context.Context, id [32]byte) (*store.BufferedEvent, error) {
	var v *store.BufferedEvent
	e := n.cachedGet(ctx, "buffer:"+hex.EncodeToString(id[:]), &v)
	return v, e
}
func (n *NodeDeviceStore) PutBufferedEvent(ctx context.Context, id [32]byte, plaintext []byte, server time.Time) error {
	return n.cachedSet(ctx, "buffer:"+hex.EncodeToString(id[:]), store.BufferedEvent{Plaintext: plaintext, InsertTime: time.Now(), ServerTime: server})
}
func (n *NodeDeviceStore) DoDecryptionTxn(ctx context.Context, fn func(context.Context) error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	return fn(ctx)
}
func (n *NodeDeviceStore) ClearBufferedEventPlaintext(ctx context.Context, id [32]byte) error {
	v, e := n.GetBufferedEvent(ctx, id)
	if e != nil || v == nil {
		return e
	}
	v.Plaintext = nil
	return n.cachedSet(ctx, "buffer:"+hex.EncodeToString(id[:]), v)
}
func (n *NodeDeviceStore) DeleteOldBufferedHashes(ctx context.Context) error {
	return n.pruneCache(ctx, "buffer:", 14*24*time.Hour, "InsertTime")
}
func (n *NodeDeviceStore) GetOutgoingEvent(ctx context.Context, chat, alt types.JID, id types.MessageID) (string, []byte, error) {
	var v struct {
		Format    string
		Plaintext []byte
	}
	e := n.cachedGet(ctx, "outgoing:"+chat.String()+":"+id, &v)
	if e == nil && v.Format == "" && !alt.IsEmpty() {
		e = n.cachedGet(ctx, "outgoing:"+alt.String()+":"+id, &v)
	}
	return v.Format, v.Plaintext, e
}
func (n *NodeDeviceStore) AddOutgoingEvent(ctx context.Context, chat types.JID, id types.MessageID, format string, plain []byte) error {
	return n.cachedSet(ctx, "outgoing:"+chat.String()+":"+id, map[string]any{"Format": format, "Plaintext": plain, "InsertTime": time.Now()})
}
func (n *NodeDeviceStore) DeleteOldOutgoingEvents(ctx context.Context) error {
	return n.pruneCache(ctx, "outgoing:", 24*time.Hour, "InsertTime")
}
func (n *NodeDeviceStore) pruneCache(ctx context.Context, prefix string, age time.Duration, field string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	for key, raw := range n.aux {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(raw, &fields)
			var t time.Time
			_ = json.Unmarshal(fields[field], &t)
			if time.Since(t) > age {
				delete(n.aux, key)
			}
		}
	}
	return nil
}

var _ store.AllStores = (*NodeDeviceStore)(nil) // LID methods provided below.
func (n *NodeDeviceStore) PutManyLIDMappings(ctx context.Context, v []store.LIDMapping) error {
	return nodeDeviceLIDs{n.LID}.PutManyLIDMappings(ctx, v)
}
func (n *NodeDeviceStore) PutLIDMapping(ctx context.Context, lid, pn types.JID) error {
	return nodeDeviceLIDs{n.LID}.PutLIDMapping(ctx, lid, pn)
}
func (n *NodeDeviceStore) GetPNForLID(ctx context.Context, lid types.JID) (types.JID, error) {
	return nodeDeviceLIDs{n.LID}.GetPNForLID(ctx, lid)
}
func (n *NodeDeviceStore) GetLIDForPN(ctx context.Context, pn types.JID) (types.JID, error) {
	return nodeDeviceLIDs{n.LID}.GetLIDForPN(ctx, pn)
}
func (n *NodeDeviceStore) GetManyLIDsForPNs(ctx context.Context, pns []types.JID) (map[types.JID]types.JID, error) {
	return nodeDeviceLIDs{n.LID}.GetManyLIDsForPNs(ctx, pns)
}
