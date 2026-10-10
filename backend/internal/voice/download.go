package voice

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type DownloadError struct{ Failure Failure }

func (e *DownloadError) Error() string { return e.Failure.Message }

type DownloadProgress struct {
	CompletedBytes, TotalBytes int64
	ResumedFromBytes           *int64
	Source                     string
}
type DownloadOptions struct {
	Client                                                           func(context.Context, string) (*http.Client, error)
	MaxRetries                                                       *int
	Backoff, MaxBackoff, ProbeTimeout, IdleTimeout, ProgressInterval *time.Duration
	Sleep                                                            func(context.Context, time.Duration) error
	Now                                                              func() time.Time
}
type DownloadResult struct {
	Source           string
	ResumedFromBytes *int64
}

type resolvedDownloadOptions struct {
	Client                                                           func(context.Context, string) (*http.Client, error)
	MaxRetries                                                       int
	Backoff, MaxBackoff, ProbeTimeout, IdleTimeout, ProgressInterval time.Duration
	Sleep                                                            func(context.Context, time.Duration) error
	Now                                                              func() time.Time
}

func NewDownloadOptions(input DownloadOptions) resolvedDownloadOptions {
	o := resolvedDownloadOptions{Client: input.Client, MaxRetries: 5, Backoff: duration(input.Backoff, 500*time.Millisecond), MaxBackoff: duration(input.MaxBackoff, 10*time.Second), ProbeTimeout: duration(input.ProbeTimeout, 3*time.Second), IdleTimeout: duration(input.IdleTimeout, 30*time.Second), ProgressInterval: duration(input.ProgressInterval, 100*time.Millisecond), Sleep: input.Sleep, Now: input.Now}
	if input.MaxRetries != nil {
		o.MaxRetries = *input.MaxRetries
	}
	if o.Client == nil {
		o.Client = func(context.Context, string) (*http.Client, error) {
			return &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true}}, nil
		}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = func(ctx context.Context, d time.Duration) error {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-timer.C:
				return nil
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}
	}
	return o
}

func Origin(raw string) string {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme == "" || u.Host == "" {
		return "invalid-url"
	}
	return u.Scheme + "://" + u.Host
}

var urlInError = regexp.MustCompile(`https?://[^\s"')]+`)

func ScrubURLs(message string) string {
	return urlInError.ReplaceAllStringFunc(message, func(raw string) string {
		u, e := url.Parse(raw)
		if e != nil {
			return "url"
		}
		return Origin(raw) + u.EscapedPath()
	})
}
func ClassifyError(e error) string {
	var own *DownloadError
	if errors.As(e, &own) {
		return own.Failure.Reason
	}
	var path *os.PathError
	if errors.As(e, &path) {
		return "storage"
	}
	var dns *net.DNSError
	if errors.As(e, &dns) {
		return "dns"
	}
	var certificate x509.UnknownAuthorityError
	if errors.As(e, &certificate) {
		return "certificate"
	}
	var host x509.HostnameError
	if errors.As(e, &host) {
		return "certificate"
	}
	var transport net.Error
	if errors.As(e, &transport) && transport.Timeout() {
		return "timeout"
	}
	text := e.Error()
	if certificatePattern.MatchString(text) {
		return "certificate"
	}
	if timeoutPattern.MatchString(text) {
		return "timeout"
	}
	if networkPattern.MatchString(text) {
		return "network"
	}
	return "unknown"
}

var certificatePattern = regexp.MustCompile(`(?i)CERT_|CERTIFICATE|UNABLE_TO_VERIFY|SELF.SIGNED|ERR_TLS|SSL|UnknownIssuer|InvalidCertificate|UNABLE_TO_GET_ISSUER`)
var timeoutPattern = regexp.MustCompile(`(?i)timed? ?out`)
var networkPattern = regexp.MustCompile(`(?i)fetch failed|socket|connection|network|closed|terminated|unable to connect|ECONN|EPIPE|unexpected EOF`)

func downloadFailure(e error, source, resource string) error {
	var own *DownloadError
	if errors.As(e, &own) {
		return e
	}
	return &DownloadError{Failure{Reason: ClassifyError(e), Source: source, Resource: resource, Message: ScrubURLs(e.Error())}}
}
func diskFailure(e error, resource string) error {
	return &DownloadError{Failure{Reason: "storage", Resource: resource, Message: ScrubURLs(e.Error())}}
}
func newHash(kind string) hash.Hash {
	if kind == "sha512" {
		return sha512.New()
	}
	return sha256.New()
}
func digestMatches(h hash.Hash, expected DownloadHash) bool {
	sum := h.Sum(nil)
	if expected.Encoding == "base64" {
		return base64.StdEncoding.EncodeToString(sum) == expected.Value
	}
	return hex.EncodeToString(sum) == strings.ToLower(expected.Value)
}
func prefixHash(path string, length int64, h hash.Hash) error {
	file, e := os.Open(path)
	if e != nil {
		return e
	}
	defer file.Close()
	_, e = io.CopyN(h, file, length)
	return e
}
func partSize(path, resource string) (int64, error) {
	stat, e := os.Stat(path)
	if errors.Is(e, os.ErrNotExist) {
		return 0, nil
	}
	if e != nil {
		return 0, diskFailure(e, resource)
	}
	return stat.Size(), nil
}

var contentRange = regexp.MustCompile(`^bytes (\d+)-(\d+)/(\d+|\*)$`)

func validRange(value string, offset, total int64) bool {
	match := contentRange.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return false
	}
	start, e1 := strconv.ParseInt(match[1], 10, 64)
	end, e2 := strconv.ParseInt(match[2], 10, 64)
	size, e3 := strconv.ParseInt(match[3], 10, 64)
	return e1 == nil && e2 == nil && start == offset && end == total-1 && (match[3] == "*" || e3 == nil && size == total)
}
func orderSources(ctx context.Context, urls []string, o resolvedDownloadOptions) ([]string, error) {
	if len(urls) < 2 {
		return urls, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, o.ProbeTimeout)
	defer cancel()
	type result struct {
		url string
		ok  bool
	}
	results := make(chan result, len(urls))
	for _, raw := range urls {
		go func(raw string) {
			client, e := o.Client(probeCtx, raw)
			if e != nil {
				results <- result{}
				return
			}
			req, e := http.NewRequestWithContext(probeCtx, "HEAD", raw, nil)
			if e != nil {
				results <- result{}
				return
			}
			req.Close = true
			res, e := client.Do(req)
			if e != nil {
				results <- result{}
				return
			}
			_ = res.Body.Close()
			results <- result{raw, res.StatusCode < 400}
		}(raw)
	}
	for range urls {
		select {
		case result := <-results:
			if result.ok {
				ordered := []string{result.url}
				for _, raw := range urls {
					if raw != result.url {
						ordered = append(ordered, raw)
					}
				}
				return ordered, nil
			}
		case <-probeCtx.Done():
			if ctx.Err() != nil {
				return nil, context.Cause(ctx)
			}
			return urls, nil
		}
	}
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	return urls, nil
}
func DownloadAsset(ctx context.Context, asset Asset, destination string, options DownloadOptions, progress func(DownloadProgress)) (DownloadResult, error) {
	o := NewDownloadOptions(options)
	resource := asset.Name
	part := destination + ".part"
	if e := os.MkdirAll(filepath.Dir(destination), 0755); e != nil {
		return DownloadResult{}, diskFailure(e, resource)
	}
	var resumed *int64
	var lastProgress time.Time
	report := func(completed int64, source string, force bool) {
		if progress == nil {
			return
		}
		now := o.Now()
		if !force && now.Sub(lastProgress) < o.ProgressInterval {
			return
		}
		lastProgress = now
		progress(DownloadProgress{completed, asset.Bytes, resumed, source})
	}
	transfer := func(raw string, allowResume bool) (DownloadResult, bool, error) {
		source := Origin(raw)
		offset := int64(0)
		var e error
		if allowResume {
			offset, e = partSize(part, resource)
			if e != nil {
				return DownloadResult{}, false, e
			}
		} else {
			_ = os.Remove(part)
		}
		if offset > asset.Bytes {
			_ = os.Remove(part)
			offset = 0
		}
		h := newHash(asset.Hash.Algorithm)
		if offset == asset.Bytes {
			if e = prefixHash(part, offset, h); e != nil {
				return DownloadResult{}, false, diskFailure(e, resource)
			}
			if !digestMatches(h, asset.Hash) {
				_ = os.Remove(part)
				return DownloadResult{}, true, nil
			}
			if e = os.Rename(part, destination); e != nil {
				return DownloadResult{}, false, diskFailure(e, resource)
			}
			resumed = ptr(offset)
			report(asset.Bytes, source, true)
			return DownloadResult{source, resumed}, false, nil
		}
		attemptCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		stalled := make(chan struct{})
		var stallOnce sync.Once
		timer := time.AfterFunc(o.IdleTimeout, func() { stallOnce.Do(func() { close(stalled); cancel() }) })
		defer timer.Stop()
		client, e := o.Client(attemptCtx, raw)
		if e != nil {
			return DownloadResult{}, false, downloadFailure(e, source, resource)
		}
		req, e := http.NewRequestWithContext(attemptCtx, "GET", raw, nil)
		if e != nil {
			return DownloadResult{}, false, downloadFailure(e, source, resource)
		}
		req.Close = true
		req.Header.Set("Accept-Encoding", "identity")
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}
		response, e := client.Do(req)
		if e != nil {
			if ctx.Err() != nil {
				return DownloadResult{}, false, context.Cause(ctx)
			}
			select {
			case <-stalled:
				return DownloadResult{}, false, &DownloadError{Failure{Reason: "timeout", Source: source, Resource: resource, Message: "No data received before the timeout"}}
			default:
				return DownloadResult{}, false, downloadFailure(e, source, resource)
			}
		}
		defer response.Body.Close()
		resuming := false
		switch response.StatusCode {
		case 416:
			if offset == 0 {
				return DownloadResult{}, false, &DownloadError{Failure{Reason: "http", Source: source, Resource: resource, Status: statusPtr(416), Message: "HTTP 416 for a full download"}}
			}
			_ = os.Remove(part)
			return DownloadResult{}, true, nil
		case 206:
			if offset <= 0 || !validRange(response.Header.Get("Content-Range"), offset, asset.Bytes) {
				_ = os.Remove(part)
				return DownloadResult{}, true, nil
			}
			resuming = true
		case 200:
			if length, ok := response.Header["Content-Length"]; ok {
				number, e := strconv.ParseInt(strings.Join(length, ", "), 10, 64)
				if e != nil || number != asset.Bytes {
					return DownloadResult{}, false, &DownloadError{Failure{Reason: "integrity", Source: source, Resource: resource, Message: fmt.Sprintf("Server reported %s bytes, expected %d", strings.Join(length, ", "), asset.Bytes)}}
				}
			}
			offset = 0
		default:
			return DownloadResult{}, false, &DownloadError{Failure{Reason: "http", Source: source, Resource: resource, Status: statusPtr(response.StatusCode), Message: fmt.Sprintf("HTTP %d from %s", response.StatusCode, source)}}
		}
		flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
		if resuming {
			if e = prefixHash(part, offset, h); e != nil {
				return DownloadResult{}, false, diskFailure(e, resource)
			}
			resumed = ptr(offset)
			flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
		}
		file, e := os.OpenFile(part, flags, 0666)
		if e != nil {
			return DownloadResult{}, false, diskFailure(e, resource)
		}
		defer file.Close()
		completed := offset
		report(completed, source, true)
		chunkStart := completed
		buffer := make([]byte, 64*1024)
		for {
			n, readError := response.Body.Read(buffer)
			if n > 0 {
				timer.Reset(o.IdleTimeout)
				if completed+int64(n) > asset.Bytes {
					_ = os.Remove(part)
					return DownloadResult{}, false, &DownloadError{Failure{Reason: "integrity", Source: source, Resource: resource, Message: fmt.Sprintf("Received more than the expected %d bytes", asset.Bytes)}}
				}
				_, _ = h.Write(buffer[:n])
				chunkStart = completed
				written := 0
				for written < n {
					count, err := file.Write(buffer[written:n])
					if err != nil {
						_ = file.Truncate(chunkStart)
						return DownloadResult{}, false, diskFailure(err, resource)
					}
					if count == 0 {
						_ = file.Truncate(chunkStart)
						return DownloadResult{}, false, diskFailure(io.ErrShortWrite, resource)
					}
					written += count
				}
				completed += int64(n)
				report(completed, source, false)
			}
			if readError == io.EOF {
				break
			}
			if readError != nil {
				if ctx.Err() != nil {
					return DownloadResult{}, false, context.Cause(ctx)
				}
				_ = file.Truncate(chunkStart)
				select {
				case <-stalled:
					return DownloadResult{}, false, &DownloadError{Failure{Reason: "timeout", Source: source, Resource: resource, Message: "Transfer stalled"}}
				default:
					return DownloadResult{}, false, downloadFailure(readError, source, resource)
				}
			}
		}
		if completed < asset.Bytes {
			return DownloadResult{}, false, &DownloadError{Failure{Reason: "network", Source: source, Resource: resource, Message: fmt.Sprintf("Connection closed after %d of %d bytes", completed, asset.Bytes)}}
		}
		if e = file.Close(); e != nil {
			return DownloadResult{}, false, diskFailure(e, resource)
		}
		if !digestMatches(h, asset.Hash) {
			_ = os.Remove(part)
			if resuming {
				return DownloadResult{}, true, nil
			}
			return DownloadResult{}, false, &DownloadError{Failure{Reason: "integrity", Source: source, Resource: resource, Message: asset.Hash.Algorithm + " mismatch for " + asset.Name}}
		}
		if e = os.Rename(part, destination); e != nil {
			return DownloadResult{}, false, diskFailure(e, resource)
		}
		report(asset.Bytes, source, true)
		return DownloadResult{source, resumed}, false, nil
	}
	ordered, e := orderSources(ctx, asset.URLs, o)
	if e != nil {
		return DownloadResult{}, e
	}
	var last error
	for _, raw := range ordered {
		for retry := 0; ; retry++ {
			result, restart, e := transfer(raw, true)
			if restart {
				result, restart, e = transfer(raw, false)
				if restart {
					e = &DownloadError{Failure{Reason: "integrity", Source: Origin(raw), Resource: resource, Message: asset.Hash.Algorithm + " mismatch for " + asset.Name}}
				}
			}
			if e == nil {
				return result, nil
			}
			if ctx.Err() != nil {
				return DownloadResult{}, context.Cause(ctx)
			}
			last = downloadFailure(e, Origin(raw), resource)
			reason := ClassifyError(last)
			if reason == "storage" || reason == "unknown" || reason == "unsupported-platform" {
				return DownloadResult{}, last
			}
			if (reason != "network" && reason != "timeout") || retry >= o.MaxRetries {
				break
			}
			delay := o.Backoff
			for i := 0; i < retry && delay < o.MaxBackoff; i++ {
				delay *= 2
			}
			if delay > o.MaxBackoff {
				delay = o.MaxBackoff
			}
			if e = o.Sleep(ctx, delay); e != nil {
				return DownloadResult{}, e
			}
		}
	}
	if last == nil {
		last = &DownloadError{Failure{Reason: "unknown", Resource: resource, Message: "No download source configured"}}
	}
	return DownloadResult{}, last
}
func statusPtr(n int) *int { return &n }
