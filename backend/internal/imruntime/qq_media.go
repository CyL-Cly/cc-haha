package imruntime

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	whatwg "github.com/nlnwa/whatwg-url/url"
)

type QQUploadDailyLimitError struct {
	FilePath string
	FileSize int
	Cause    error
}

func (e *QQUploadDailyLimitError) Error() string { return e.Cause.Error() }
func (c *QQClient) retryUpload(ctx context.Context, fn func() (json.RawMessage, error), base time.Duration, persistent time.Duration) (json.RawMessage, error) {
	var result json.RawMessage
	var err error
	for attempt := 0; attempt <= 2; attempt++ {
		result, err = fn()
		if err == nil {
			return result, nil
		}
		var api *QQAPIError
		if persistent > 0 && errors.As(err, &api) && string(api.BizCode) == "40093001" {
			deadline := c.o.Now().Add(persistent)
			for c.o.Now().Before(deadline) {
				result, err = fn()
				if err == nil {
					return result, nil
				}
				if !errors.As(err, &api) || string(api.BizCode) != "40093001" {
					return nil, err
				}
				remaining := deadline.Sub(c.o.Now())
				if remaining <= 0 {
					break
				}
				if sleepErr := c.sleep(ctx, min(time.Second, remaining)); sleepErr != nil {
					return nil, sleepErr
				}
			}
			return nil, err
		}
		if attempt < 2 {
			if e := c.sleep(ctx, base*time.Duration(1<<attempt)); e != nil {
				return nil, e
			}
		}
	}
	return nil, err
}
func (c *QQClient) uploadChunked(ctx context.Context, t QQTarget, data []byte) (protocol.JSONObject, error) {
	sum := md5.Sum(data)
	hash := fmt.Sprintf("%x", sum)
	if info := c.mediaCacheGet(hash, t); len(info) > 0 {
		return qqObject(qqField("file_uuid", ""), protocol.JSONField{Name: "file_info", Value: info}, qqField("ttl", 0)), nil
	}
	sha := sha1.Sum(data)
	md10 := sum
	if len(data) > 10002432 {
		md10 = md5.Sum(data[:10002432])
	}
	base := strings.TrimSuffix(qqMessagePath(t), "/messages")
	prepare, e := c.Request(ctx, "POST", base+"/upload_prepare", raw(qqObject(qqField("file_type", 1), qqField("file_name", "file"), qqField("file_size", len(data)), qqField("md5", hash), qqField("sha1", fmt.Sprintf("%x", sha)), qqField("md5_10m", fmt.Sprintf("%x", md10)))))
	if e != nil {
		var api *QQAPIError
		if errors.As(e, &api) && string(api.BizCode) == "40093002" {
			return protocol.JSONObject{}, &QQUploadDailyLimitError{"<buffer>", len(data), e}
		}
		return protocol.JSONObject{}, e
	}
	p := object(prepare)
	parts := collectJSONArray(p.Get("parts"))
	block := qqInt(p.Get("block_size"), 0)
	if block <= 0 {
		return protocol.JSONObject{}, errors.New("QQ chunk upload block_size is invalid")
	}
	parallel := qqInt(p.Get("concurrency"), 1)
	if parallel == 0 {
		parallel = 1
	}
	parallel = min(parallel, 10)
	if parallel < 1 {
		return protocol.JSONObject{}, errors.New("QQ chunk upload concurrency is invalid")
	}
	persistent := 2 * time.Minute
	if truthy(p.Get("retry_timeout")) {
		persistent = time.Duration(math.Min(qqNumber(p.Get("retry_timeout"))*1000, 600000)) * time.Millisecond
	}
	for start := 0; start < len(parts); start += parallel {
		var wg sync.WaitGroup
		failures := make(chan error, parallel)
		for _, part := range parts[start:min(start+parallel, len(parts))] {
			part := part
			wg.Add(1)
			go func() {
				defer wg.Done()
				index := qqInt(part.Get("index"), 0)
				offset := (index - 1) * block
				if offset < 0 || offset > len(data) {
					failures <- errors.New("QQ upload part offset invalid")
					return
				}
				buffer := data[offset:min(offset+block, len(data))]
				digest := md5.Sum(buffer)
				if e := c.putPart(ctx, TextJSON(part.Get("presigned_url")), buffer, index, len(parts)); e != nil {
					failures <- e
					return
				}
				_, e := c.retryUpload(ctx, func() (json.RawMessage, error) {
					return c.Request(ctx, "POST", base+"/upload_part_finish", raw(qqObject(protocol.JSONField{Name: "upload_id", Value: p.Get("upload_id")}, qqField("part_index", index), qqField("block_size", len(buffer)), qqField("md5", fmt.Sprintf("%x", digest)))))
				}, time.Second, persistent)
				if e != nil {
					failures <- e
				}
			}()
		}
		wg.Wait()
		close(failures)
		if e := <-failures; e != nil {
			return protocol.JSONObject{}, e
		}
	}
	result, e := c.retryUpload(ctx, func() (json.RawMessage, error) {
		return c.Request(ctx, "POST", base+"/files", raw(qqObject(protocol.JSONField{Name: "upload_id", Value: p.Get("upload_id")})))
	}, 2*time.Second, 0)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	uploaded := object(result)
	c.mediaCacheSet(hash, t, uploaded)
	return uploaded, nil
}
func (c *QQClient) putPart(ctx context.Context, address Text, data []byte, index, total int) error {
	u, e := whatwg.Parse(address.String())
	if e != nil || u.Protocol() != "https:" {
		return errors.New("QQ COS upload requires HTTPS URL")
	}
	var err error
	for attempt := 0; attempt <= 2; attempt++ {
		ctxPart, cancel := context.WithCancel(ctx)
		stop := c.o.Schedule(300*time.Second, cancel)
		req, e := http.NewRequestWithContext(ctxPart, "PUT", u.Href(false), bytes.NewReader(data))
		if e == nil {
			req.Header.Set("User-Agent", "Bun/1.3.14")
			res, requestErr := c.o.HTTP.Do(req)
			e = requestErr
			if e == nil {
				body, readErr := io.ReadAll(res.Body)
				res.Body.Close()
				e = readErr
				if e == nil && (res.StatusCode < 200 || res.StatusCode >= 300) {
					e = fmt.Errorf("COS PUT failed: %d %s - %s", res.StatusCode, http.StatusText(res.StatusCode), NewText(string(protocol.DecodeJavaScriptUTF8(body))).Slice(0, 120).String())
				}
			}
		}
		stop()
		cancel()
		if e == nil {
			return nil
		}
		err = e
		if errors.Is(e, context.Canceled) && ctx.Err() == nil {
			err = fmt.Errorf("Part %d/%d upload timeout after 300000ms", index, total)
		}
		if attempt < 2 {
			if e = c.sleep(ctx, time.Duration(1<<attempt)*time.Second); e != nil {
				return e
			}
		}
	}
	return err
}

var qqHasURLScheme = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*://`)

func NormalizeQQAttachmentURL(value Text) (string, bool) {
	value = value.Trim()
	if len(value) == 0 {
		return "", false
	}
	candidate := value.String()
	if !qqHasURLScheme.MatchString(candidate) {
		candidate = "https://" + candidate
	}
	u, e := whatwg.Parse(candidate)
	if e != nil || u.Username() != "" || u.Password() != "" || (u.Protocol() != "https:" && u.Protocol() != "http:") {
		return "", false
	}
	if u.Protocol() == "http:" {
		u.SetProtocol("https:")
	}
	return u.Href(false), true
}

type QQMedia struct {
	Store *AttachmentStore
	HTTP  *http.Client
}

func (m *QQMedia) Download(ctx context.Context, attachment protocol.JSONObject, session, seed string) (LocalAttachment, error) {
	address, ok := NormalizeQQAttachmentURL(TextJSON(attachment.Get("url")))
	if !ok {
		return LocalAttachment{}, errors.New("QQ attachment has no usable download URL")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, "GET", address, nil)
	if e != nil {
		return LocalAttachment{}, e
	}
	res, e := m.HTTP.Do(req)
	if e != nil {
		return LocalAttachment{}, e
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return LocalAttachment{}, fmt.Errorf("QQ attachment download failed: %d %s", res.StatusCode, http.StatusText(res.StatusCode))
	}
	data, e := io.ReadAll(res.Body)
	if e != nil {
		return LocalAttachment{}, e
	}
	mimePart := func(text string) string { return protocol.TrimJavaScriptString(strings.Split(text, ";")[0]) }
	mime := mimePart(attachment.String("content_type"))
	if mime == "" {
		mime = mimePart(res.Header.Get("Content-Type"))
	}
	if mime == "" {
		mime = InferMIMEFromFilename(attachment.String("filename"))
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	name := TextJSON(attachment.Get("filename")).Trim().String()
	if name == "" {
		name = "qq-file-" + seed
		if strings.HasPrefix(mime, "image/") {
			name = "qq-image-" + seed + "." + ImageExtensionForMIME(mime)
		}
	}
	path, e := m.Store.ResolvePath("qq", session, name)
	if e != nil {
		return LocalAttachment{}, e
	}
	if e = m.Store.Write(path, data); e != nil {
		return LocalAttachment{}, e
	}
	kind := "file"
	if strings.HasPrefix(mime, "image/") {
		kind = "image"
	}
	return LocalAttachment{kind, name, path, mime, int64(len(data)), data}, nil
}
