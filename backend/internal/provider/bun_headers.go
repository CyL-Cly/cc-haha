package provider

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// HeaderPair is an original constructor input or an original parsed wire row.
// Wire values must first be decoded from their Latin1 bytes, as Bun's Pico bridge
// does. A http.Header map is never a source of ordering for this representation.
type HeaderPair struct{ Name, Value string }
type BunHeaderError struct{ Message string }

func (failure *BunHeaderError) Error() string { return failure.Message }

var ErrBunHeaderMetadata = errors.New("original Bun header metadata is missing")

// BunHeaders retains the pinned Bun 1.3.14 native vectors, independently of the
// sorted WHATWG iterator. Mutations and snapshots are safe for concurrent hosts.
type BunHeaders struct {
	mu               sync.RWMutex
	common, uncommon []HeaderPair
	cookies          []string
}

func headerName(name string) (string, error) {
	if name == "" {
		return "", &BunHeaderError{Message: "Invalid header name: ''"}
	}
	for _, value := range []byte(name) {
		if !(value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(value))) {
			return "", &BunHeaderError{Message: "Invalid header name: '" + name + "'"}
		}
	}
	return strings.ToLower(name), nil
}

func headerValue(name, value string) (string, error) {
	original := value
	value = strings.Trim(value, " \t\r\n")
	if !utf8.ValidString(value) {
		return "", &BunHeaderError{Message: "Header '" + name + "' has invalid value: '" + original + "'"}
	}
	for _, character := range value {
		if character > 255 || character == 0 || character == '\r' || character == '\n' {
			return "", &BunHeaderError{Message: "Header '" + name + "' has invalid value: '" + original + "'"}
		}
	}
	return value, nil
}

func newBunHeaders(pairs []HeaderPair, fetch bool) (*BunHeaders, error) {
	headers := &BunHeaders{}
	for _, pair := range pairs {
		name, err := headerName(pair.Name)
		if err != nil {
			return nil, err
		}
		value, err := headerValue(pair.Name, pair.Value)
		if err != nil {
			return nil, err
		}
		if fetch && value == "" {
			continue
		}
		headers.put(name, value, fetch && !bunCommonHeader(name))
	}
	return headers, nil
}

func NewConstructorHeaders(pairs []HeaderPair) (*BunHeaders, error) {
	return newBunHeaders(pairs, false)
}
func NewFetchHeaders(pairs []HeaderPair) (*BunHeaders, error) { return newBunHeaders(pairs, true) }

func (headers *BunHeaders) put(name, value string, replace bool) {
	if name == "set-cookie" {
		if replace {
			headers.cookies = []string{value}
		} else {
			headers.cookies = append(headers.cookies, value)
		}
		return
	}
	vector := &headers.uncommon
	if bunCommonHeader(name) {
		vector = &headers.common
	}
	for index := range *vector {
		if (*vector)[index].Name == name {
			if replace {
				(*vector)[index].Value = value
			} else {
				separator := ", "
				if name == "cookie" {
					separator = "; "
				}
				(*vector)[index].Value += separator + value
			}
			return
		}
	}
	*vector = append(*vector, HeaderPair{name, value})
}

func (headers *BunHeaders) Set(name, value string) error { return headers.mutate(name, value, true) }
func (headers *BunHeaders) Append(name, value string) error {
	return headers.mutate(name, value, false)
}
func (headers *BunHeaders) mutate(name, value string, replace bool) error {
	if replace {
		value = strings.Trim(value, " \t\r\n")
	}
	normalized, err := headerName(name)
	if err != nil {
		return err
	}
	value, err = headerValue(name, value)
	if err != nil {
		return err
	}
	headers.mu.Lock()
	defer headers.mu.Unlock()
	headers.put(normalized, value, replace)
	return nil
}
func (headers *BunHeaders) Delete(name string) error {
	name, err := headerName(name)
	if err != nil {
		return err
	}
	headers.mu.Lock()
	defer headers.mu.Unlock()
	if name == "set-cookie" {
		headers.cookies = nil
		return nil
	}
	vector := &headers.uncommon
	if bunCommonHeader(name) {
		vector = &headers.common
	}
	for index, pair := range *vector {
		if pair.Name == name {
			*vector = append((*vector)[:index], (*vector)[index+1:]...)
			break
		}
	}
	return nil
}
func (headers *BunHeaders) Get(name string) (string, bool, error) {
	name, err := headerName(name)
	if err != nil {
		return "", false, err
	}
	headers.mu.RLock()
	defer headers.mu.RUnlock()
	if name == "set-cookie" {
		return strings.Join(headers.cookies, ", "), len(headers.cookies) > 0, nil
	}
	vector := headers.uncommon
	if bunCommonHeader(name) {
		vector = headers.common
	}
	for _, pair := range vector {
		if pair.Name == name {
			return pair.Value, true, nil
		}
	}
	return "", false, nil
}
func (headers *BunHeaders) Clone() *BunHeaders {
	if headers == nil {
		return nil
	}
	headers.mu.RLock()
	defer headers.mu.RUnlock()
	return &BunHeaders{common: append([]HeaderPair{}, headers.common...), uncommon: append([]HeaderPair{}, headers.uncommon...), cookies: append([]string{}, headers.cookies...)}
}
func (headers *BunHeaders) SetCookies() []string {
	headers.mu.RLock()
	defer headers.mu.RUnlock()
	return append([]string{}, headers.cookies...)
}
func (headers *BunHeaders) Iterator() []HeaderPair {
	snapshot := headers.Clone()
	pairs := append(snapshot.common, snapshot.uncommon...)
	sort.Slice(pairs, func(left, right int) bool { return pairs[left].Name < pairs[right].Name })
	for _, value := range snapshot.cookies {
		pairs = append(pairs, HeaderPair{"set-cookie", value})
	}
	return pairs
}
func headerJSON(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return protocol.StringifyValidatedJavaScriptJSON(raw)
}
func (headers *BunHeaders) ToJSON() (protocol.JSONObject, error) {
	if headers == nil {
		return protocol.JSONObject{}, ErrBunHeaderMetadata
	}
	snapshot := headers.Clone()
	object := protocol.NewJSONObject()
	for _, pair := range snapshot.common {
		object.Set(pair.Name, headerJSON(pair.Value))
	}
	if len(snapshot.cookies) > 0 {
		object.Set("set-cookie", headerJSON(snapshot.cookies))
	}
	for _, pair := range snapshot.uncommon {
		object.Set(pair.Name, headerJSON(pair.Value))
	}
	return object, nil
}
func (headers *BunHeaders) MarshalJSON() ([]byte, error) {
	object, err := headers.ToJSON()
	if err != nil {
		return nil, err
	}
	return object.MarshalJSON()
}
func (headers *BunHeaders) HTTPHeader() http.Header {
	result := make(http.Header)
	for _, pair := range headers.Iterator() {
		result.Add(pair.Name, pair.Value)
	}
	return result
}
func HeaderPairsFromWire(pairs [][2][]byte) ([]HeaderPair, error) {
	result := make([]HeaderPair, 0, len(pairs))
	for _, pair := range pairs {
		name, err := headerName(string(pair[0]))
		if err != nil {
			return nil, err
		}
		var value strings.Builder
		for _, character := range pair[1] {
			value.WriteRune(rune(character))
		}
		result = append(result, HeaderPair{name, value.String()})
	}
	return result, nil
}

var _ json.Marshaler = (*BunHeaders)(nil)
var _ error = (*BunHeaderError)(nil)

// HeaderValue observes the live original Headers object. Headers is retained as
// a legacy policy-map snapshot; it cannot represent mutation or enumeration.
func (failure *SDKStreamError) HeaderValue(name string) (string, bool, error) {
	if failure == nil {
		return "", false, nil
	}
	if failure.OriginalHeaders != nil {
		return failure.OriginalHeaders.Get(name)
	}
	normalized, err := headerName(name)
	if err != nil {
		return "", false, err
	}
	if values, exists := failure.Headers[http.CanonicalHeaderKey(normalized)]; exists {
		return strings.Join(values, ", "), true, nil
	}
	for key, values := range failure.Headers {
		if strings.EqualFold(key, normalized) {
			return strings.Join(values, ", "), true, nil
		}
	}
	return "", false, nil
}
