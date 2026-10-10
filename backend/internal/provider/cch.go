/*
Claude Code CCH signing logic based on sub2api, ported from
src/utils/claudeCodeCch.ts.
Reference: https://github.com/Wei-Shaw/sub2api
License: LGPL-3.0-or-later, Copyright (c) 2026 Wesley Liddick.
*/
package provider

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/bits"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const billingPrefix = "x-anthropic-billing-header:"
const cchPlaceholder = "cch=00000;"
const cchSeed uint64 = 0x6E52736AC806831E

var cchPlaceholderPattern = regexp.MustCompile(`\bcch=00000;`)

// SignClaudeCodeCCHInString preserves every original body byte except the one
// recognized billing-field literal. Its checksum is over the unsigned body.
func SignClaudeCodeCCHInString(body string) string {
	if !strings.Contains(body, billingPrefix) || !strings.Contains(body, cchPlaceholder) || !json.Valid([]byte(body)) {
		return body
	}
	if len(cchPlaceholderPattern.FindAllStringIndex(body, -1)) != 1 {
		return body
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &object); err != nil {
		return body
	}
	system := bytes.TrimSpace(object["system"])
	stringSystem := len(system) > 0 && system[0] == '"'
	var blocks []json.RawMessage
	if stringSystem {
		blocks = []json.RawMessage{system}
	} else if len(system) > 0 && system[0] == '[' {
		_ = json.Unmarshal(system, &blocks)
	}
	var billingFields []json.RawMessage
	for _, block := range blocks {
		var text json.RawMessage
		if stringSystem {
			text = block
		} else {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(block, &fields)
			var blockType string
			_ = json.Unmarshal(fields["type"], &blockType)
			if blockType != "text" {
				continue
			}
			text = fields["text"]
		}
		var decoded string
		if json.Unmarshal(text, &decoded) == nil && strings.HasPrefix(decoded, billingPrefix) {
			billingFields = append(billingFields, text)
		}
	}
	count := 0
	var selected []byte
	for _, field := range billingFields {
		// JSON.stringify of this string retains lone UTF-16 code units. Do not
		// rebuild its literal by marshaling a replacement-character Go string.
		literal, _ := protocol.ParseAndStringifyJavaScriptJSON(field)
		var decoded string
		_ = json.Unmarshal(field, &decoded)
		matches := len(cchPlaceholderPattern.FindAllStringIndex(decoded, -1))
		count += matches
		if matches == 1 {
			selected = literal
		}
	}
	if count != 1 || strings.Count(body, string(selected)) != 1 {
		return body
	}
	hash := fmt.Sprintf("%05x", xxHash64Seeded([]byte(body), cchSeed)&0xfffff)
	signed := cchPlaceholderPattern.ReplaceAllString(string(selected), "cch="+hash+";")
	return strings.Replace(body, string(selected), signed, 1)
}

// SignClaudeCodeCCHBody matches the source Uint8Array path: unchanged signing
// returns the same bytes; changed signing returns TextDecoder/Encoder UTF-8.
func SignClaudeCodeCCHBody(body []byte) []byte {
	decoded := decodeTextDecoder(body)
	signed := SignClaudeCodeCCHInString(decoded)
	if signed == decoded {
		return body
	}
	return []byte(signed)
}

func decodeTextDecoder(body []byte) string {
	body = bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf})
	return string(protocol.DecodeJavaScriptUTF8(body))
}

func xxHash64Seeded(data []byte, seed uint64) uint64 {
	const p1 uint64 = 11400714785074694791
	const p2 uint64 = 14029467366897019727
	const p3 uint64 = 1609587929392839161
	const p4 uint64 = 9650029242287828579
	const p5 uint64 = 2870177450012600261
	round := func(acc, input uint64) uint64 { return bits.RotateLeft64(acc+input*p2, 31) * p1 }
	merge := func(acc, value uint64) uint64 { return (acc^round(0, value))*p1 + p4 }
	offset := 0
	var hash uint64
	if len(data) >= 32 {
		v1, v2, v3, v4 := seed+p1+p2, seed+p2, seed, seed-p1
		for offset+32 <= len(data) {
			v1 = round(v1, binary.LittleEndian.Uint64(data[offset:]))
			v2 = round(v2, binary.LittleEndian.Uint64(data[offset+8:]))
			v3 = round(v3, binary.LittleEndian.Uint64(data[offset+16:]))
			v4 = round(v4, binary.LittleEndian.Uint64(data[offset+24:]))
			offset += 32
		}
		hash = bits.RotateLeft64(v1, 1) + bits.RotateLeft64(v2, 7) + bits.RotateLeft64(v3, 12) + bits.RotateLeft64(v4, 18)
		hash = merge(merge(merge(merge(hash, v1), v2), v3), v4)
	} else {
		hash = seed + p5
	}
	hash += uint64(len(data))
	for offset+8 <= len(data) {
		hash ^= round(0, binary.LittleEndian.Uint64(data[offset:]))
		hash = bits.RotateLeft64(hash, 27)*p1 + p4
		offset += 8
	}
	if offset+4 <= len(data) {
		hash ^= uint64(binary.LittleEndian.Uint32(data[offset:])) * p1
		hash = bits.RotateLeft64(hash, 23)*p2 + p3
		offset += 4
	}
	for offset < len(data) {
		hash ^= uint64(data[offset]) * p5
		hash = bits.RotateLeft64(hash, 11) * p1
		offset++
	}
	hash ^= hash >> 33
	hash *= p2
	hash ^= hash >> 29
	hash *= p3
	hash ^= hash >> 32
	return hash
}
