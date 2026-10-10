package imruntime

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/andybalholm/brotli"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func CollectWecomMediaCandidates(body protocol.JSONObject, nowMillis int64) ([]protocol.JSONObject, error) {
	seed := body.Get("msgid")
	if !truthy(seed) {
		seed = raw(fmt.Sprintf("%d", nowMillis))
	}
	seedJSON, err := protocol.JavaScriptStringJSON(seed)
	if err != nil {
		return nil, err
	}
	seedText := TextJSON(seedJSON)
	result := []protocol.JSONObject{}
	addImage := func(media protocol.JSONObject, seed Text) {
		if !truthy(media.Get("url")) {
			return
		}
		value := wecomJSONObject(wecomField("kind", "image"), wecomField("name", NewText("wecom-image-").Join(seed).Join(NewText(".jpg"))))
		value.Set("url", media.Get("url"))
		if v := media.Get("aeskey"); len(v) > 0 {
			value.Set("aesKey", v)
		}
		value.Set("mimeType", raw("image/jpeg"))
		result = append(result, value)
	}
	addImage(object(body.Get("image")), seedText)
	file := object(body.Get("file"))
	if truthy(file.Get("url")) {
		v := wecomJSONObject(wecomField("kind", "file"), wecomField("name", NewText("wecom-file-").Join(seedText)))
		v.Set("url", file.Get("url"))
		if key := file.Get("aeskey"); len(key) > 0 {
			v.Set("aesKey", key)
		}
		result = append(result, v)
	}
	items := object(body.Get("mixed")).Get("msg_item")
	if len(items) > 0 && string(items) != "null" {
		var list []json.RawMessage
		if json.Unmarshal(items, &list) != nil {
			return nil, errors.New("body.mixed?.msg_item?.forEach is not a function")
		}
		for i, item := range list {
			if string(item) == "null" {
				return nil, errors.New("null is not an object (evaluating 'item.msgtype')")
			}
			v := object(item)
			if v.String("msgtype") == "image" {
				addImage(object(v.Get("image")), seedText.Join(NewText(fmt.Sprintf("-%d", i))))
			}
		}
	}
	return result, nil
}

// The SDK deliberately disables the cipher's 16-byte unpad and validates a
// 32-byte PKCS#7 pad itself. IV is the first 16 bytes of the decoded key.
func DecryptWecomFile(encrypted []byte, keyText Text) ([]byte, error) {
	if len(encrypted) == 0 {
		return nil, errors.New("decryptFile: encryptedBuffer is empty or not provided")
	}
	if len(keyText) == 0 {
		return nil, errors.New("decryptFile: aesKey must be a non-empty string")
	}
	key := nodeBase64(keyText.String())
	wrap := func(err error) ([]byte, error) {
		return nil, fmt.Errorf("decryptFile: Decryption failed - %s. This may indicate corrupted data or an incorrect aesKey.", err)
	}
	if len(key) != 32 {
		return wrap(errors.New("Invalid key length"))
	}
	if len(encrypted)%aes.BlockSize != 0 {
		return wrap(errors.New("wrong final block length"))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return wrap(err)
	}
	data := make([]byte, len(encrypted))
	cipher.NewCBCDecrypter(block, key[:16]).CryptBlocks(data, encrypted)
	pad := int(data[len(data)-1])
	if pad < 1 || pad > 32 || pad > len(data) {
		return wrap(fmt.Errorf("Invalid PKCS#7 padding value: %d", pad))
	}
	for _, b := range data[len(data)-pad:] {
		if int(b) != pad {
			return wrap(errors.New("Invalid PKCS#7 padding: padding bytes mismatch"))
		}
	}
	return data[:len(data)-pad], nil
}

var wecomFilenameUTF8 = regexp.MustCompile(`(?i)filename\*=UTF-8''([^;\s]+)`)
var wecomFilenamePlain = regexp.MustCompile(`(?i)filename="?([^";\s]+)"?`)

func WecomDownloadFilename(disposition string) (*string, error) {
	match := wecomFilenameUTF8.FindStringSubmatch(disposition)
	if match == nil {
		match = wecomFilenamePlain.FindStringSubmatch(disposition)
	}
	if match == nil {
		return nil, nil
	}
	name, err := url.PathUnescape(match[1])
	if err != nil || !utf8.ValidString(name) {
		return nil, errors.New("URI error")
	}
	return &name, nil
}

type WecomDownload struct {
	Buffer   []byte
	Filename *string
}

func (c *WecomClient) DownloadFile(ctx context.Context, address string, key Text) (WecomDownload, error) {
	ctx, done, err := c.begin(ctx)
	if err != nil {
		return WecomDownload{}, err
	}
	defer done()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// On the pinned Bun node:http implementation the ClientRequest timeout ends
	// when response headers arrive. Body reads remain pending until data/close;
	// the client ownership context still cancels and joins them on Close.
	var timer *time.Timer
	var timedOut atomic.Bool
	if *c.o.RequestTimeout > 0 {
		timer = time.AfterFunc(*c.o.RequestTimeout, func() { timedOut.Store(true); cancel() })
		defer timer.Stop()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return WecomDownload{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	// WeCom SDK1.0.7's installed Axios1.13.6 producer supplies these headers.
	req.Header.Set("User-Agent", "axios/1.13.6")
	req.Header.Set("Accept-Encoding", "gzip, compress, deflate, br")
	response, err := c.HTTP.Do(req)
	if timer != nil {
		timer.Stop()
	}
	if err != nil {
		if timedOut.Load() {
			return WecomDownload{}, fmt.Errorf("timeout of %dms exceeded", c.o.RequestTimeout.Milliseconds())
		}
		return WecomDownload{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return WecomDownload{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return WecomDownload{}, fmt.Errorf("Request failed with status code %d", response.StatusCode)
	}
	data, err = wecomDecompress(data, response.Header.Get("Content-Encoding"))
	if err != nil {
		return WecomDownload{}, err
	}
	filename, err := WecomDownloadFilename(response.Header.Get("Content-Disposition"))
	if err != nil {
		return WecomDownload{}, err
	}
	if len(key) > 0 {
		data, err = DecryptWecomFile(data, key)
		if err != nil {
			return WecomDownload{}, err
		}
	}
	return WecomDownload{data, filename}, nil
}

func wecomDecompress(data []byte, encoding string) ([]byte, error) {
	var reader io.ReadCloser
	var err error
	switch strings.ToLower(encoding) {
	case "gzip", "x-gzip", "compress", "x-compress":
		if len(data) > 1 && data[0] == 0x1f && data[1] == 0x8b {
			reader, err = gzip.NewReader(bytes.NewReader(data))
		} else {
			reader, err = zlib.NewReader(bytes.NewReader(data))
		}
	case "deflate":
		if len(data) > 0 && data[0] == 0x78 {
			reader, err = zlib.NewReader(bytes.NewReader(data))
		} else {
			reader = flate.NewReader(bytes.NewReader(data))
		}
	case "br":
		return io.ReadAll(brotli.NewReader(bytes.NewReader(data)))
	default:
		return data, nil
	}
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}
func (c *WecomClient) DownloadCandidate(ctx context.Context, store *AttachmentStore, candidate protocol.JSONObject, session string) (LocalAttachment, error) {
	result, err := c.DownloadFile(ctx, candidate.String("url"), TextJSON(candidate.Get("aesKey")))
	if err != nil {
		return LocalAttachment{}, err
	}
	name := candidate.String("name")
	if result.Filename != nil && protocol.TrimJavaScriptString(*result.Filename) != "" {
		name = protocol.TrimJavaScriptString(*result.Filename)
	}
	mime := candidate.Get("mimeType")
	resolved := candidate.String("mimeType")
	if len(mime) == 0 || string(mime) == "null" {
		resolved = InferMIMEFromFilename(name)
		if resolved == "" {
			resolved = "application/octet-stream"
			if candidate.String("kind") == "image" {
				resolved = "image/jpeg"
			}
		}
	}
	target, err := store.ResolvePath("wecom", session, name)
	if err != nil {
		return LocalAttachment{}, err
	}
	if err = store.Write(target, result.Buffer); err != nil {
		return LocalAttachment{}, err
	}
	kind := "file"
	if strings.HasPrefix(strings.ToLower(resolved), "image/") {
		kind = "image"
	}
	return LocalAttachment{kind, name, target, resolved, int64(len(result.Buffer)), result.Buffer}, nil
}

// UploadMedia is the SDK's three-step WebSocket protocol, including its dynamic
// chunk worker count and 500/1000ms retry delays. No REST upload substitute.
func (c *WecomClient) UploadMedia(ctx context.Context, data []byte, kind, filename Text) (protocol.JSONObject, error) {
	ctx, done, err := c.begin(ctx)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	defer done()
	const chunkSize = 512 * 1024
	total := (len(data) + chunkSize - 1) / chunkSize
	if total > 100 {
		return protocol.JSONObject{}, fmt.Errorf("File too large: %d chunks exceeds maximum of 100 chunks (max ~50MB)", total)
	}
	sum := md5.Sum(data)
	init, err := c.SendReply(ctx, c.o.RequestID("aibot_upload_media_init"), wecomJSONObject(wecomField("type", kind), wecomField("filename", filename), wecomField("total_size", len(data)), wecomField("total_chunks", total), wecomField("md5", hex.EncodeToString(sum[:]))), "aibot_upload_media_init")
	if err != nil {
		return protocol.JSONObject{}, err
	}
	uploadID := object(init.Get("body")).Get("upload_id")
	if !truthy(uploadID) {
		return protocol.JSONObject{}, fmt.Errorf("Upload init failed: no upload_id returned. Response: %s", raw(init))
	}
	upload := func(index int) error {
		start := index * chunkSize
		end := min(start+chunkSize, len(data))
		chunk := data[start:end]
		var last error
		for attempt := 0; attempt <= 2; attempt++ {
			_, last = c.SendReply(ctx, c.o.RequestID("aibot_upload_media_chunk"), wecomJSONObject(protocol.JSONField{Name: "upload_id", Value: uploadID}, wecomField("chunk_index", index), wecomField("base64_data", base64.StdEncoding.EncodeToString(chunk))), "aibot_upload_media_chunk")
			if last == nil {
				return nil
			}
			if attempt < 2 {
				timer := time.NewTimer(time.Duration(attempt+1) * 500 * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}
		}
		return fmt.Errorf("Chunk %d upload failed after 3 attempts: %s", index, last)
	}
	if total <= 1 {
		if err = upload(0); err != nil {
			return protocol.JSONObject{}, err
		}
	} else {
		workers := 2
		if total <= 4 {
			workers = total
		} else if total <= 10 {
			workers = 3
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		next := 0
		failures := []error{}
		for range min(workers, total) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					mu.Lock()
					if next >= total {
						mu.Unlock()
						return
					}
					index := next
					next++
					mu.Unlock()
					if err := upload(index); err != nil {
						mu.Lock()
						failures = append(failures, err)
						mu.Unlock()
					}
				}
			}()
		}
		wg.Wait()
		if len(failures) > 0 {
			return protocol.JSONObject{}, fmt.Errorf("Upload failed: %d chunk(s) failed. First error: %s", len(failures), failures[0])
		}
	}
	finish, err := c.SendReply(ctx, c.o.RequestID("aibot_upload_media_finish"), wecomJSONObject(protocol.JSONField{Name: "upload_id", Value: uploadID}), "aibot_upload_media_finish")
	if err != nil {
		return protocol.JSONObject{}, err
	}
	body := object(finish.Get("body"))
	id := body.Get("media_id")
	if !truthy(id) {
		return protocol.JSONObject{}, fmt.Errorf("Upload finish failed: no media_id returned. Response: %s", raw(finish))
	}
	typ := body.Get("type")
	if len(typ) == 0 || string(typ) == "null" {
		typ = raw(kind)
	}
	created := body.Get("created_at")
	if len(created) == 0 || string(created) == "null" {
		created = raw(c.o.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
	}
	return wecomJSONObject(protocol.JSONField{Name: "type", Value: typ}, protocol.JSONField{Name: "media_id", Value: id}, protocol.JSONField{Name: "created_at", Value: created}), nil
}
