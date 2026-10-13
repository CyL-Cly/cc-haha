package whatsappcandidate

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/util/keys"
)

func nodeBuffer(data []byte) json.RawMessage {
	return nodeJSON(map[string]any{"type": "Buffer", "data": base64.StdEncoding.EncodeToString(data)})
}
func nodeKeyPair(pair *keys.KeyPair) json.RawMessage {
	return nodeJSON(nodeObject{"private": nodeBuffer(pair.Priv[:]), "public": nodeBuffer(pair.Pub[:])})
}
func CreateNodeCredentials() (nodeObject, error) {
	identity := keys.NewKeyPair()
	signed := identity.CreateSignedPreKey(1)
	registration := make([]byte, 2)
	if _, e := rand.Read(registration); e != nil {
		return nil, e
	}
	adv := make([]byte, 32)
	if _, e := rand.Read(adv); e != nil {
		return nil, e
	}
	// The pinned original uses Uint16Array.from(randomBytes(2))[0], which
	// widens the first byte rather than reading a two-byte integer.
	return nodeObject{"noiseKey": nodeKeyPair(keys.NewKeyPair()), "pairingEphemeralKeyPair": nodeKeyPair(keys.NewKeyPair()), "signedIdentityKey": nodeKeyPair(identity), "signedPreKey": nodeJSON(nodeObject{"keyPair": nodeKeyPair(&signed.KeyPair), "signature": nodeBuffer(signed.Signature[:]), "keyId": nodeJSON(1)}), "registrationId": nodeJSON(uint16(registration[0]) & 16383), "advSecretKey": nodeJSON(base64.StdEncoding.EncodeToString(adv)), "processedHistoryMessages": nodeJSON([]any{}), "nextPreKeyId": nodeJSON(1), "firstUnuploadedPreKeyId": nodeJSON(1), "accountSyncCounter": nodeJSON(0), "accountSettings": nodeJSON(map[string]bool{"unarchiveChats": false}), "registered": nodeJSON(false)}, nil
}
func DeviceFromNodeCredentials(creds nodeObject) (*store.Device, error) {
	pair := func(raw json.RawMessage) (*keys.KeyPair, error) {
		o := nodeObj(raw)
		private, public := nodeBytes(o["private"]), nodeBytes(o["public"])
		if len(private) != 32 || len(public) != 32 {
			return nil, errors.New("old WhatsApp credential keypair must contain32byteprivate/public")
		}
		p := keys.NewKeyPairFromPrivateKey(*(*[32]byte)(private))
		if base64.StdEncoding.EncodeToString(p.Pub[:]) != base64.StdEncoding.EncodeToString(public) {
			return nil, errors.New("old WhatsApp credential public/private mismatch")
		}
		return p, nil
	}
	noise, e := pair(creds["noiseKey"])
	if e != nil {
		return nil, e
	}
	identity, e := pair(creds["signedIdentityKey"])
	if e != nil {
		return nil, e
	}
	signed := nodeObj(creds["signedPreKey"])
	pre, e := pair(signed["keyPair"])
	if e != nil {
		return nil, e
	}
	signature := nodeBytes(signed["signature"])
	if len(signature) != 64 {
		return nil, errors.New("old WhatsApp signed prekey requires64byte signature")
	}
	device := &store.Device{NoiseKey: noise, IdentityKey: identity, SignedPreKey: &keys.PreKey{KeyPair: *pre, KeyID: uint32(nodeNumber(signed["keyId"])), Signature: (*[64]byte)(signature)}, RegistrationID: uint32(nodeNumber(creds["registrationId"])), AdvSecretKey: nodeBytes(creds["advSecretKey"]), Platform: nodeString(creds["platform"])}
	me := nodeObj(creds["me"])
	if id := nodeString(me["id"]); id != "" {
		jid, e := types.ParseJID(id)
		if e != nil {
			return nil, fmt.Errorf("old WhatsApp me.id: %w", e)
		}
		device.ID = &jid
		device.PushName = nodeString(me["name"])
	}
	if lid := nodeString(me["lid"]); lid != "" {
		device.LID, e = types.ParseJID(lid)
		if e != nil {
			return nil, e
		}
	}
	account := nodeObj(creds["account"])
	if len(account) > 0 {
		device.Account = &waAdv.ADVSignedDeviceIdentity{Details: nodeBytes(account["details"]), AccountSignatureKey: nodeBytes(account["accountSignatureKey"]), AccountSignature: nodeBytes(account["accountSignature"]), DeviceSignature: nodeBytes(account["deviceSignature"])}
	}
	return device, nil
}
