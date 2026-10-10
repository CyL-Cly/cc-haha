package imruntime

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

type WhatsAppMediaKeys struct{ IV, CipherKey, MACKey []byte }

func WhatsAppHKDFInfo(mediaType string) string {
	names := map[string]string{"audio": "Audio", "document": "Document", "gif": "Video", "image": "Image", "ppic": "", "product": "Image", "ptt": "Audio", "sticker": "Image", "video": "Video", "thumbnail-document": "Document Thumbnail", "thumbnail-image": "Image Thumbnail", "thumbnail-video": "Video Thumbnail", "thumbnail-link": "Link Thumbnail", "md-msg-hist": "History", "md-app-state": "App State", "product-catalog-image": "", "payment-bg-image": "Payment Background", "ptv": "Video", "biz-cover-photo": "Image"}
	name, ok := names[mediaType]
	if !ok {
		name = "undefined"
	}
	return "WhatsApp " + name + " Keys"
}
func WhatsAppDeriveMediaKeys(key []byte, mediaType string) (WhatsAppMediaKeys, error) {
	if key == nil {
		return WhatsAppMediaKeys{}, errors.New("Cannot derive from empty media key")
	}
	extract := hmac.New(sha256.New, make([]byte, sha256.Size))
	extract.Write(key)
	prk := extract.Sum(nil)
	var previous, expanded []byte
	for i := byte(1); len(expanded) < 112; i++ {
		h := hmac.New(sha256.New, prk)
		h.Write(previous)
		h.Write([]byte(WhatsAppHKDFInfo(mediaType)))
		h.Write([]byte{i})
		previous = h.Sum(nil)
		expanded = append(expanded, previous...)
	}
	return WhatsAppMediaKeys{append([]byte{}, expanded[:16]...), append([]byte{}, expanded[16:48]...), append([]byte{}, expanded[48:80]...)}, nil
}
func WhatsAppMediaKey(value json.RawMessage) ([]byte, error) {
	if len(value) == 0 || string(value) == "null" || string(value) == "false" {
		return nil, errors.New("Cannot derive from empty media key")
	}
	if value[0] == '"' {
		return nodeBase64(strings.Replace(TextJSON(value).String(), "data:;base64,", "", 1)), nil
	}
	o := object(value)
	if o.String("type") == "Buffer" {
		return WhatsAppBufferBytes(value)
	}
	var values []uint8
	if json.Unmarshal(value, &values) == nil {
		return values, nil
	}
	return nil, errors.New("WhatsApp media key requires a byte buffer or base64 string")
}
func EncryptWhatsAppMedia(data, key []byte, mediaType string) ([]byte, error) {
	keys, e := WhatsAppDeriveMediaKeys(key, mediaType)
	if e != nil {
		return nil, e
	}
	block, e := aes.NewCipher(keys.CipherKey)
	if e != nil {
		return nil, e
	}
	padding := aes.BlockSize - len(data)%aes.BlockSize
	plain := append([]byte{}, data...)
	for i := 0; i < padding; i++ {
		plain = append(plain, byte(padding))
	}
	encrypted := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, keys.IV).CryptBlocks(encrypted, plain)
	mac := hmac.New(sha256.New, keys.MACKey)
	mac.Write(keys.IV)
	mac.Write(encrypted)
	return append(encrypted, mac.Sum(nil)[:10]...), nil
}

// DecryptWhatsAppMedia follows the pinned Baileys downloader: whole AES blocks
// are decrypted and its trailing ten MAC bytes are excluded. That downloader
// does not authenticate the MAC; adding a new check would change its behavior.
func DecryptWhatsAppMedia(encrypted []byte, keys WhatsAppMediaKeys) ([]byte, error) {
	n := len(encrypted) / aes.BlockSize * aes.BlockSize
	if n == 0 {
		return nil, errors.New("Cannot read properties of undefined (reading 'final')")
	}
	block, e := aes.NewCipher(keys.CipherKey)
	if e != nil {
		return nil, e
	}
	if len(keys.IV) != aes.BlockSize {
		return nil, errors.New("Invalid initialization vector")
	}
	plain := make([]byte, n)
	cipher.NewCBCDecrypter(block, keys.IV).CryptBlocks(plain, encrypted[:n])
	padding := int(plain[n-1])
	if padding < 1 || padding > aes.BlockSize || padding > n {
		return nil, errors.New("WhatsApp AES-CBC bad decrypt")
	}
	for _, v := range plain[n-padding:] {
		if int(v) != padding {
			return nil, errors.New("WhatsApp AES-CBC bad decrypt")
		}
	}
	return plain[:n-padding], nil
}
func whatsAppBuffer(data []byte) json.RawMessage {
	return raw(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("Buffer")}, protocol.JSONField{Name: "data", Value: raw(base64.StdEncoding.EncodeToString(data))}))
}
