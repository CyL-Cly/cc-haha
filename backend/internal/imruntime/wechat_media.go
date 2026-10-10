package imruntime

import (
	"context"
	"crypto/aes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	whatwg "github.com/nlnwa/whatwg-url/url"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const WechatCDNBaseURL = "https://findermp.video.qq.com/251/20304/stodownload"

func wechatHex(v string) []byte {
	hex := func(c byte) int {
		if c >= '0' && c <= '9' {
			return int(c - '0')
		}
		if c >= 'a' && c <= 'f' {
			return int(c - 'a' + 10)
		}
		if c >= 'A' && c <= 'F' {
			return int(c - 'A' + 10)
		}
		return -1
	}
	out := []byte{}
	for i := 0; i+1 < len(v); i += 2 {
		a, b := hex(v[i]), hex(v[i+1])
		if a < 0 || b < 0 {
			break
		}
		out = append(out, byte(a*16+b))
	}
	return out
}
func wechatHexBase64(v string) string { return base64.StdEncoding.EncodeToString(wechatHex(v)) }

var wechatASCIIHex = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

func WechatAESKey(value string) ([]byte, error) {
	decoded := nodeBase64(value)
	if len(decoded) == 16 {
		return decoded, nil
	}
	ascii := append([]byte(nil), decoded...)
	for i := range ascii {
		ascii[i] &= 0x7f
	}
	if len(decoded) == 32 && wechatASCIIHex.Match(ascii) {
		return wechatHex(string(ascii)), nil
	}
	return nil, fmt.Errorf("WeChat AES key must decode to 16 bytes, got %d", len(decoded))
}
func WechatDecryptECB(encrypted, key []byte) ([]byte, error) {
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	if len(encrypted) == 0 || len(encrypted)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("wrong final block length")
	}
	plain := make([]byte, len(encrypted))
	for i := 0; i < len(encrypted); i += aes.BlockSize {
		block.Decrypt(plain[i:i+aes.BlockSize], encrypted[i:i+aes.BlockSize])
	}
	pad := int(plain[len(plain)-1])
	if pad < 1 || pad > aes.BlockSize || pad > len(plain) {
		return nil, fmt.Errorf("bad decrypt")
	}
	for _, b := range plain[len(plain)-pad:] {
		if int(b) != pad {
			return nil, fmt.Errorf("bad decrypt")
		}
	}
	return plain[:len(plain)-pad], nil
}

type WechatMediaOptions struct {
	HTTP        *http.Client
	Environment map[string]string
	Store       *AttachmentStore
	Now         func() time.Time
}
type WechatMedia struct {
	o      WechatMediaOptions
	HTTP   *http.Client
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	wg     sync.WaitGroup
	closed bool
	once   sync.Once
}
type WechatLocalAttachment struct {
	LocalAttachment
	NameJSON json.RawMessage
}

func NewWechatMedia(o WechatMediaOptions) (*WechatMedia, error) {
	if o.Store == nil {
		return nil, fmt.Errorf("WeChat media requires actual attachment store")
	}
	client, e := wechatHTTP(o.HTTP, o.Environment)
	if e != nil {
		return nil, e
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &WechatMedia{o: o, HTTP: client, ctx: ctx, cancel: cancel}, nil
}
func (m *WechatMedia) Close() {
	m.once.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.cancel()
		m.mu.Unlock()
		m.wg.Wait()
		m.HTTP.CloseIdleConnections()
	})
}
func (m *WechatMedia) DownloadCandidate(ctx context.Context, c WechatMediaCandidate, session string) (WechatLocalAttachment, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return WechatLocalAttachment{}, context.Canceled
	}
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.ctx, cancel)
	defer func() { stop(); cancel() }()
	target := c.URL
	if !wechatTruthy(target) {
		if wechatTruthy(c.EncryptQueryParam) {
			q, e := wechatString(c.EncryptQueryParam)
			if e != nil {
				return WechatLocalAttachment{}, e
			}
			target = raw(NewText(WechatCDNBaseURL + "?").Join(q))
		} else {
			return WechatLocalAttachment{}, fmt.Errorf("WeChat media item is missing a download URL")
		}
	}
	value, e := wechatString(target)
	if e != nil {
		return WechatLocalAttachment{}, e
	}
	u, e := whatwg.Parse(value.String())
	if e != nil {
		return WechatLocalAttachment{}, e
	}
	req, e := http.NewRequestWithContext(ctx, "GET", u.Href(false), nil)
	if e != nil {
		return WechatLocalAttachment{}, e
	}
	res, e := m.HTTP.Do(req)
	if e != nil {
		return WechatLocalAttachment{}, e
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		phrase := strings.TrimPrefix(res.Status, fmt.Sprint(res.StatusCode)+" ")
		return WechatLocalAttachment{}, fmt.Errorf("WeChat media download failed: %d %s", res.StatusCode, phrase)
	}
	data, e := io.ReadAll(res.Body)
	if e != nil {
		return WechatLocalAttachment{}, e
	}
	if wechatTruthy(c.AESKey) {
		keyText, e := wechatStringOnly(c.AESKey, "The first argument must be of type string or an instance of Buffer")
		if e != nil {
			return WechatLocalAttachment{}, e
		}
		key, e := WechatAESKey(keyText.String())
		if e != nil {
			return WechatLocalAttachment{}, e
		}
		data, e = WechatDecryptECB(data, key)
		if e != nil {
			return WechatLocalAttachment{}, e
		}
	}
	name, e := wechatStringOnly(c.Name, "name must be a string")
	if e != nil {
		return WechatLocalAttachment{}, e
	}
	path, e := m.o.Store.ResolvePath("wechat", session, name.String())
	if e != nil {
		return WechatLocalAttachment{}, e
	}
	if e = m.o.Store.Write(path, data); e != nil {
		return WechatLocalAttachment{}, e
	}
	mime := c.MIME
	if mime == "" {
		mime = "application/octet-stream"
		if c.Kind == "image" {
			mime = "image/jpeg"
		}
	}
	return WechatLocalAttachment{LocalAttachment{c.Kind, name.String(), path, mime, int64(len(data)), data}, append(json.RawMessage(nil), c.Name...)}, nil
}

// Collect is a deferred callback: the caller invokes it only after dedup,
// authorization and session restoration succeed. Results retain input order,
// matching Promise.allSettled; failed downloads never block successful peers.
func (m *WechatMedia) Collect(ctx context.Context, candidates []WechatMediaCandidate, session string, notice func(string) error) ([]protocol.JSONObject, error) {
	type result struct {
		local WechatLocalAttachment
		err   error
	}
	settled := make([]result, len(candidates))
	var wg sync.WaitGroup
	for i, c := range candidates {
		wg.Add(1)
		go func(i int, c WechatMediaCandidate) {
			defer wg.Done()
			settled[i].local, settled[i].err = m.DownloadCandidate(ctx, c, session)
		}(i, c)
	}
	wg.Wait()
	out := []protocol.JSONObject{}
	failures := 0
	for _, r := range settled {
		if r.err != nil {
			failures++
			continue
		}
		check := CheckAttachmentLimit(r.local.Kind, r.local.Size, r.local.MIME)
		if !check.OK {
			if notice != nil {
				if e := notice(check.Hint); e != nil {
					return nil, e
				}
			}
			continue
		}
		kind := r.local.Kind
		fields := []protocol.JSONField{wechatField("type", kind), {Name: "name", Value: r.local.NameJSON}}
		if kind == "image" {
			fields = append(fields, wechatField("data", base64.StdEncoding.EncodeToString(r.local.Buffer)))
		} else {
			fields = append(fields, wechatField("path", r.local.Path))
		}
		fields = append(fields, wechatField("mimeType", r.local.MIME))
		out = append(out, wechatFields(fields...))
	}
	if failures > 0 && notice != nil {
		hint := fmt.Sprintf("%d 个附件下载失败，已跳过。", failures)
		if failures == len(candidates) {
			hint = "附件下载失败，请稍后重试。"
		}
		if e := notice(hint); e != nil {
			return nil, e
		}
	}
	return out, nil
}
