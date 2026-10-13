package imruntime

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// This core supplies the existing server bridge and stores to platform ports.
// It does not own another agent or a platform transport.
type platformRuntimeCore struct {
	migration    *MigrationLifecycle
	Config       Config
	Bridge       *Bridge
	ServerClient *HTTPClient
	Store        *SessionStore
	Pairing      *Pairing
	Dedup        *MessageDedup
	Attachments  *AttachmentStore
	Chat         *ChatRuntime
	ctx          context.Context
	cancel       context.CancelFunc
	HTTP         *http.Client
	TLS          *tls.Config
	Log          func(string)
	Now          func() time.Time
}

func newPlatformRuntimeCore(ctx context.Context, cfg Config, httpClient *http.Client, tlsConfig *tls.Config, log func(string), now func() time.Time) (*platformRuntimeCore, error) {
	if err := migrationRuntimeAvailable(cfg); err != nil {
		return nil, err
	}
	if cfg.Err != nil {
		return nil, cfg.Err
	}
	if !filepath.IsAbs(cfg.Home) || !filepath.IsAbs(cfg.ConfigDir) {
		return nil, errors.New("IM runtime requires explicit home/config")
	}
	if log == nil {
		log = func(string) {}
	}
	if now == nil {
		now = time.Now
	}
	var err error
	if httpClient == nil {
		httpClient, err = AdapterNetworkHTTP(cfg.Environment, true)
		if err != nil {
			return nil, err
		}
	}
	if tlsConfig == nil {
		client, e := AdapterNetworkHTTP(cfg.Environment, false)
		if e != nil {
			httpClient.CloseIdleConnections()
			return nil, e
		}
		if transport, ok := client.Transport.(*http.Transport); ok {
			tlsConfig = transport.TLSClientConfig
		}
		client.CloseIdleConnections()
	}
	ctx, lifecycle := newMigrationContext(ctx)
	ctx, cancel := context.WithCancel(ctx)
	r := &platformRuntimeCore{migration: lifecycle, Config: cfg, ctx: ctx, cancel: cancel, HTTP: httpClient, TLS: tlsConfig, Log: log, Now: now}
	r.Bridge = NewBridge(ctx, cfg, tlsConfig, log, BridgeOptions{Now: now})
	r.ServerClient = NewHTTPClient(cfg, httpClient)
	r.Store = NewSessionStore(cfg.ConfigDir)
	r.Store.Lifecycle = lifecycle
	r.Store.NowMillis = func() int64 { return now().UnixMilli() }
	r.Pairing = NewPairing(cfg.ConfigDir, nil)
	r.Pairing.Lifecycle = lifecycle
	r.Pairing.NowMillis = r.Store.NowMillis
	r.Dedup = NewMessageDedup(ctx)
	r.Dedup.Now = now
	r.Attachments = NewConfiguredAttachmentStore(cfg)
	r.Attachments.Lifecycle = lifecycle
	r.Attachments.Now = now
	return r, nil
}
func (r *platformRuntimeCore) bind(port ChatPort, interval time.Duration, threshold int) {
	r.Chat = NewChatRuntime(r.ctx, ChatRuntimeOptions{Config: r.Config, Port: port, Bridge: r.Bridge, Store: r.Store, Client: r.ServerClient, Pairing: r.Pairing, Dedup: r.Dedup, FlushInterval: interval, FlushThreshold: threshold, Log: r.Log, Now: r.Now})
}
func (r *platformRuntimeCore) close() {
	r.cancel()
	r.Bridge.Close()
	if r.Chat != nil {
		r.Chat.Close()
	}
	r.Dedup.Close()
	r.HTTP.CloseIdleConnections()
}
func (r *platformRuntimeCore) collect(id, prefix string, count int, download func(int, string) (LocalAttachment, error), notice func(string) error) ([]protocol.JSONObject, error) {
	session := id
	if stored := r.Store.Get(id); stored != nil {
		session = stored.SessionID
	}
	results := make([]struct {
		local LocalAttachment
		err   error
	}, count)
	var workers sync.WaitGroup
	for i := 0; i < count; i++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			results[index].local, results[index].err = download(index, session)
		}(i)
	}
	workers.Wait()
	refs := []protocol.JSONObject{}
	failures := 0
	for _, result := range results {
		if result.err != nil {
			failures++
			r.Log(prefix + " attachment download failed: " + result.err.Error())
			continue
		}
		local := result.local
		check := CheckAttachmentLimit(local.Kind, local.Size, local.MIME)
		if !check.OK {
			if err := notice(check.Hint); err != nil {
				return nil, err
			}
			continue
		}
		ref := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw(local.Kind)}, protocol.JSONField{Name: "name", Value: raw(local.Name)}, protocol.JSONField{Name: "mimeType", Value: raw(local.MIME)})
		if local.Kind == "image" {
			ref.Set("data", raw(base64.StdEncoding.EncodeToString(local.Buffer)))
		} else {
			ref.Set("path", raw(local.Path))
		}
		refs = append(refs, ref)
	}
	if failures > 0 {
		text := "附件下载失败，请稍后重试。"
		if failures < count {
			text = fmt.Sprintf("%d 个附件下载失败，已跳过。", failures)
		}
		if err := notice(text); err != nil {
			return nil, err
		}
	}
	return refs, nil
}
