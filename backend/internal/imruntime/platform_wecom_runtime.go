package imruntime

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/google/uuid"
)

type WecomRuntimeOptions struct {
	Config        Config
	ClientOptions WecomClientOptions
	NewClient     func(context.Context, WecomClientOptions) (*WecomClient, error)
	ServerHTTP    *http.Client
	SocketTLS     *tls.Config
	Log           func(string)
	Now           func() time.Time
}
type WecomRuntime struct {
	*platformRuntimeCore
	Client      *WecomClient
	mu          sync.Mutex
	frames      map[string]protocol.JSONObject
	start, stop sync.Once
	wg          sync.WaitGroup
}

func NewWecomRuntime(ctx context.Context, o WecomRuntimeOptions) (*WecomRuntime, error) {
	if o.Config.Err != nil {
		return nil, o.Config.Err
	}
	credentials := WecomConfig(o.Config)
	if !credentialTruthy(credentials.BotID) || !credentialTruthy(credentials.Secret) {
		return nil, errors.New("[WeCom] Missing WECOM_BOT_ID / WECOM_BOT_SECRET. Bind 企业微信 in Desktop Settings > IM.")
	}
	core, err := newPlatformRuntimeCore(ctx, o.Config, o.ServerHTTP, o.SocketTLS, o.Log, o.Now)
	if err != nil {
		return nil, err
	}
	r := &WecomRuntime{platformRuntimeCore: core, frames: map[string]protocol.JSONObject{}}
	if o.NewClient == nil {
		o.NewClient = NewWecomClient
	}
	options := o.ClientOptions
	options.Credentials = protocol.NewJSONObject(protocol.JSONField{Name: "bot_id", Value: credentials.BotID}, protocol.JSONField{Name: "secret", Value: credentials.Secret})
	options.Environment = o.Config.Environment
	if options.TLS == nil {
		options.TLS = core.TLS
	}
	if options.Now == nil {
		options.Now = core.Now
	}
	options.OnMessage = func(frame protocol.JSONObject) {
		if err := r.HandleMessage(frame); err != nil {
			core.Log("[WeCom] message error: " + err.Error())
		}
	}
	options.OnAuthenticated = func() { core.Log("[WeCom] Authenticated, bot is running!") }
	options.OnDisconnected = func(reason string) { core.Log("[WeCom] Disconnected: " + reason) }
	options.OnReconnecting = func(attempt int) { core.Log(fmt.Sprintf("[WeCom] Reconnecting (attempt %d)", attempt)) }
	options.OnError = func(err error) { core.Log("[WeCom] Connection error: " + err.Error()) }
	r.Client, err = o.NewClient(core.ctx, options)
	if err != nil {
		core.close()
		return nil, err
	}
	core.bind(ChatPort{Platform: "wecom", LogPrefix: "[WeCom]", SendNotice: r.Notice, CreateResponse: func(id string) ResponseStream {
		r.mu.Lock()
		frame, ok := r.frames[id]
		r.mu.Unlock()
		var captured *protocol.JSONObject
		if ok {
			captured = &frame
		}
		return &wecomRuntimeResponse{NewWecomResponse(core.ctx, r.Client, NewText(id), captured, strings.ReplaceAll(uuid.NewString(), "-", ""), core.Log)}
	}, SendImage: r.SendImage, ClearChat: func(id string) { r.mu.Lock(); delete(r.frames, id); r.mu.Unlock() }}, 500*time.Millisecond, 160)
	return r, nil
}

type wecomRuntimeResponse struct{ response *WecomResponse }

func (s *wecomRuntimeResponse) Append(text Text) error { return s.response.AppendText(text) }
func (s *wecomRuntimeResponse) Finish() error          { return s.response.Finish() }
func (r *WecomRuntime) Notice(id, text string) error {
	for _, chunk := range SplitWecomMessageByBytes(NewText(text), 4000) {
		_, err := r.Client.SendMessage(r.ctx, NewText(id), protocol.NewJSONObject(protocol.JSONField{Name: "msgtype", Value: raw("markdown")}, protocol.JSONField{Name: "markdown", Value: raw(protocol.NewJSONObject(protocol.JSONField{Name: "content", Value: raw(chunk)}))}))
		if err != nil {
			return err
		}
	}
	return nil
}
func (r *WecomRuntime) SendImage(id string, image OutboundImage) error {
	check := CheckAttachmentLimit("image", int64(len(image.Buffer)), image.MIME)
	if !check.OK {
		r.Log("[WeCom] Outbound image rejected: " + check.Hint)
		return nil
	}
	name := NewText(fmt.Sprintf("claude-%d.%s", r.Now().UnixMilli(), ImageExtensionForMIME(image.MIME)))
	uploaded, err := r.Client.UploadMedia(r.ctx, image.Buffer, NewText("image"), name)
	if err != nil {
		return err
	}
	if !credentialTruthy(uploaded.Get("media_id")) {
		return errors.New("Enterprise WeChat upload returned no media_id")
	}
	_, err = r.Client.SendMediaMessage(r.ctx, NewText(id), NewText("image"), TextJSON(uploaded.Get("media_id")))
	return err
}
func (r *WecomRuntime) HandleMessage(frame protocol.JSONObject) error {
	done, admitted := r.migration.Admit()
	if !admitted {
		return nil
	}
	defer done(nil)
	body := object(frame.Get("body"))
	payload, err := ExtractWecomPayload(body)
	if err != nil || payload == nil {
		return err
	}
	id := payload.ChatID.String()
	r.mu.Lock()
	r.frames[id] = frame.Clone()
	r.mu.Unlock()
	candidates, err := CollectWecomMediaCandidates(body, r.Now().UnixMilli())
	if err != nil {
		return err
	}
	has := len(candidates) > 0
	r.Chat.QueueInbound(InboundChatMessage{ChatID: id, UserID: raw(payload.UserID), DisplayName: payload.UserID.String(), DedupKey: wecomKey(payload.DedupKey), Text: payload.Text.String(), TextValue: &payload.Text, HasAttachments: &has, LoadAttachments: func() ([]protocol.JSONObject, error) {
		return r.collect(id, "[WeCom]", len(candidates), func(index int, session string) (LocalAttachment, error) {
			return r.Client.DownloadCandidate(r.ctx, r.Attachments, candidates[index], session)
		}, func(text string) error { return r.Notice(id, text) })
	}})
	return nil
}
func (r *WecomRuntime) Start() {
	r.start.Do(func() {
		r.Log("[WeCom] Starting adapter...")
		r.Log("[WeCom] Server: " + r.Config.ServerURL)
		r.wg.Add(1)
		go func() { defer r.wg.Done(); r.Attachments.GC() }()
		r.Client.Start()
	})
}
func (r *WecomRuntime) Wait() error { <-r.ctx.Done(); return nil }
func (r *WecomRuntime) Close() {
	r.stop.Do(func() { r.cancel(); r.Client.Close(); r.wg.Wait(); r.platformRuntimeCore.close() })
}
