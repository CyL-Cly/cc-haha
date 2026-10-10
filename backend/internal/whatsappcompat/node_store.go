package whatsappcandidate

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
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

	"go.mau.fi/libsignal/ecc"
	"go.mau.fi/libsignal/kdf"
	"go.mau.fi/libsignal/keys/chain"
	"go.mau.fi/libsignal/keys/identity"
	"go.mau.fi/libsignal/keys/message"
	sig "go.mau.fi/libsignal/protocol"
	"go.mau.fi/libsignal/session"
	"go.mau.fi/libsignal/state/record"
	"go.mau.fi/libsignal/util/optional"
	"go.mau.fi/whatsmeow/store"
)

type nodeObject map[string]json.RawMessage

func nodeObj(v json.RawMessage) nodeObject {
	var o nodeObject
	_ = json.Unmarshal(v, &o)
	if o == nil {
		o = nodeObject{}
	}
	return o
}
func nodeJSON(v any) json.RawMessage      { b, _ := json.Marshal(v); return b }
func nodeString(v json.RawMessage) string { var s string; _ = json.Unmarshal(v, &s); return s }
func nodeNumber(v json.RawMessage) int64  { var n int64; _ = json.Unmarshal(v, &n); return n }
func nodeBytes(v json.RawMessage) []byte {
	s := nodeString(v)
	if s != "" {
		b, _ := base64.StdEncoding.DecodeString(s)
		return b
	}
	o := nodeObj(v)
	if nodeString(o["type"]) == "Buffer" {
		b, _ := base64.StdEncoding.DecodeString(nodeString(o["data"]))
		return b
	}
	return nil
}
func nodeEncoded(v []byte) json.RawMessage { return nodeJSON(base64.StdEncoding.EncodeToString(v)) }
func nodeSignedPublic(v []byte) []byte {
	if len(v) == 32 {
		return append([]byte{5}, v...)
	}
	return v
}
func nodeHash(seed, key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(seed)
	return mac.Sum(nil)
}
func nodeDerived(seed []byte, index uint32) (*message.KeysStructure, error) {
	data, e := kdf.DeriveSecrets(seed, nil, []byte(message.KdfSalt), 80)
	if e != nil {
		return nil, e
	}
	return &message.KeysStructure{CipherKey: data[:32], MacKey: data[32:64], IV: data[64:80], Index: index}, nil
}
func nodeKeysEqual(a, b *message.KeysStructure) bool {
	return a.Index == b.Index && bytes.Equal(a.CipherKey, b.CipherKey) && bytes.Equal(a.MacKey, b.MacKey) && bytes.Equal(a.IV, b.IV)
}

// NodeSignalStore retains the original Node envelopes as its authoritative
// records. The native serializer receives an ephemeral structural translation.
// This candidate is not yet bound to WhatsApp's Noise client or product files.
type NodeSignalStore struct {
	metadataMu  sync.Mutex
	credsMu     sync.RWMutex
	gates       sync.Map
	Dir         string
	Creds       nodeObject
	Sessions    map[string]json.RawMessage
	Now         func() time.Time
	readRecord  func(string) (json.RawMessage, error)
	writeRecord func(string, json.RawMessage) error
}

func (s *NodeSignalStore) nodeAddress(a *sig.SignalAddress) string {
	return a.Name() + "." + strconv.FormatUint(uint64(a.DeviceID()), 10)
}
func (s *NodeSignalStore) GetIdentityKeyPair() *identity.KeyPair {
	s.credsMu.RLock()
	defer s.credsMu.RUnlock()
	o := nodeObj(s.Creds["signedIdentityKey"])
	pub, priv := nodeBytes(o["public"]), nodeBytes(o["private"])
	return identity.NewKeyPair(identity.NewKey(ecc.NewDjbECPublicKey(*(*[32]byte)(pub))), ecc.NewDjbECPrivateKey(*(*[32]byte)(priv)))
}
func (s *NodeSignalStore) GetLocalRegistrationID() uint32 {
	s.credsMu.RLock()
	defer s.credsMu.RUnlock()
	return uint32(nodeNumber(s.Creds["registrationId"]))
}
func (s *NodeSignalStore) SaveIdentity(context.Context, *sig.SignalAddress, *identity.Key) error {
	return nil
} // original Baileys signalStorage does not persist peer trust
func (s *NodeSignalStore) IsTrustedIdentity(context.Context, *sig.SignalAddress, *identity.Key) (bool, error) {
	return true, nil
} // exact original signalStorage predicate
func (s *NodeSignalStore) LoadSession(ctx context.Context, a *sig.SignalAddress) (*record.Session, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	v, e := s.readNodeRecord(s.nodeAddress(a))
	if e != nil {
		return nil, e
	}
	if len(v) == 0 {
		return record.NewSession(store.SignalProtobufSerializer.Session, store.SignalProtobufSerializer.State), nil
	}
	shape, e := s.importRecord(v)
	if e != nil {
		return nil, e
	}
	return record.NewSessionFromStructure(shape, store.SignalProtobufSerializer.Session, store.SignalProtobufSerializer.State)
}
func (s *NodeSignalStore) StoreSession(ctx context.Context, a *sig.SignalAddress, r *record.Session) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	shape, e := store.SignalProtobufSerializer.Session.Deserialize(r.Serialize())
	if e != nil {
		return e
	}
	previous, e := s.readNodeRecord(s.nodeAddress(a))
	if e != nil {
		return e
	}
	v, e := s.exportRecord(shape, previous)
	if e != nil {
		return e
	}
	if s.Dir != "" {
		var writeErr error
		if s.writeRecord != nil {
			writeErr = s.writeRecord(s.nodeAddress(a), v)
		} else {
			writeErr = os.WriteFile(s.nodePath(s.nodeAddress(a)), v, 0666)
		}
		if writeErr != nil {
			return writeErr
		}
	}
	s.metadataMu.Lock()
	s.Sessions[s.nodeAddress(a)] = v
	s.metadataMu.Unlock()
	return nil
}
func (s *NodeSignalStore) ContainsSession(ctx context.Context, a *sig.SignalAddress) (bool, error) {
	if e := ctx.Err(); e != nil {
		return false, e
	}
	v, e := s.readNodeRecord(s.nodeAddress(a))
	return len(v) > 0, e
}
func (s *NodeSignalStore) GetSubDeviceSessions(context.Context, string) ([]uint32, error) {
	return nil, errors.New("candidate subdevice enumeration not bound")
}
func (s *NodeSignalStore) DeleteSession(ctx context.Context, a *sig.SignalAddress) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if s.Dir != "" {
		e := os.Remove(s.nodePath(s.nodeAddress(a)))
		if e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	s.metadataMu.Lock()
	delete(s.Sessions, s.nodeAddress(a))
	s.metadataMu.Unlock()
	return nil
}
func (s *NodeSignalStore) DeleteAllSessions(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if s.Dir != "" {
		entries, e := os.ReadDir(s.Dir)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasPrefix(entry.Name(), "session-") && strings.HasSuffix(entry.Name(), ".json") {
				if e := os.Remove(filepath.Join(s.Dir, entry.Name())); e != nil {
					return e
				}
			}
		}
	}
	s.metadataMu.Lock()
	s.Sessions = map[string]json.RawMessage{}
	s.metadataMu.Unlock()
	return nil
}
func (s *NodeSignalStore) importRecord(v json.RawMessage) (*record.SessionStructure, error) {
	o := nodeObj(v)
	o, e := migrateNodeRecord(o)
	if e != nil {
		return nil, e
	}
	entries := nodeObj(o["_sessions"])
	var active json.RawMessage
	archived := []json.RawMessage{}
	for _, v := range entries {
		if nodeNumber(nodeObj(nodeObj(v)["indexInfo"])["closed"]) == -1 {
			if active != nil {
				return nil, errors.New("multiple open original Node sessions")
			}
			active = v
		} else {
			archived = append(archived, v)
		}
	}
	if active == nil {
		return nil, errors.New("No open session")
	}
	sort.SliceStable(archived, func(i, j int) bool {
		return nodeNumber(nodeObj(nodeObj(archived[i])["indexInfo"])["used"]) > nodeNumber(nodeObj(nodeObj(archived[j])["indexInfo"])["used"])
	})
	current, e := s.importState(active)
	if e != nil {
		return nil, e
	}
	out := &record.SessionStructure{SessionState: current}
	for _, v := range archived {
		state, e := s.importState(v)
		if e != nil {
			return nil, e
		}
		out.PreviousStates = append(out.PreviousStates, state)
	}
	return out, nil
}
func (s *NodeSignalStore) importState(v json.RawMessage) (*record.StateStructure, error) {
	s.credsMu.RLock()
	identity := append(json.RawMessage{}, s.Creds["signedIdentityKey"]...)
	s.credsMu.RUnlock()
	o := nodeObj(v)
	ratchet, index := nodeObj(o["currentRatchet"]), nodeObj(o["indexInfo"])
	pair := nodeObj(ratchet["ephemeralKeyPair"])
	pub, priv := nodeBytes(pair["pubKey"]), nodeBytes(pair["privKey"])
	state := &record.StateStructure{LocalIdentityPublic: nodeSignedPublic(nodeBytes(nodeObj(identity)["public"])), LocalRegistrationID: s.GetLocalRegistrationID(), RemoteRegistrationID: uint32(nodeNumber(o["registrationId"])), RemoteIdentityPublic: nodeBytes(index["remoteIdentityKey"]), PreviousCounter: uint32(nodeNumber(ratchet["previousCounter"])), RootKey: nodeBytes(ratchet["rootKey"]), SenderBaseKey: nodeBytes(index["baseKey"]), SessionVersion: 3}
	chains := nodeObj(o["_chains"])
	names := []string{}
	for key := range chains {
		names = append(names, key)
	}
	sort.Strings(names)
	for _, name := range names {
		c := nodeObj(chains[name])
		chainKey := nodeObj(c["chainKey"])
		keyPub, _ := base64.StdEncoding.DecodeString(name)
		target := &record.ChainStructure{SenderRatchetKeyPublic: keyPub, ChainKey: &chain.KeyStructure{Key: nodeBytes(chainKey["key"]), Index: uint32(nodeNumber(chainKey["counter"]) + 1)}}
		if nodeNumber(c["chainType"]) == 1 {
			target.SenderRatchetKeyPrivate = priv
			if !bytes.Equal(pub, keyPub) {
				return nil, errors.New("original sender ratchet identity mismatch")
			}
		}
		keys := nodeObj(c["messageKeys"])
		numbers := []int{}
		for n := range keys {
			i, e := strconv.Atoi(n)
			if e != nil {
				return nil, e
			}
			numbers = append(numbers, i)
		}
		sort.Ints(numbers)
		for _, i := range numbers {
			k, e := nodeDerived(nodeBytes(keys[strconv.Itoa(i)]), uint32(i))
			if e != nil {
				return nil, e
			}
			target.MessageKeys = append(target.MessageKeys, k)
		}
		if nodeNumber(c["chainType"]) == 1 {
			state.SenderChain = target
		} else {
			state.ReceiverChains = append(state.ReceiverChains, target)
		}
	}
	if state.SenderChain == nil {
		return nil, errors.New("original session has no active sending chain")
	}
	if p := nodeObj(o["pendingPreKey"]); len(p) > 0 {
		state.PendingPreKey = &record.PendingPreKeyStructure{SignedPreKeyID: uint32(nodeNumber(p["signedKeyId"])), BaseKey: nodeBytes(p["baseKey"])}
		if p["preKeyId"] != nil {
			state.PendingPreKey.PreKeyID = optional.NewOptionalUint32(uint32(nodeNumber(p["preKeyId"])))
		} else {
			state.PendingPreKey.PreKeyID = optional.NewEmptyUint32()
		}
	}
	return state, nil
}
func (s *NodeSignalStore) exportRecord(shape *record.SessionStructure, previous json.RawMessage) (json.RawMessage, error) {
	root := nodeObj(previous)
	entries := nodeObj(root["_sessions"])
	out := nodeObject{}
	states := append([]*record.StateStructure{shape.SessionState}, shape.PreviousStates...)
	for i, state := range states {
		if state == nil || state.SenderChain == nil || state.SessionVersion == 0 {
			continue
		}
		base := base64.StdEncoding.EncodeToString(state.SenderBaseKey)
		old := entries[base]
		entry, e := s.exportState(state, old, i == 0)
		if e != nil {
			return nil, e
		}
		out[base] = entry
	}
	root["_sessions"] = nodeJSON(out)
	root["version"] = nodeJSON("v1")
	return nodeJSON(root), nil
}
func (s *NodeSignalStore) exportState(state *record.StateStructure, previous json.RawMessage, active bool) (json.RawMessage, error) {
	o := nodeObj(previous)
	oldRatchet := nodeObj(o["currentRatchet"])
	oldChains := nodeObj(o["_chains"])
	index := nodeObj(o["indexInfo"])
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	if len(index) == 0 {
		index["baseKeyType"] = nodeJSON(2)
		index["created"] = nodeJSON(now.UnixMilli())
		index["used"] = nodeJSON(now.UnixMilli())
	}
	index["baseKey"] = nodeEncoded(state.SenderBaseKey)
	index["remoteIdentityKey"] = nodeEncoded(state.RemoteIdentityPublic)
	if active {
		index["closed"] = nodeJSON(-1)
	} else if nodeNumber(index["closed"]) == -1 {
		index["closed"] = nodeJSON(now.UnixMilli())
	}
	o["registrationId"] = nodeJSON(state.RemoteRegistrationID)
	o["indexInfo"] = nodeJSON(index)
	ratchet := nodeObj(o["currentRatchet"])
	pair := nodeObj(ratchet["ephemeralKeyPair"])
	pair["pubKey"] = nodeEncoded(state.SenderChain.SenderRatchetKeyPublic)
	pair["privKey"] = nodeEncoded(state.SenderChain.SenderRatchetKeyPrivate)
	ratchet["ephemeralKeyPair"] = nodeJSON(pair)
	ratchet["rootKey"] = nodeEncoded(state.RootKey)
	ratchet["previousCounter"] = nodeJSON(state.PreviousCounter)
	newChains := nodeObject{}
	all := append([]*record.ChainStructure{state.SenderChain}, state.ReceiverChains...)
	for i, c := range all {
		name := base64.StdEncoding.EncodeToString(c.SenderRatchetKeyPublic)
		old := nodeObj(oldChains[name])
		keys, e := s.exportMessageSeeds(c, old, oldRatchet, i > 0)
		if e != nil {
			return nil, e
		}
		old["messageKeys"] = nodeJSON(keys)
		old["chainType"] = nodeJSON(1)
		if i > 0 {
			old["chainType"] = nodeJSON(2)
		}
		ck := nodeObj(old["chainKey"])
		ck["counter"] = nodeJSON(int64(c.ChainKey.Index) - 1)
		if len(c.ChainKey.Key) > 0 {
			ck["key"] = nodeEncoded(c.ChainKey.Key)
		} else {
			delete(ck, "key")
		}
		old["chainKey"] = nodeJSON(ck)
		newChains[name] = nodeJSON(old)
		if i > 0 && len(oldChains[name]) == 0 {
			ratchet["lastRemoteEphemeralKey"] = nodeEncoded(c.SenderRatchetKeyPublic)
		}
	}
	o["_chains"] = nodeJSON(newChains)
	o["currentRatchet"] = nodeJSON(ratchet)
	if state.PendingPreKey == nil {
		delete(o, "pendingPreKey")
	} else {
		p := nodeObj(o["pendingPreKey"])
		p["signedKeyId"] = nodeJSON(state.PendingPreKey.SignedPreKeyID)
		p["baseKey"] = nodeEncoded(state.PendingPreKey.BaseKey)
		if state.PendingPreKey.PreKeyID != nil && !state.PendingPreKey.PreKeyID.IsEmpty {
			p["preKeyId"] = nodeJSON(state.PendingPreKey.PreKeyID.Value)
		} else {
			delete(p, "preKeyId")
		}
		o["pendingPreKey"] = nodeJSON(p)
	}
	return nodeJSON(o), nil
}
func (s *NodeSignalStore) exportMessageSeeds(c *record.ChainStructure, old, oldRatchet nodeObject, receiver bool) (nodeObject, error) {
	priorKeys := nodeObj(old["messageKeys"])
	out := nodeObject{}
	oldChain := nodeObj(old["chainKey"])
	chainBytes := nodeBytes(oldChain["key"])
	index := uint32(nodeNumber(oldChain["counter"]) + 1)
	// Native Node sessions retain each newly skipped seed before advancing the
	// chain. Old records still reconstruct any seeds not present in that shape.
	needsPriorRatchet := false
	for _, mk := range c.MessageKeys {
		if len(mk.NodeSeed) == 0 && len(nodeBytes(priorKeys[strconv.FormatUint(uint64(mk.Index), 10)])) == 0 {
			needsPriorRatchet = true
		}
	}
	if len(old) == 0 && receiver && needsPriorRatchet {
		priv := nodeBytes(nodeObj(oldRatchet["ephemeralKeyPair"])["privKey"])
		remote := c.SenderRatchetKeyPublic
		if len(priv) != 32 || len(remote) != 33 {
			return nil, errors.New("missing prior ratchet for new receiver seed reconstruction")
		}
		key, e := ecdh.X25519().NewPrivateKey(priv)
		if e != nil {
			return nil, e
		}
		pub, e := ecdh.X25519().NewPublicKey(remote[1:])
		if e != nil {
			return nil, e
		}
		secret, e := key.ECDH(pub)
		if e != nil {
			return nil, e
		}
		expanded, e := kdf.DeriveSecrets(secret, nodeBytes(oldRatchet["rootKey"]), []byte("WhisperRatchet"), 64)
		if e != nil {
			return nil, e
		}
		chainBytes = expanded[32:64]
		index = 0
	}
	for _, mk := range c.MessageKeys {
		n := strconv.FormatUint(uint64(mk.Index), 10)
		seed := nodeBytes(priorKeys[n])
		if len(mk.NodeSeed) > 0 {
			derived, e := nodeDerived(mk.NodeSeed, mk.Index)
			if e != nil || !nodeKeysEqual(derived, mk) {
				return nil, errors.New("retained Node skipped seed no longer matches native key")
			}
			out[n] = nodeEncoded(mk.NodeSeed)
			continue
		}
		if len(seed) > 0 {
			derived, e := nodeDerived(seed, mk.Index)
			if e != nil || !nodeKeysEqual(derived, mk) {
				return nil, errors.New("original skipped seed no longer matches native key")
			}
			out[n] = nodeEncoded(seed)
			continue
		}
		if len(chainBytes) == 0 || mk.Index < index {
			return nil, fmt.Errorf("native skipped message key %d lacks reconstructible original seed", mk.Index)
		}
		for index < mk.Index {
			chainBytes = nodeHash([]byte{2}, chainBytes)
			index++
		}
		seed = nodeHash([]byte{1}, chainBytes)
		derived, e := nodeDerived(seed, mk.Index)
		if e != nil || !nodeKeysEqual(derived, mk) {
			return nil, fmt.Errorf("reconstructed skipped seed %d does not match actual native key", mk.Index)
		}
		out[n] = nodeEncoded(seed)
	}
	return out, nil
}

func (s *NodeSignalStore) nodePath(address string) string {
	return filepath.Join(s.Dir, strings.ReplaceAll(strings.ReplaceAll("session-"+address+".json", "/", "__"), ":", "-"))
}
func (s *NodeSignalStore) readNodeRecord(address string) (json.RawMessage, error) {
	if s.readRecord != nil {
		return s.readRecord(address)
	}
	if s.Dir == "" {
		s.metadataMu.Lock()
		defer s.metadataMu.Unlock()
		return append(json.RawMessage{}, s.Sessions[address]...), nil
	}
	v, e := os.ReadFile(s.nodePath(address))
	if e != nil || !json.Valid(v) {
		return nil, nil
	}
	return v, nil
}
func OpenNodeSignalDirectory(dir string, now func() time.Time) (*NodeSignalStore, error) {
	v, e := os.ReadFile(filepath.Join(dir, "creds.json"))
	if e != nil {
		return nil, e
	}
	return &NodeSignalStore{Dir: dir, Creds: nodeObj(v), Sessions: map[string]json.RawMessage{}, Now: now}, nil
}

// DecryptNodeMessage mirrors original Node decryptWithSessions: each failed
// candidate is discarded and a successful archived candidate stays archived.
// The upstream Go Cipher promotes archived states and mutates failed candidates,
// so its whole-record method cannot serve the old Node store contract directly.
func (s *NodeSignalStore) DecryptNodeMessage(ctx context.Context, address *sig.SignalAddress, cipher *session.Cipher, message *sig.SignalMessage) ([]byte, error) {
	release, e := s.acquireAddress(ctx, s.nodeAddress(address))
	if e != nil {
		return nil, e
	}
	defer release()
	original, e := s.readNodeRecord(s.nodeAddress(address))
	if e != nil {
		return nil, e
	}
	root, e := migrateNodeRecord(nodeObj(original))
	if e != nil {
		return nil, e
	}
	root["version"] = nodeJSON("v1")
	entries := nodeObj(root["_sessions"])
	names := []string{}
	for name := range entries {
		names = append(names, name)
	}
	sort.SliceStable(names, func(i, j int) bool {
		return nodeNumber(nodeObj(nodeObj(entries[names[i]])["indexInfo"])["used"]) > nodeNumber(nodeObj(nodeObj(entries[names[j]])["indexInfo"])["used"])
	})
	var last error
	for _, name := range names {
		previous := entries[name]
		shape, e := s.importState(previous)
		if e != nil {
			return nil, e
		}
		state, e := record.NewStateFromStructure(shape, store.SignalProtobufSerializer.State)
		if e != nil {
			return nil, e
		}
		plain, _, e := cipher.DecryptWithState(ctx, state, message)
		if e != nil {
			last = e
			continue
		}
		active := nodeNumber(nodeObj(nodeObj(previous)["indexInfo"])["closed"]) == -1
		updated, e := store.SignalProtobufSerializer.State.Deserialize(state.Serialize())
		if e != nil {
			return nil, e
		}
		v, e := s.exportState(updated, previous, active)
		if e != nil {
			return nil, e
		}
		entry := nodeObj(v)
		index := nodeObj(entry["indexInfo"])
		now := time.Now()
		if s.Now != nil {
			now = s.Now()
		}
		index["used"] = nodeJSON(now.UnixMilli())
		entry["indexInfo"] = nodeJSON(index)
		entries[name] = nodeJSON(entry)
		root["_sessions"] = nodeJSON(entries)
		data := nodeJSON(root)
		if s.Dir != "" {
			if e := os.WriteFile(s.nodePath(s.nodeAddress(address)), data, 0666); e != nil {
				return nil, e
			}
		}
		s.metadataMu.Lock()
		s.Sessions[s.nodeAddress(address)] = data
		s.metadataMu.Unlock()
		return plain, nil
	}
	if last == nil {
		last = errors.New("No sessions available")
	}
	return nil, last
}

func migrateNodeRecord(o nodeObject) (nodeObject, error) {
	if nodeString(o["version"]) == "v1" {
		return o, nil
	}
	if len(o["version"]) > 0 {
		return nil, errors.New("Error migrating SessionRecord")
	}
	entries := nodeObj(o["_sessions"])
	if id := o["registrationId"]; nodeNumber(id) != 0 {
		for name, v := range entries {
			entry := nodeObj(v)
			if nodeNumber(entry["registrationId"]) == 0 {
				entry["registrationId"] = id
			}
			entries[name] = nodeJSON(entry)
		}
	}
	o["_sessions"] = nodeJSON(entries)
	return o, nil
}

func (s *NodeSignalStore) acquireAddress(ctx context.Context, address string) (func(), error) {
	newGate := make(chan struct{}, 1)
	newGate <- struct{}{}
	actual, _ := s.gates.LoadOrStore(address, newGate)
	gate := actual.(chan struct{})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-gate:
		return func() { gate <- struct{}{} }, nil
	}
}
func (s *NodeSignalStore) EncryptNodeMessage(ctx context.Context, address *sig.SignalAddress, cipher *session.Cipher, plain []byte) (sig.CiphertextMessage, error) {
	release, e := s.acquireAddress(ctx, s.nodeAddress(address))
	if e != nil {
		return nil, e
	}
	defer release()
	return cipher.Encrypt(ctx, plain)
}
