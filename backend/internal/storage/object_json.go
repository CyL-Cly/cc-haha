package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// UnmarshalJSON retains JavaScript UTF-16 property names without changing the
// map's ordinary ASCII/Unicode access or interpreting its RawMessage values.
// Lone code units use WTF-8 only inside Go keys; MarshalJSON restores standard
// JSON string escapes, so persistence has no new format or reserved key prefix.
func (object *Object) UnmarshalJSON(raw []byte) error {
	if object == nil {
		return errors.New("cannot decode storage.Object into a nil receiver")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil {
		return err
	}
	if first != nil && first != json.Delim('{') {
		kind := "array"
		switch first.(type) {
		case string:
			kind = "string"
		case bool:
			kind = "bool"
		case json.Number:
			kind = "number"
		}
		return &json.UnmarshalTypeError{Value: kind, Type: reflect.TypeOf(Object{}), Offset: decoder.InputOffset()}
	}
	var parsed Object
	if first != nil {
		parsed = Object{}
		for decoder.More() {
			start := decoder.InputOffset()
			if _, err := decoder.Token(); err != nil {
				return err
			}
			end := decoder.InputOffset()
			keySource := raw[start:end]
			keySource = keySource[bytes.IndexByte(keySource, '"'):]
			// Token has validated this exact quoted key, including escapes.
			units, _ := protocol.DecodeJavaScriptJSONString(keySource)
			var value json.RawMessage
			if err := decoder.Decode(&value); err != nil {
				return err
			}
			parsed[objectKeyFromUTF16(units)] = value
		}
		if _, err := decoder.Token(); err != nil {
			return err
		}
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("storage.Object cannot contain a trailing JSON value")
	}
	if parsed == nil {
		*object = nil
	} else {
		// Retain encoding/json's existing-map merge behavior. A failed parse
		// commits nothing, and equivalent escaped keys retain the last value.
		if *object == nil {
			*object = Object{}
		}
		for key, value := range parsed {
			(*object)[key] = value
		}
	}
	return nil
}

func (object Object) MarshalJSON() ([]byte, error) {
	if object == nil {
		return []byte("null"), nil
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	output := []byte{'{'}
	for index, key := range keys {
		if index > 0 {
			output = append(output, ',')
		}
		output = append(output, protocol.QuoteJavaScriptUTF16(objectKeyUTF16(key))...)
		output = append(output, ':')
		value := object[key]
		if value == nil {
			output = append(output, "null"...)
		} else {
			if !json.Valid(value) {
				return nil, fmt.Errorf("storage.Object field %q contains invalid JSON", key)
			}
			output = append(output, value...)
		}
	}
	return append(output, '}'), nil
}

func objectKeyFromUTF16(units []uint16) string {
	var output []byte
	for index := 0; index < len(units); index++ {
		unit := units[index]
		if unit >= 0xd800 && unit <= 0xdbff && index+1 < len(units) && units[index+1] >= 0xdc00 && units[index+1] <= 0xdfff {
			output = utf8.AppendRune(output, utf16.DecodeRune(rune(unit), rune(units[index+1])))
			index++
		} else if unit >= 0xd800 && unit <= 0xdfff {
			output = append(output, byte(0xe0|unit>>12), byte(0x80|(unit>>6)&0x3f), byte(0x80|unit&0x3f))
		} else {
			output = utf8.AppendRune(output, rune(unit))
		}
	}
	return string(output)
}

func objectKeyUTF16(key string) []uint16 {
	var units []uint16
	for index := 0; index < len(key); {
		// WTF-8 permits precisely the surrogate triples rejected by UTF-8.
		if index+2 < len(key) && key[index] == 0xed && key[index+1] >= 0xa0 && key[index+1] <= 0xbf && key[index+2] >= 0x80 && key[index+2] <= 0xbf {
			unit := uint16(key[index]&0x0f)<<12 | uint16(key[index+1]&0x3f)<<6 | uint16(key[index+2]&0x3f)
			units = append(units, unit)
			index += 3
		} else {
			value, count := utf8.DecodeRuneInString(key[index:])
			units = append(units, utf16.Encode([]rune{value})...)
			index += count
		}
	}
	return units
}
