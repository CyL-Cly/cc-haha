package whatsappcandidate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sig "go.mau.fi/libsignal/protocol"
	"go.mau.fi/libsignal/state/record"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/util/keys"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// NodeDeviceStore binds the pinned client's concrete Device to the existing
// Baileys authentication directory. Signal records remain Node records on disk;
// native session serialization is only a boundary representation.
type NodeDeviceStore struct {
	Signal      *NodeSignalStore
	LID         *NodeLIDStore
	mu          sync.Mutex
	cache       *NodeSignalKeyCache
	aux         nodeObject
	WriteKeys   func(context.Context, map[string]map[string]any) error
	CommitSleep func(context.Context, time.Duration) error
	Warn        func(error)
}

func OpenNodeDevice(dir string, now func() time.Time) (*store.Device, *NodeDeviceStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, nil, err
	}
	path := filepath.Join(dir, "creds.json")
	raw, err := os.ReadFile(path)
	if err != nil || !json.Valid(raw) {
		if backup, e := os.ReadFile(path + ".bak"); e == nil && json.Valid(backup) {
			if e = os.WriteFile(path, backup, 0600); e != nil {
				return nil, nil, e
			}
			_ = os.Chmod(path, 0600)
			raw = backup
		} else {
			creds, e := CreateNodeCredentials()
			if e != nil {
				return nil, nil, e
			}
			raw = nodeJSON(creds)
		}
	}
	signal := &NodeSignalStore{Dir: dir, Creds: nodeObj(raw), Sessions: map[string]json.RawMessage{}, Now: now}
	n := &NodeDeviceStore{Signal: signal, LID: &NodeLIDStore{Dir: dir, Now: now}, aux: nodeObject{}}
	n.cache = &NodeSignalKeyCache{Now: now, Read: n.readKeys, Write: n.writeKeys}
	signal.readRecord = func(address string) (json.RawMessage, error) {
		return n.getKey(context.Background(), "session", address)
	}
	signal.writeRecord = func(address string, value json.RawMessage) error {
		return n.commitSessionMutations(context.Background(), map[string]any{address: value})
	}
	device, e := DeviceFromNodeCredentials(signal.Creds)
	if e != nil {
		return nil, nil, e
	}
	device.Log = waLog.Noop
	device.Identities = n
	device.Sessions = n
	device.PreKeys = n
	device.SenderKeys = n
	device.AppStateKeys = n
	device.AppState = n
	device.Contacts = n
	device.ChatSettings = n
	device.MsgSecrets = n
	device.PrivacyTokens = n
	device.EventBuffer = n
	device.LIDs = nodeDeviceLIDs{n.LID}
	device.Container = n
	device.Initialized = true
	return device, n, nil
}

func (n *NodeDeviceStore) readKeys(ctx context.Context, category string, ids []string) (map[string]any, error) {
	out := map[string]any{}
	for _, id := range ids {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		path := n.keyPath(category, id)
		wait, release := reserveNodeFile(path)
		wait()
		b, e := os.ReadFile(path)
		release()
		if e == nil && json.Valid(b) {
			out[id] = json.RawMessage(b)
		}
	}
	return out, nil
}
func (n *NodeDeviceStore) writeKeys(ctx context.Context, values map[string]map[string]any) error {
	if n.WriteKeys != nil {
		return n.WriteKeys(ctx, values)
	}
	count := 0
	for _, entries := range values {
		count += len(entries)
	}
	results := make(chan error, count)
	for category, entries := range values {
		for id, v := range entries {
			if e := ctx.Err(); e != nil {
				return e
			}
			path := n.keyPath(category, id)
			wait, release := reserveNodeFile(path)
			go func(value any) {
				wait()
				defer release()
				var err error
				if value == nil {
					err = os.Remove(path)
					if os.IsNotExist(err) {
						err = nil
					}
				} else {
					err = os.WriteFile(path, nodeJSON(value), 0666)
				}
				results <- err
			}(v)
		}
	}
	for i := 0; i < count; i++ {
		if err := <-results; err != nil {
			return err
		}
	}
	return nil
}
func (n *NodeDeviceStore) keyPath(category, id string) string {
	return filepath.Join(n.Signal.Dir, strings.NewReplacer("/", "__", ":", "-").Replace(category+"-"+id+".json"))
}
func (n *NodeDeviceStore) getKey(ctx context.Context, category, id string) (json.RawMessage, error) {
	v, e := n.cache.Get(ctx, category, []string{id})
	if e != nil {
		return nil, e
	}
	if v[id] == nil {
		return nil, nil
	}
	return nodeJSON(v[id]), nil
}
func (n *NodeDeviceStore) putKey(ctx context.Context, category, id string, value any) error {
	return n.cache.Set(ctx, map[string]map[string]any{category: {id: value}})
}
func deviceSignalAddress(address string) (*sig.SignalAddress, error) {
	i := strings.LastIndexByte(address, ':')
	if i < 0 {
		return nil, fmt.Errorf("invalid native Signal address %q", address)
	}
	id, e := strconv.ParseUint(address[i+1:], 10, 32)
	if e != nil {
		return nil, e
	}
	return sig.NewSignalAddress(address[:i], uint32(id)), nil
}

func (n *NodeDeviceStore) GetSession(ctx context.Context, address string) ([]byte, error) {
	a, e := deviceSignalAddress(address)
	if e != nil {
		return nil, e
	}
	found, e := n.Signal.ContainsSession(ctx, a)
	if e != nil || !found {
		return nil, e
	}
	r, e := n.Signal.LoadSession(ctx, a)
	if e != nil && e.Error() == "No open session" {
		old, readErr := n.Signal.readNodeRecord(n.Signal.nodeAddress(a))
		if readErr != nil {
			return nil, readErr
		}
		shape := &record.SessionStructure{}
		entries := nodeObj(nodeObj(old)["_sessions"])
		for _, raw := range entries {
			state, err := n.Signal.importState(raw)
			if err != nil {
				return nil, err
			}
			shape.PreviousStates = append(shape.PreviousStates, state)
		}
		if len(shape.PreviousStates) == 0 {
			return nil, errors.New("No open session")
		}
		placeholder := *shape.PreviousStates[0]
		// Version zero is an explicit inactive projection, never a wire Signal
		// session. The native serializer requires key-bearing fields even when
		// there is no open Node record. Real closed states remain in Archives.
		placeholder.SessionVersion = 0
		shape.SessionState = &placeholder
		return store.SignalProtobufSerializer.Session.Serialize(shape), nil
	}
	if e != nil {
		return nil, e
	}
	return r.Serialize(), nil
}
func (n *NodeDeviceStore) HasSession(ctx context.Context, address string) (bool, error) {
	a, e := deviceSignalAddress(address)
	if e != nil {
		return false, e
	}
	return n.Signal.ContainsSession(ctx, a)
}
func (n *NodeDeviceStore) GetManySessions(ctx context.Context, addresses []string) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, a := range addresses {
		v, e := n.GetSession(ctx, a)
		if e != nil {
			return nil, e
		}
		out[a] = v
	}
	return out, nil
}
func (n *NodeDeviceStore) PutSession(ctx context.Context, address string, raw []byte) error {
	a, e := deviceSignalAddress(address)
	if e != nil {
		return e
	}
	r, e := record.NewSessionFromBytes(raw, store.SignalProtobufSerializer.Session, store.SignalProtobufSerializer.State)
	if e != nil {
		return e
	}
	return n.Signal.StoreSession(ctx, a, r)
}
func (n *NodeDeviceStore) PutManySessions(ctx context.Context, values map[string][]byte) error {
	mutations := map[string]any{}
	for a, v := range values {
		address, e := deviceSignalAddress(a)
		if e != nil {
			return e
		}
		shape, e := store.SignalProtobufSerializer.Session.Deserialize(v)
		if e != nil {
			return e
		}
		old, e := n.Signal.readNodeRecord(n.Signal.nodeAddress(address))
		if e != nil {
			return e
		}
		converted, e := n.Signal.exportRecord(shape, old)
		if e != nil {
			return e
		}
		mutations[n.Signal.nodeAddress(address)] = converted
	}
	if len(mutations) == 0 {
		return nil
	}
	return n.commitSessionMutations(ctx, mutations)
}
func (n *NodeDeviceStore) DeleteSession(ctx context.Context, address string) error {
	a, e := deviceSignalAddress(address)
	if e != nil {
		return e
	}
	return n.Signal.DeleteSession(ctx, a)
}
func (n *NodeDeviceStore) DeleteAllSessions(ctx context.Context, phone string) error {
	entries, e := os.ReadDir(n.Signal.Dir)
	if e != nil {
		return e
	}
	for _, f := range entries {
		if strings.HasPrefix(f.Name(), "session-"+phone+".") {
			id := strings.TrimSuffix(strings.TrimPrefix(f.Name(), "session-"), ".json")
			i := strings.LastIndexByte(id, '.')
			if i < 0 {
				continue
			}
			if e = n.DeleteSession(ctx, id[:i]+":"+id[i+1:]); e != nil {
				return e
			}
		}
	}
	return nil
}
func (n *NodeDeviceStore) MigratePNToLID(ctx context.Context, pn, lid types.JID) error {
	entries, e := os.ReadDir(n.Signal.Dir)
	if e != nil {
		return e
	}
	for _, f := range entries {
		prefix := "session-" + pn.User + "."
		if !strings.HasPrefix(f.Name(), prefix) {
			continue
		}
		suffix := strings.TrimPrefix(f.Name(), prefix)
		raw, e := os.ReadFile(filepath.Join(n.Signal.Dir, f.Name()))
		if e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(n.Signal.Dir, "session-"+lid.User+"."+suffix), raw, 0666); e != nil {
			return e
		}
		if e = os.Remove(filepath.Join(n.Signal.Dir, f.Name())); e != nil {
			return e
		}
	}
	n.Signal.metadataMu.Lock()
	n.Signal.Sessions = map[string]json.RawMessage{}
	n.Signal.metadataMu.Unlock()
	return nil
}

// Original Baileys signalStorage intentionally accepts all peer identities and
// does not save them. The client-facing adapter retains that exact policy.
func (n *NodeDeviceStore) PutIdentity(ctx context.Context, address string, key [32]byte) error {
	return ctx.Err()
}
func (n *NodeDeviceStore) DeleteAllIdentities(ctx context.Context, phone string) error {
	return ctx.Err()
}
func (n *NodeDeviceStore) DeleteIdentity(ctx context.Context, address string) error { return ctx.Err() }
func (n *NodeDeviceStore) IsTrustedIdentity(ctx context.Context, address string, key [32]byte) (bool, error) {
	return true, ctx.Err()
}

func (n *NodeDeviceStore) GetPreKey(ctx context.Context, id uint32) (*keys.PreKey, error) {
	raw, e := n.getKey(ctx, "pre-key", strconv.FormatUint(uint64(id), 10))
	if e != nil || len(raw) == 0 {
		return nil, e
	}
	pair := nodeObj(raw)
	priv, pub := nodeBytes(pair["private"]), nodeBytes(pair["public"])
	if len(priv) != 32 || len(pub) != 32 {
		return nil, errors.New("invalid original prekey")
	}
	return &keys.PreKey{KeyPair: keys.KeyPair{Priv: (*[32]byte)(priv), Pub: (*[32]byte)(pub)}, KeyID: id}, nil
}
func (n *NodeDeviceStore) GetOrGenPreKeys(ctx context.Context, count uint32) ([]*keys.PreKey, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Signal.credsMu.Lock()
	defer n.Signal.credsMu.Unlock()
	first := uint32(nodeNumber(n.Signal.Creds["firstUnuploadedPreKeyId"]))
	next := uint32(nodeNumber(n.Signal.Creds["nextPreKeyId"]))
	end := first + count
	out := make([]*keys.PreKey, 0, count)
	for id := first; id < end; id++ {
		key, e := n.GetPreKey(ctx, id)
		if e != nil {
			return nil, e
		}
		if key == nil {
			if id < next {
				return nil, fmt.Errorf("original prekey %d missing", id)
			}
			key = &keys.PreKey{KeyPair: *keys.NewKeyPair(), KeyID: id}
			if e = n.putKey(ctx, "pre-key", strconv.FormatUint(uint64(id), 10), nodeKeyPair(&key.KeyPair)); e != nil {
				return nil, e
			}
		}
		out = append(out, key)
	}
	if end > next {
		n.Signal.Creds["nextPreKeyId"] = nodeJSON(end)
	}
	return out, nil
}
func (n *NodeDeviceStore) GenOnePreKey(ctx context.Context) (*keys.PreKey, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Signal.credsMu.Lock()
	defer n.Signal.credsMu.Unlock()
	id := uint32(nodeNumber(n.Signal.Creds["nextPreKeyId"]))
	key := &keys.PreKey{KeyPair: *keys.NewKeyPair(), KeyID: id}
	if e := n.putKey(ctx, "pre-key", strconv.FormatUint(uint64(id), 10), nodeKeyPair(&key.KeyPair)); e != nil {
		return nil, e
	}
	n.Signal.Creds["nextPreKeyId"] = nodeJSON(id + 1)
	return key, nil
}
func (n *NodeDeviceStore) RemovePreKey(ctx context.Context, id uint32) error {
	return n.putKey(ctx, "pre-key", strconv.FormatUint(uint64(id), 10), nil)
}
func (n *NodeDeviceStore) MarkPreKeysAsUploaded(ctx context.Context, id uint32) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Signal.credsMu.Lock()
	defer n.Signal.credsMu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	if int64(id+1) > nodeNumber(n.Signal.Creds["firstUnuploadedPreKeyId"]) {
		n.Signal.Creds["firstUnuploadedPreKeyId"] = nodeJSON(id + 1)
	}
	return n.enqueueCredsSaveLocked()
}
func (n *NodeDeviceStore) UploadedPreKeyCount(ctx context.Context) (int, error) {
	if e := ctx.Err(); e != nil {
		return 0, e
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Signal.credsMu.Lock()
	defer n.Signal.credsMu.Unlock()
	first := nodeNumber(n.Signal.Creds["firstUnuploadedPreKeyId"])
	entries, e := os.ReadDir(n.Signal.Dir)
	if e != nil {
		return 0, e
	}
	count := 0
	for _, f := range entries {
		if strings.HasPrefix(f.Name(), "pre-key-") {
			id, e := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(f.Name(), "pre-key-"), ".json"), 10, 64)
			if e == nil && id < first {
				count++
			}
		}
	}
	return count, nil
}
func (n *NodeDeviceStore) PutSenderKey(ctx context.Context, group, user string, value []byte) error {
	return errors.New("original WhatsApp group sender-key conversion is not bound to the direct-message adapter")
}
func (n *NodeDeviceStore) GetSenderKey(ctx context.Context, group, user string) ([]byte, error) {
	return nil, errors.New("original WhatsApp group sender-key conversion is not bound to the direct-message adapter")
}

func (n *NodeDeviceStore) PutDevice(ctx context.Context, d *store.Device) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Signal.credsMu.Lock()
	defer n.Signal.credsMu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	me := nodeObj(n.Signal.Creds["me"])
	newPair := len(me) == 0 && d.Account != nil
	if d.ID != nil {
		me["id"] = nodeJSON(d.ID.String())
	}
	if !d.LID.IsEmpty() {
		me["lid"] = nodeJSON(d.LID.String())
	}
	if d.PushName != "" {
		me["name"] = nodeJSON(d.PushName)
	} else if newPair && d.BusinessName != "" {
		me["name"] = nodeJSON(d.BusinessName)
	}
	n.Signal.Creds["me"] = nodeJSON(me)
	n.Signal.Creds["platform"] = nodeJSON(d.Platform)
	if d.Account != nil {
		account := nodeObj(n.Signal.Creds["account"])
		// protobufjs decodes these as Uint8Array; BufferJSON writes them as
		// base64 strings, unlike the Curve keypair Buffer envelopes.
		account["details"] = nodeEncoded(d.Account.Details)
		account["accountSignatureKey"] = nodeEncoded(d.Account.AccountSignatureKey)
		account["accountSignature"] = nodeEncoded(d.Account.AccountSignature)
		account["deviceSignature"] = nodeEncoded(d.Account.DeviceSignature)
		n.Signal.Creds["account"] = nodeJSON(account)
	}
	if newPair && !d.LID.IsEmpty() && d.ID != nil {
		var prior []json.RawMessage
		_ = json.Unmarshal(n.Signal.Creds["signalIdentities"], &prior)
		prior = append(prior, nodeJSON(map[string]any{"identifier": map[string]any{"name": d.LID.String(), "deviceId": 0}, "identifierKey": nodeBuffer(nodeSignedPublic(d.Account.GetAccountSignatureKey()))}))
		n.Signal.Creds["signalIdentities"] = nodeJSON(prior)
	}
	return n.enqueueCredsSaveLocked()
}
func (n *NodeDeviceStore) saveCredsLocked() error {
	path := filepath.Join(n.Signal.Dir, "creds.json")
	if old, e := os.ReadFile(path); e == nil && json.Valid(old) {
		if e = os.WriteFile(path+".bak", old, 0600); e == nil {
			_ = os.Chmod(path+".bak", 0600)
		}
	}
	if e := os.WriteFile(path, nodeJSON(n.Signal.Creds), 0600); e != nil {
		return e
	}
	return os.Chmod(path, 0600)
}
func (n *NodeDeviceStore) DeleteDevice(ctx context.Context, d *store.Device) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	// The original socket's logged-out handling invalidates its connection but
	// preserves authDir. Only explicit user unlink calls clearWhatsAppAuth.
	n.Signal.metadataMu.Lock()
	n.Signal.Sessions = map[string]json.RawMessage{}
	n.Signal.metadataMu.Unlock()
	n.cache.Clear()
	return nil
}

func (n *NodeDeviceStore) ClearAuth(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return os.RemoveAll(n.Signal.Dir)
}

type nodeDeviceLIDs struct{ original *NodeLIDStore }

func (n nodeDeviceLIDs) PutManyLIDMappings(ctx context.Context, values []store.LIDMapping) error {
	pairs := make([][2]string, len(values))
	for i, v := range values {
		pairs[i] = [2]string{v.LID.String(), v.PN.String()}
	}
	return n.original.StoreLIDPNMappings(ctx, pairs)
}
func (n nodeDeviceLIDs) PutLIDMapping(ctx context.Context, lid, pn types.JID) error {
	return n.PutManyLIDMappings(ctx, []store.LIDMapping{{LID: lid, PN: pn}})
}
func (n nodeDeviceLIDs) GetPNForLID(ctx context.Context, lid types.JID) (types.JID, error) {
	v, e := n.original.GetPNForLID(ctx, lid.String())
	if e != nil || v == "" {
		return types.EmptyJID, e
	}
	return types.ParseJID(v)
}
func (n nodeDeviceLIDs) GetLIDForPN(ctx context.Context, pn types.JID) (types.JID, error) {
	v, e := n.original.GetLIDForPN(ctx, pn.String())
	if e != nil || v == "" {
		return types.EmptyJID, e
	}
	return types.ParseJID(v)
}
func (n nodeDeviceLIDs) GetManyLIDsForPNs(ctx context.Context, pns []types.JID) (map[types.JID]types.JID, error) {
	out := map[types.JID]types.JID{}
	for _, pn := range pns {
		lid, e := n.GetLIDForPN(ctx, pn)
		if e != nil {
			return nil, e
		}
		if !lid.IsEmpty() {
			out[pn] = lid
		}
	}
	return out, nil
}

func (n *NodeDeviceStore) keysByCategory(category string) ([]string, error) {
	entries, e := os.ReadDir(n.Signal.Dir)
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for _, f := range entries {
		if strings.HasPrefix(f.Name(), category+"-") && strings.HasSuffix(f.Name(), ".json") {
			ids = append(ids, strings.TrimSuffix(strings.TrimPrefix(f.Name(), category+"-"), ".json"))
		}
	}
	sort.Strings(ids)
	return ids, nil
}
