package clibootstrap

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	weburl "github.com/nlnwa/whatwg-url/url"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
)

type nativeManagedFetch struct {
	success, skipRetry bool
	settings           *protocol.JSONObject // nil is the actual 304 sentinel.
}

// sortKeysDeep uses UTF-16 ordering, then Object own-key enumeration just as
// Source's sortKeysDeep + JSON.stringify do. Arrays retain their original order.
func remoteManagedSortedJSON(raw json.RawMessage) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if !json.Valid(raw) {
		return nil, errors.New("invalid settings JSON")
	}
	if len(raw) > 0 && raw[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
		for i, item := range items {
			next, err := remoteManagedSortedJSON(item)
			if err != nil {
				return nil, err
			}
			items[i] = next
		}
		joined := []byte{'['}
		for i, item := range items {
			if i > 0 {
				joined = append(joined, ',')
			}
			joined = append(joined, item...)
		}
		return append(joined, ']'), nil
	}
	if len(raw) > 0 && raw[0] == '{' {
		var object protocol.JSONObject
		if err := object.UnmarshalJSON(raw); err != nil {
			return nil, err
		}
		properties := object.OwnProperties()
		sort.SliceStable(properties, func(i, j int) bool {
			left, _ := protocol.DecodeJavaScriptJSONString(properties[i].NameJSON)
			right, _ := protocol.DecodeJavaScriptJSONString(properties[j].NameJSON)
			for k := 0; k < len(left) && k < len(right); k++ {
				if left[k] != right[k] {
					return left[k] < right[k]
				}
			}
			return len(left) < len(right)
		})
		result := protocol.NewJSONObject()
		for _, property := range properties {
			value, err := remoteManagedSortedJSON(property.Value)
			if err != nil {
				return nil, err
			}
			if err = result.SetPropertyJSON(property.NameJSON, value); err != nil {
				return nil, err
			}
		}
		return result.MarshalJSON()
	}
	return protocol.StringifyValidatedJavaScriptJSON(raw), nil
}
func remoteManagedChecksum(settings *protocol.JSONObject) (string, error) {
	raw, err := settings.MarshalJSON()
	if err != nil {
		return "", err
	}
	canonical, err := remoteManagedSortedJSON(raw)
	if err != nil {
		return "", err
	}
	canonical = protocol.StringifyValidatedJavaScriptJSON(canonical)
	digest := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func (s *nativeRemoteManagedPolicy) fetchAttempt(checksum string) (nativeManagedFetch, error) {
	// This is the existing shared OAuth check/refresh producer, not a model call.
	// It refreshes before every attempted settings GET and keeps its own dedup.
	s.mu.Lock()
	refresh, client, endpoint, configurationError := s.refresh, s.client, s.endpoint, s.configurationError
	s.mu.Unlock()
	if configurationError != nil {
		return nativeManagedFetch{}, configurationError
	}
	if client == nil {
		return nativeManagedFetch{}, errors.New("remote settings transport was not initialized")
	}
	if refresh != nil {
		if err := refresh.check(s.life); err != nil {
			return nativeManagedFetch{}, err
		}
	}
	var credentials nativeManagedCredentials
	err := s.onOwner(s.life, func(scope *cooperative.Scope) error {
		var err error
		credentials, err = s.sample(scope, false)
		return err
	})
	if err != nil {
		return nativeManagedFetch{}, err
	}
	headers := http.Header{}
	if credentials.APIKey != "" {
		headers.Set("x-api-key", credentials.APIKey)
	} else if credentials.OAuth != nil && credentials.OAuth.String("accessToken") != "" {
		headers.Set("Authorization", "Bearer "+credentials.OAuth.String("accessToken"))
		headers.Set("anthropic-beta", "oauth-2025-04-20")
	} else {
		return nativeManagedFetch{skipRetry: true}, nil
	}
	ctx, cancel := context.WithTimeout(s.life, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nativeManagedFetch{}, err
	}
	request.Header = headers
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Accept-Encoding", "gzip, compress, deflate, br")
	request.Header.Set("User-Agent", "claude-code/"+provider.SourceCompatibilityVersion)
	if checksum != "" {
		request.Header.Set("If-None-Match", `"`+checksum+`"`)
	}
	response, err := client.Do(request)
	if err != nil {
		return nativeManagedFetch{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return nativeManagedFetch{skipRetry: true}, nil
	}
	// Even status-only responses own their body close; no arbitrary error-body
	// draining can extend an authentication failure beyond its real response.
	if response.StatusCode == 204 || response.StatusCode == 404 {
		empty := protocol.NewJSONObject()
		return nativeManagedFetch{success: true, settings: &empty}, nil
	}
	if response.StatusCode == 304 {
		return nativeManagedFetch{success: true}, nil
	}
	if response.StatusCode != 200 {
		return nativeManagedFetch{}, nil
	}
	raw, err := webfetch.ReadAxiosResponseBody(response)
	if err != nil {
		return nativeManagedFetch{}, err
	}
	raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	var envelope protocol.JSONObject
	if envelope.UnmarshalJSON(raw) != nil || !envelope.IsObject() {
		return nativeManagedFetch{}, nil
	}
	// Zod requires strings, including the empty string. String() alone would
	// incorrectly accept missing values as an empty string.
	for _, key := range []string{"uuid", "checksum"} {
		var value string
		if json.Unmarshal(envelope.Get(key), &value) != nil || bytes.Equal(envelope.Get(key), []byte("null")) {
			return nativeManagedFetch{}, nil
		}
	}
	rawSettings := envelope.Get("settings")
	var settings protocol.JSONObject
	if settings.UnmarshalJSON(rawSettings) != nil || !settings.IsObject() {
		return nativeManagedFetch{}, nil
	}
	parsed, err := nativeSettings(rawSettings, "remote managed settings")
	if err != nil || parsed == nil {
		return nativeManagedFetch{}, nil
	}
	return nativeManagedFetch{success: true, settings: parsed}, nil
}
func remoteManagedRetryDelay(attempt int) (time.Duration, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return 0, err
	}
	fraction := float64(binary.LittleEndian.Uint64(random[:])>>11) / float64(uint64(1)<<53)
	base := 500 * time.Millisecond * time.Duration(1<<uint(attempt-1))
	if base > 32*time.Second {
		base = 32 * time.Second
	}
	return base + time.Duration(fraction*.25*float64(base)), nil
}
func (s *nativeRemoteManagedPolicy) retryWait(delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-s.life.Done():
		return context.Cause(s.life)
	}
}
func (s *nativeRemoteManagedPolicy) fetchAndLoad(cached *protocol.JSONObject) (nativeManagedLoad, error) {
	checksum := ""
	if cached != nil {
		var err error
		checksum, err = remoteManagedChecksum(cached)
		if err != nil {
			return s.useStale(cached)
		}
	}
	var fetched nativeManagedFetch
	for attempt := 1; attempt <= 6; attempt++ {
		result, err := s.fetchAttempt(checksum)
		if fatal := s.liveError(err); fatal != nil {
			return nativeManagedLoad{}, fatal
		}
		if err == nil && result.success {
			fetched = result
			break
		}
		if result.skipRetry || attempt == 6 {
			return s.useStale(cached)
		}
		delay, err := remoteManagedRetryDelay(attempt)
		if err != nil {
			return s.useStale(cached)
		}
		if err = s.retryWait(delay); err != nil {
			return nativeManagedLoad{}, err
		}
	}
	if !fetched.success {
		return s.useStale(cached)
	}
	if fetched.settings == nil && cached != nil {
		return s.useStale(cached)
	}
	next := fetched.settings
	if next == nil {
		empty := protocol.NewJSONObject()
		next = &empty
	}
	if len(next.OwnProperties()) > 0 {
		approved, err := s.security(cached, next)
		if err != nil {
			if fatal := s.liveError(err); fatal != nil {
				return nativeManagedLoad{}, fatal
			}
			return s.useStale(cached)
		}
		if !approved {
			return nativeManagedLoad{cached}, nil
		}
	}
	if err := s.onOwner(s.life, func(*cooperative.Scope) error { s.mu.Lock(); s.session = next; s.mu.Unlock(); return nil }); err != nil {
		return nativeManagedLoad{}, err
	}
	path := filepath.Join(s.n.ConfigHomeDirectory(), "remote-settings.json")
	if len(next.OwnProperties()) == 0 {
		_ = s.removeCache(path)
	} else {
		_ = s.saveCache(path, next)
	}
	return nativeManagedLoad{next}, nil
}
func (s *nativeRemoteManagedPolicy) removeCache(path string) error {
	if err := s.life.Err(); err != nil {
		return context.Cause(s.life)
	}
	if err := s.n.check("remote managed policy unlink", path); err != nil {
		return err
	}
	return os.Remove(path)
}
func (s *nativeRemoteManagedPolicy) saveCache(path string, value *protocol.JSONObject) (err error) {
	if err = s.life.Err(); err != nil {
		return context.Cause(s.life)
	}
	if err = s.n.check("remote managed policy save", path); err != nil {
		return err
	}
	handle, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer func() {
		closeError := handle.Close()
		if err == nil {
			err = closeError
		}
	}()
	raw, err := value.MarshalJSON()
	if err != nil {
		return err
	}
	var pretty bytes.Buffer
	if err = json.Indent(&pretty, protocol.StringifyValidatedJavaScriptJSON(raw), "", "  "); err != nil {
		return err
	}
	if _, err = handle.Write(pretty.Bytes()); err != nil {
		return err
	}
	return handle.Sync()
}

func (s *nativeRemoteManagedPolicy) useStale(cached *protocol.JSONObject) (nativeManagedLoad, error) {
	if cached != nil {
		if err := s.onOwner(s.life, func(*cooperative.Scope) error { s.mu.Lock(); s.session = cached; s.mu.Unlock(); return nil }); err != nil {
			return nativeManagedLoad{}, err
		}
	}
	return nativeManagedLoad{cached}, nil
}

// Axios follow-redirects' GET policy: 21 hops and only its three sensitive
// headers are removed on protocol downgrade or a non-subdomain host change.
// x-api-key is deliberately not added to that Source list. Re-copying the
// previous headers prevents net/http from restoring an earlier stripped value.
func remoteManagedRedirect(request *http.Request, via []*http.Request) error {
	if len(via) > 21 {
		return errors.New("maximum redirects exceeded")
	}
	previous := via[len(via)-1]
	request.Header = previous.Header.Clone()
	if request.Method == http.MethodGet && previous.Method != http.MethodGet && previous.Method != http.MethodHead {
		for name := range request.Header {
			if strings.HasPrefix(strings.ToLower(name), "content-") {
				request.Header.Del(name)
			}
		}
	}
	target, err := weburl.Parse(request.URL.String())
	if err != nil {
		return err
	}
	origin, err := weburl.Parse(previous.URL.String())
	if err != nil {
		return err
	}
	host, current := target.Host(), origin.Host()
	subdomain := len(host) > len(current)+1 && strings.HasSuffix(host, "."+current)
	if target.Scheme() != origin.Scheme() && target.Scheme() != "https" || host != current && !subdomain {
		for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie"} {
			request.Header.Del(name)
		}
	}
	return nil
}
