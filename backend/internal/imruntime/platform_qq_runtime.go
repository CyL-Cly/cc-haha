package imruntime

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type QQRuntimeOptions struct {
	Config         Config
	ClientOptions  QQClientOptions
	NewClient      func(context.Context, QQClientOptions) (*QQClient, error)
	GatewayOptions QQGatewayOptions
	ServerHTTP     *http.Client
	SocketTLS      *tls.Config
	Log            func(string)
	Now            func() time.Time
}
type QQRuntime struct {
	*platformRuntimeCore
	Client      *QQClient
	Gateway     *QQGateway
	Media       *QQMedia
	mu          sync.Mutex
	targets     map[string]QQTarget
	err         error
	start, stop sync.Once
	done        chan struct{}
	wg          sync.WaitGroup
}

func NewQQRuntime(ctx context.Context, o QQRuntimeOptions) (*QQRuntime, error) {
	if o.Config.Err != nil {
		return nil, o.Config.Err
	}
	credentials := QQConfig(o.Config)
	if !credentialTruthy(credentials.AppID) || !credentialTruthy(credentials.AppSecret) {
		return nil, errors.New("[QQ] Missing QQ_APP_ID / QQ_APP_SECRET. Bind QQ in Desktop Settings > IM.")
	}
	core, err := newPlatformRuntimeCore(ctx, o.Config, o.ServerHTTP, o.SocketTLS, o.Log, o.Now)
	if err != nil {
		return nil, err
	}
	r := &QQRuntime{platformRuntimeCore: core, targets: map[string]QQTarget{}, done: make(chan struct{})}
	if o.NewClient == nil {
		o.NewClient = NewQQClient
	}
	options := o.ClientOptions
	options.Credentials = protocol.NewJSONObject(protocol.JSONField{Name: "appId", Value: credentials.AppID}, protocol.JSONField{Name: "appSecret", Value: credentials.AppSecret})
	options.Environment = o.Config.Environment
	if options.Now == nil {
		options.Now = core.Now
	}
	options.Log = core.Log
	r.Client, err = o.NewClient(core.ctx, options)
	if err != nil {
		core.close()
		return nil, err
	}
	r.Media = &QQMedia{Store: core.Attachments, HTTP: r.Client.o.HTTP}
	core.bind(NewQQChatPort(QQPortOptions{Context: core.ctx, Client: r.Client, TargetFor: r.TargetFor, Log: core.Log, ClearChat: func(id string) { r.mu.Lock(); delete(r.targets, id); r.mu.Unlock() }}), QQFlushInterval, QQFlushThreshold)
	gatewayOptions := o.GatewayOptions
	gatewayOptions.Client = r.Client
	if gatewayOptions.TLS == nil {
		gatewayOptions.TLS = core.TLS
	}
	gatewayOptions.OnMessage = func(message protocol.JSONObject) {
		if err := r.HandleMessage(message); err != nil {
			core.Log("[QQ] message error: " + err.Error())
		}
	}
	gatewayOptions.OnReady = func(_ json.RawMessage) { core.Log("[QQ] Gateway ready, bot is running!") }
	gatewayOptions.OnResumed = func(_ json.RawMessage) { core.Log("[QQ] Gateway session resumed") }
	gatewayOptions.OnError = func(err error) { core.Log("[QQ] Connection error: " + err.Error()) }
	r.Gateway, err = NewQQGateway(gatewayOptions)
	if err != nil {
		r.Client.Close()
		core.close()
		return nil, err
	}
	return r, nil
}
func (r *QQRuntime) TargetFor(id string) QQTarget {
	r.mu.Lock()
	target, ok := r.targets[id]
	r.mu.Unlock()
	if !ok {
		return QQTarget{Scope: "c2c", TargetID: NewText(id)}
	}
	return QQTarget{Scope: target.Scope, TargetID: target.TargetID.Clone(), MessageID: target.MessageID.Clone()}
}
func (r *QQRuntime) HandleMessage(message protocol.JSONObject) error {
	done, admitted := r.migration.Admit()
	if !admitted {
		return nil
	}
	defer done(nil)
	payload, err := ExtractQQPayload(message)
	if err != nil || !payload.IsObject() {
		return err
	}
	id := payload.String("chatId")
	if id == "" {
		return nil
	}
	text := TextJSON(payload.Get("text"))
	r.mu.Lock()
	r.targets[id] = QQTarget{Scope: "c2c", TargetID: TextJSON(payload.Get("chatId")), MessageID: TextJSON(message.Get("messageId"))}
	r.mu.Unlock()
	attachments := collectJSONArray(payload.Get("attachments"))
	has := len(attachments) > 0
	seed := payload.String("dedupKey")
	r.Chat.QueueInbound(InboundChatMessage{ChatID: id, UserID: payload.Get("userId"), DisplayName: payload.String("displayName"), DedupKey: string(payload.Get("dedupKey")), Text: text.String(), TextValue: &text, HasAttachments: &has, LoadAttachments: func() ([]protocol.JSONObject, error) {
		return r.collect(id, "[QQ]", len(attachments), func(index int, session string) (LocalAttachment, error) {
			return r.Media.Download(r.ctx, attachments[index], session, fmt.Sprintf("%s-%d", seed, index))
		}, func(text string) error { return r.Client.SendNotice(r.ctx, r.TargetFor(id), NewText(text)) })
	}})
	return nil
}
func (r *QQRuntime) Start() {
	r.start.Do(func() {
		r.Log("[QQ] Starting adapter...")
		r.Log("[QQ] Server: " + r.Config.ServerURL)
		r.wg.Add(2)
		go func() { defer r.wg.Done(); r.Attachments.GC() }()
		go func() {
			defer r.wg.Done()
			defer close(r.done)
			err := r.Gateway.Run(r.ctx)
			r.mu.Lock()
			if r.ctx.Err() == nil {
				r.err = err
			}
			r.mu.Unlock()
		}()
	})
}
func (r *QQRuntime) Wait() error { <-r.done; r.mu.Lock(); defer r.mu.Unlock(); return r.err }
func (r *QQRuntime) Close() {
	r.stop.Do(func() { r.cancel(); r.Gateway.Close(); r.Client.Close(); r.wg.Wait(); r.platformRuntimeCore.close() })
}
