package imruntime

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	wa "github.com/NanmiCoder/cc-haha/backend/internal/whatsappcompat"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/encoding/protojson"
)

type WhatsAppRuntimeOptions struct {
	Config        Config
	ClientOptions wa.NodeWhatsAppOptions
	ServerHTTP    *http.Client
	SocketTLS     *tls.Config
	Log           func(string)
	Now           func() time.Time
}
type WhatsAppRuntime struct {
	*platformRuntimeCore
	Client      *wa.NodeWhatsAppClient
	Media       *WhatsAppMedia
	mu          sync.Mutex
	thinking    map[string]Text
	noticed     map[string]bool
	start, stop sync.Once
	done        chan struct{}
	err         error
}

func WhatsAppAuthDirectory(cfg Config) (string, error) {
	value := PlatformCredential(cfg, "whatsapp", "authDir", "WHATSAPP_AUTH_DIR", filepath.Join(cfg.ConfigDir, "whatsapp-auth", "default"))
	if !isString(value) {
		return "", errors.New("configured WhatsApp authDir requires string operations")
	}
	path := TextJSON(value).Trim().String()
	if path == "" {
		path = filepath.Join(cfg.ConfigDir, "whatsapp-auth", "default")
	}
	if path == "~" {
		path = cfg.Home
	} else if strings.HasPrefix(path, "~/") {
		path = filepath.Join(cfg.Home, path[2:])
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cfg.Environment["PWD"], path)
	}
	return filepath.Clean(path), nil
}

func NewWhatsAppRuntime(ctx context.Context, o WhatsAppRuntimeOptions) (*WhatsAppRuntime, error) {
	path, e := WhatsAppAuthDirectory(o.Config)
	if e != nil {
		return nil, e
	}
	if _, e = os.Stat(filepath.Join(path, "creds.json")); e != nil {
		return nil, errors.New("[WhatsApp] No linked WhatsApp account found. Bind with QR in Desktop Settings first.")
	}
	core, e := newPlatformRuntimeCore(ctx, o.Config, o.ServerHTTP, o.SocketTLS, o.Log, o.Now)
	if e != nil {
		return nil, e
	}
	r := &WhatsAppRuntime{platformRuntimeCore: core, thinking: map[string]Text{}, noticed: map[string]bool{}, done: make(chan struct{})}
	clientOptions := o.ClientOptions
	clientOptions.AuthDir = path
	if clientOptions.ImageThumbnail == nil {
		clientOptions.ImageThumbnail = func(data []byte) (wa.NodeImageThumbnail, error) {
			codec, e := imageprocessing.BundledCodec("")
			if e != nil {
				return wa.NodeImageThumbnail{}, e
			}
			return WhatsAppImageThumbnail(codec, data)
		}
	}
	clientOptions.Now = core.Now
	prior := clientOptions.OnEvent
	clientOptions.OnEvent = func(v any) {
		switch event := v.(type) {
		case *events.Message:
			if !event.Info.SourceOffline && event.SourceWebMsg == nil && event.UnavailableRequestID == "" {
				// Release the SDK node handler before waiting for a mediaretry
				// notification on that same queue. Chat owns and joins this work.
				r.Chat.launch(func() {
					if e := r.HandleMessage(event); e != nil {
						core.Log("[WhatsApp] message error: " + e.Error())
					}
				})
			}
		case *events.LoggedOut:
			r.mu.Lock()
			r.err = errors.New("[WhatsApp] Account logged out. Rebind with QR in Desktop Settings.")
			r.mu.Unlock()
			core.cancel()
		}
		if prior != nil {
			prior(v)
		}
	}
	r.Client, e = wa.NewNodeWhatsAppClient(core.ctx, clientOptions)
	if e != nil {
		core.close()
		return nil, e
	}
	mediaHTTP := o.ClientOptions.HTTPTransport
	if mediaHTTP == nil {
		mediaHTTP = http.DefaultTransport
	}
	r.Media = &WhatsAppMedia{Store: core.Attachments, HTTP: &http.Client{Transport: mediaHTTP}, Reupload: func(ctx context.Context, message protocol.JSONObject) (protocol.JSONObject, error) {
		field, media, e := whatsAppRetryMedia(message.Get("message"))
		if e != nil {
			return message, e
		}
		key, e := WhatsAppMediaKey(media.Get("mediaKey"))
		if e != nil {
			return message, e
		}
		k := object(message.Get("key"))
		chat, e := types.ParseJID(k.String("remoteJid"))
		if e != nil {
			return message, e
		}
		sender := chat
		participant := k.String("participant")
		if participant != "" {
			sender, e = types.ParseJID(participant)
			if e != nil {
				return message, e
			}
		}
		info := types.MessageInfo{ID: k.String("id"), MessageSource: types.MessageSource{Chat: chat, Sender: sender, IsFromMe: truthy(k.Get("fromMe")), IsGroup: chat.Server == types.GroupServer}}
		direct, e := r.Client.UpdateMedia(ctx, info, key)
		if e != nil {
			return message, e
		}
		message.Set("message", whatsAppSetRetryMediaPath(message.Get("message"), field, direct, 0))
		return message, nil
	}}
	core.bind(ChatPort{Platform: "whatsapp", LogPrefix: "[WhatsApp]", SendNotice: r.SendNotice, CreateResponse: func(id string) ResponseStream { return &whatsAppRuntimeResponse{runtime: r, id: id} }, SendImage: func(id string, image OutboundImage) error {
		jid, e := types.ParseJID(id)
		if e != nil {
			return e
		}
		_, e = r.Client.SendMedia(r.ctx, jid, image.Buffer, image.MIME, image.Alt, "")
		return e
	}, ClearChat: r.clearTransient}, 800*time.Millisecond, 320)
	r.Chat.o.ServerMessage = r.HandleServerMessage
	return r, nil
}

func (r *WhatsAppRuntime) SendNotice(id, text string) error {
	jid, e := types.ParseJID(id)
	if e != nil {
		return e
	}
	for _, chunk := range SplitWhatsAppText(NewText(text), 4000) {
		if _, e = r.Client.SendText(r.ctx, jid, chunk.String()); e != nil {
			return e
		}
	}
	return nil
}
func (r *WhatsAppRuntime) clearTransient(id string) {
	r.mu.Lock()
	delete(r.thinking, id)
	delete(r.noticed, id)
	r.mu.Unlock()
}
func (r *WhatsAppRuntime) HandleServerMessage(id string, m protocol.JSONObject) error {
	return r.handleServerMessage(r.ctx, id, m, func(text string) error { return r.SendNotice(id, text) })
}

func (r *WhatsAppRuntime) handleServerMessage(ctx context.Context, id string, m protocol.JSONObject, send func(string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	kind := m.String("type")
	if kind == "content_start" {
		if m.String("blockType") == "text" {
			r.mu.Lock()
			delete(r.thinking, id)
			r.mu.Unlock()
		}
		return nil
	}
	if kind == "error" {
		r.clearTransient(id)
		c := r.Chat.chat(id)
		c.mu.Lock()
		c.state.State = "idle"
		c.state.Verb = ""
		c.mu.Unlock()
		text, _ := protocol.JavaScriptStringJSON(m.Get("message"))
		return r.Chat.handleError(id, TextJSON(text).String())
	}
	switch kind {
	case "connected", "status", "content_delta", "thinking", "permission_request", "message_complete", "system_notification", "permission_resolved", "permission_requests_snapshot":
	default:
		return nil
	}
	if kind == "thinking" {
		r.mu.Lock()
		r.thinking[id] = r.thinking[id].Join(TextJSON(m.Get("text")))
		r.mu.Unlock()
		return nil
	}
	if kind == "status" && m.String("state") == "thinking" {
		r.mu.Lock()
		first := !r.noticed[id]
		r.noticed[id] = true
		r.mu.Unlock()
		if first {
			if e := send("思考中..."); e != nil {
				return e
			}
		}
	}
	if kind == "message_complete" || kind == "error" {
		r.clearTransient(id)
	}
	if kind == "content_delta" {
		r.mu.Lock()
		delete(r.thinking, id)
		r.mu.Unlock()
	}
	if kind == "permission_request" {
		c := r.Chat.chat(id)
		c.mu.Lock()
		c.state.PendingPermissionCount++
		c.state.State = "permission_pending"
		request := m.String("requestId")
		found := false
		for _, pending := range c.state.Pending {
			found = found || pending == request
		}
		if !found {
			c.state.Pending = append(c.state.Pending, request)
		}
		c.mu.Unlock()
		return send("🔐 需要权限确认 [" + request + "]\n工具: " + m.String("toolName") + "\n" + PreviewInput(m.Get("input"), 300).String() + "\n\n" + PermissionInstructions(request))
	}
	return r.Chat.HandleServerMessage(id, m)
}

func (r *WhatsAppRuntime) HandleMessage(event *events.Message) error {
	content := event.RawMessage
	if content == nil {
		content = event.Message
	}
	b, e := protojson.Marshal(content)
	if e != nil {
		return e
	}
	message := protocol.NewJSONObject(protocol.JSONField{Name: "key", Value: raw(map[string]any{"remoteJid": event.Info.Chat.String(), "id": event.Info.ID, "fromMe": event.Info.IsFromMe, "participant": event.Info.Sender.String()})}, protocol.JSONField{Name: "pushName", Value: raw(event.Info.PushName)}, protocol.JSONField{Name: "message", Value: whatsAppSocketJSON(b)})
	payload, e := WhatsAppInbound(message)
	if e != nil || !payload.IsObject() {
		return e
	}
	id := payload.String("chatId")
	if !r.Dedup.TryRecord("whatsapp:" + payload.String("dedupKey")) {
		return nil
	}
	descriptor, e := WhatsAppDescribeMedia(message.Get("message"))
	if e != nil {
		return e
	}
	count := 0
	if descriptor != nil {
		count = 1
	}
	attachments, e := r.collect(id, "[WhatsApp]", count, func(_ int, session string) (LocalAttachment, error) {
		v, e := r.Media.Download(r.ctx, message, session)
		if e != nil {
			return LocalAttachment{}, e
		}
		if v == nil {
			return LocalAttachment{}, nil
		}
		return *v, nil
	}, func(text string) error { return r.SendNotice(id, text) })
	if e != nil {
		return e
	}
	text := TextJSON(payload.Get("text"))
	if len(text.Trim()) == 0 && len(attachments) == 0 {
		return nil
	}
	r.Client.ReadAndCompose(r.ctx, event.Info)
	r.Chat.QueueInbound(InboundChatMessage{ChatID: id, UserID: payload.Get("userId"), DisplayName: payload.String("displayName"), Text: text.String(), TextValue: &text, Attachments: attachments})
	return nil
}
func (r *WhatsAppRuntime) Start() {
	r.start.Do(func() {
		r.Attachments.GC()
		if e := r.Client.Connect(r.ctx); e != nil {
			r.mu.Lock()
			r.err = e
			r.mu.Unlock()
			close(r.done)
			return
		}
		go func() { <-r.ctx.Done(); close(r.done) }()
	})
}
func (r *WhatsAppRuntime) Wait() error { <-r.done; r.mu.Lock(); defer r.mu.Unlock(); return r.err }
func (r *WhatsAppRuntime) Close() {
	r.stop.Do(func() { r.cancel(); _ = r.Client.Close(context.Background()); r.platformRuntimeCore.close() })
}

type whatsAppRuntimeResponse struct {
	runtime *WhatsAppRuntime
	id      string
	mu      sync.Mutex
	text    Text
	closed  bool
}

func (r *whatsAppRuntimeResponse) Append(text Text) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.text = r.text.Join(text)
	}
	return nil
}
func (r *whatsAppRuntimeResponse) Finish() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	text := r.text.Clone()
	r.text = nil
	r.mu.Unlock()
	if len(text.Trim()) == 0 {
		return nil
	}
	return r.runtime.SendNotice(r.id, text.String())
}
