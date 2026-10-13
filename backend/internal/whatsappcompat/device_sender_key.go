package whatsappcandidate

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"go.mau.fi/libsignal/groups/ratchet"
	groupRecord "go.mau.fi/libsignal/groups/state/record"
	"go.mau.fi/whatsmeow/store"
)

func nodeSenderKeyID(group, user string) (string, error) {
	address, err := deviceSignalAddress(user)
	if err != nil {
		return "", err
	}
	return group + "::" + address.Name() + "::" + strconv.FormatUint(uint64(address.DeviceID()), 10), nil
}

func senderKeyRows(raw json.RawMessage) ([]nodeObject, error) {
	if len(raw) == 0 {
		return []nodeObject{}, nil
	}
	encoded := nodeBytes(raw)
	var rows []nodeObject
	if len(encoded) == 0 || json.Unmarshal(encoded, &rows) != nil || rows == nil {
		return nil, errors.New("invalid original sender-key record")
	}
	return rows, nil
}

func cloneSenderKeyObject(value nodeObject) nodeObject {
	copy := nodeObject{}
	for name, raw := range value {
		copy[name] = append(json.RawMessage(nil), raw...)
	}
	return copy
}

func senderKeyBytes(raw json.RawMessage) []byte {
	// The outer auth file uses BufferJSON base64; the inner SenderKeyRecord is
	// JSON.stringify without that replacer and contains Node Buffer byte arrays.
	object := nodeObj(raw)
	data := object["data"]
	if nodeString(object["type"]) == "Buffer" && len(data) > 0 && data[0] == '[' {
		var bytes []byte
		if json.Unmarshal(data, &bytes) != nil {
			return nil
		}
		return bytes
	}
	return nodeBytes(raw)
}

func importSenderKeyRows(rows []nodeObject) (*groupRecord.SenderKeyStructure, error) {
	structure := &groupRecord.SenderKeyStructure{SenderKeyStates: []*groupRecord.SenderKeyStateStructure{}}
	first := map[uint32]int{}
	for index, row := range rows {
		var id uint32
		if json.Unmarshal(row["senderKeyId"], &id) != nil {
			return nil, errors.New("invalid original sender-key state counter")
		}
		if _, found := first[id]; !found {
			first[id] = index
		}
	}
	// Node keeps oldest to newest; the actual Go current-state consumer selects
	// its first element. Reverse at the boundary without rewriting auth files.
	// Repeated distributions may repeat an ID: Node decrypts its oldest match.
	// Project that selected state once; the remaining original states stay in
	// the physical auth record and are retained when committing below.
	for index := len(rows) - 1; index >= 0; index-- {
		row := rows[index]
		id := uint32(nodeNumber(row["senderKeyId"]))
		if first[id] != index {
			continue
		}
		chain := nodeObj(row["senderChainKey"])
		signing := nodeObj(row["senderSigningKey"])
		seed, public, private := senderKeyBytes(chain["seed"]), senderKeyBytes(signing["public"]), senderKeyBytes(signing["private"])
		if len(seed) != 32 || len(public) != 32 && (len(public) != 33 || public[0] != 5) || len(private) != 0 && len(private) != 32 {
			return nil, errors.New("invalid original sender-key state keys")
		}
		if len(public) == 32 {
			public = append([]byte{5}, public...)
		}
		var iteration uint32
		if json.Unmarshal(chain["iteration"], &iteration) != nil {
			return nil, errors.New("invalid original sender-key state counter")
		}
		state := &groupRecord.SenderKeyStateStructure{KeyID: id, SenderChainKey: &ratchet.SenderChainKeyStructure{Iteration: iteration, ChainKey: seed}, SigningKeyPublic: public, SigningKeyPrivate: private, Keys: []*ratchet.SenderMessageKeyStructure{}}
		var keys []nodeObject
		if len(row["senderMessageKeys"]) > 0 && string(row["senderMessageKeys"]) != "null" && json.Unmarshal(row["senderMessageKeys"], &keys) != nil {
			return nil, errors.New("invalid original sender-key skipped keys")
		}
		for _, key := range keys {
			seed := senderKeyBytes(key["seed"])
			var iteration uint32
			if len(seed) != 32 || json.Unmarshal(key["iteration"], &iteration) != nil {
				return nil, errors.New("invalid original sender-key skipped seed")
			}
			derived, err := ratchet.NewSenderMessageKey(iteration, seed)
			if err != nil {
				return nil, err
			}
			state.Keys = append(state.Keys, ratchet.NewStructFromSenderMessageKey(derived))
		}
		structure.SenderKeyStates = append(structure.SenderKeyStates, state)
	}
	return structure, nil
}

func (n *NodeDeviceStore) GetSenderKey(ctx context.Context, group, user string) ([]byte, error) {
	id, err := nodeSenderKeyID(group, user)
	if err != nil {
		return nil, err
	}
	raw, err := n.getKey(ctx, "sender-key", id)
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	rows, err := senderKeyRows(raw)
	if err != nil {
		return nil, err
	}
	structure, err := importSenderKeyRows(rows)
	if err != nil {
		return nil, err
	}
	return store.SignalProtobufSerializer.SenderKeyRecord.Serialize(structure), nil
}

func (n *NodeDeviceStore) PutSenderKey(ctx context.Context, group, user string, value []byte) error {
	id, err := nodeSenderKeyID(group, user)
	if err != nil {
		return err
	}
	structure, err := store.SignalProtobufSerializer.SenderKeyRecord.Deserialize(value)
	if err != nil {
		return err
	}
	old, err := n.getKey(ctx, "sender-key", id)
	if err != nil {
		return err
	}
	previous, err := senderKeyRows(old)
	if err != nil {
		return err
	}
	byID := map[uint32]nodeObject{}
	for _, row := range previous {
		id := uint32(nodeNumber(row["senderKeyId"]))
		if _, found := byID[id]; !found {
			byID[id] = row
		}
	}
	rows := []nodeObject{}
	present := map[uint32]bool{}
	for _, state := range structure.SenderKeyStates {
		if state != nil {
			present[state.KeyID] = true
		}
	}
	positions := map[uint32]int{}
	for _, row := range previous {
		id := uint32(nodeNumber(row["senderKeyId"]))
		if !present[id] {
			continue
		}
		if _, found := positions[id]; !found {
			positions[id] = len(rows)
		}
		rows = append(rows, cloneSenderKeyObject(row))
	}
	updated := map[uint32]bool{}
	for index := len(structure.SenderKeyStates) - 1; index >= 0; index-- {
		state := structure.SenderKeyStates[index]
		if state == nil || state.SenderChainKey == nil || len(state.SenderChainKey.ChainKey) != 32 || len(state.SigningKeyPublic) != 33 || state.SigningKeyPublic[0] != 5 {
			return errors.New("invalid native sender-key state")
		}
		row := cloneSenderKeyObject(byID[state.KeyID])
		if updated[state.KeyID] {
			row = nodeObject{}
		}
		chain := nodeObj(row["senderChainKey"])
		chain["iteration"], chain["seed"] = nodeJSON(state.SenderChainKey.Iteration), nodeBuffer(state.SenderChainKey.ChainKey)
		signing := nodeObj(row["senderSigningKey"])
		public := state.SigningKeyPublic
		if len(senderKeyBytes(signing["public"])) != 33 {
			public = public[1:]
		}
		private := state.SigningKeyPrivate
		if _, existed := byID[state.KeyID]; existed && len(senderKeyBytes(signing["private"])) == 0 {
			// The Go record reader projects an absent recipient private key as
			// a zero key object. Retain Source's absent private-key semantics.
			private = []byte{}
		}
		signing["public"], signing["private"] = nodeBuffer(public), nodeBuffer(private)
		keys := []nodeObject{}
		for _, key := range state.Keys {
			if key == nil || len(key.Seed) != 32 {
				return errors.New("native sender-key skipped seed is unavailable")
			}
			keys = append(keys, nodeObject{"iteration": nodeJSON(key.Iteration), "seed": nodeBuffer(key.Seed)})
		}
		row["senderKeyId"], row["senderChainKey"], row["senderSigningKey"], row["senderMessageKeys"] = nodeJSON(state.KeyID), nodeJSON(chain), nodeJSON(signing), nodeJSON(keys)
		if position, found := positions[state.KeyID]; found && !updated[state.KeyID] {
			rows[position] = row
		} else {
			rows = append(rows, row)
		}
		updated[state.KeyID] = true
	}
	if len(rows) > 5 {
		rows = rows[len(rows)-5:]
	}
	return n.putKey(ctx, "sender-key", id, nodeBuffer(nodeJSON(rows)))
}
