package imruntime

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type SlackRuntimeOptions struct {
	Config     Config
	API        *SlackAPI
	ServerHTTP *http.Client
	SocketTLS  *tls.Config
	Log        func(string)
	Now        func() time.Time
}
type SlackRuntime struct {
	o            SlackRuntimeOptions
	API          *SlackAPI
	Bridge       *Bridge
	Client       *HTTPClient
	Store        *SessionStore
	Pairing      *Pairing
	Dedup        *MessageDedup
	Attachments  *AttachmentStore
	Chat         *ChatRuntime
	Socket       *SlackSocketMode
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	threads      map[string]string
	names        map[string]string
	nameOrder    []string
	botUserID    string
	closed       bool
	wg           sync.WaitGroup
	start, close sync.Once
	done         chan struct{}
	err          error
}

func SlackAppToken(c Config) string {
	if value := c.Environment["SLACK_APP_TOKEN"]; value != "" {
		return value
	}
	value := object(c.Document.Get("slack")).Get("appToken")
	if truthy(value) {
		s, _ := protocol.JavaScriptStringJSON(value)
		return TextJSON(s).String()
	}
	return ""
}
func NewSlackRuntime(ctx context.Context, o SlackRuntimeOptions) (*SlackRuntime, error) {
	if o.Config.Err != nil {
		return nil, o.Config.Err
	}
	if !filepath.IsAbs(o.Config.Home) || !filepath.IsAbs(o.Config.ConfigDir) {
		return nil, errors.New("Slack runtime requires explicit home/config")
	}
	if o.Config.Token == "" || SlackAppToken(o.Config) == "" {
		return nil, errors.New("[Slack] Missing SLACK_BOT_TOKEN / SLACK_APP_TOKEN. Configure Slack in Desktop Settings > IM.")
	}
	if o.Log == nil {
		o.Log = func(string) {}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	var err error
	if o.API == nil {
		o.API, err = NewSlackAPI(SlackAPIOptions{Token: o.Config.Token, Environment: o.Config.Environment})
		if err != nil {
			return nil, err
		}
	}
	if o.ServerHTTP == nil {
		o.ServerHTTP, err = AdapterNetworkHTTP(o.Config.Environment, true)
		if err != nil {
			return nil, err
		}
	}
	if o.SocketTLS == nil {
		client, err := AdapterNetworkHTTP(o.Config.Environment, false)
		if err != nil {
			return nil, err
		}
		if tr, ok := client.Transport.(*http.Transport); ok {
			o.SocketTLS = tr.TLSClientConfig
		}
		client.CloseIdleConnections()
	}
	ctx, cancel := context.WithCancel(ctx)
	r := &SlackRuntime{o: o, API: o.API, ctx: ctx, cancel: cancel, threads: map[string]string{}, names: map[string]string{}, done: make(chan struct{})}
	r.Bridge = NewBridge(ctx, o.Config, o.SocketTLS, o.Log)
	r.Client = NewHTTPClient(o.Config, o.ServerHTTP)
	r.Store = NewSessionStore(o.Config.ConfigDir)
	r.Store.NowMillis = func() int64 { return o.Now().UnixMilli() }
	r.Pairing = NewPairing(o.Config.ConfigDir, nil)
	r.Pairing.NowMillis = r.Store.NowMillis
	r.Dedup = NewMessageDedup(ctx)
	r.Dedup.Now = o.Now
	r.Attachments = NewAttachmentStore(o.Config.Home)
	r.Attachments.Now = o.Now
	port := ChatPort{Platform: "slack", LogPrefix: "[Slack]", SendNotice: r.Notice, CreateResponse: func(id string) ResponseStream { return &SlackResponse{runtime: r, chat: id} }, SendImage: r.SendImage, ClearChat: func(id string) { r.mu.Lock(); delete(r.threads, id); r.mu.Unlock() }}
	r.Chat = NewChatRuntime(ctx, ChatRuntimeOptions{Config: o.Config, Port: port, Bridge: r.Bridge, Store: r.Store, Client: r.Client, Pairing: r.Pairing, Dedup: r.Dedup, FlushInterval: 900 * time.Millisecond, FlushThreshold: 240, Log: o.Log, Now: o.Now})
	r.Socket = NewSlackSocketMode(ctx, SlackSocketOptions{OpenConnection: func(ctx context.Context) (string, error) {
		return r.API.OpenSocketConnection(ctx, SlackAppToken(o.Config))
	}, TLS: o.SocketTLS, Log: o.Log, OnEnvelope: r.HandleEnvelope})
	return r, nil
}
func (r *SlackRuntime) launch(fn func()) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.wg.Add(1)
	r.mu.Unlock()
	go func() { defer r.wg.Done(); fn() }()
}
func (r *SlackRuntime) Start() {
	r.start.Do(func() {
		r.o.Log("[Slack] Starting adapter...")
		r.o.Log("[Slack] Server: " + r.o.Config.ServerURL)
		r.launch(func() { r.Attachments.GC() })
		r.launch(func() {
			defer close(r.done)
			identity, err := r.API.AuthTest(r.ctx)
			if err != nil {
				r.mu.Lock()
				r.err = err
				r.mu.Unlock()
				if r.ctx.Err() == nil {
					r.o.Log("[Slack] auth.test failed: " + err.Error())
				}
				return
			}
			user := identity.String("user_id")
			r.mu.Lock()
			r.botUserID = user
			r.mu.Unlock()
			if user == "" {
				user = "unknown bot user"
			}
			r.o.Log("[Slack] Authenticated as " + user)
			r.Socket.Start()
			<-r.ctx.Done()
		})
	})
}
func (r *SlackRuntime) Wait() error { <-r.done; r.mu.Lock(); defer r.mu.Unlock(); return r.err }
func (r *SlackRuntime) Close() {
	r.close.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.mu.Unlock()
		r.cancel()
		r.Socket.Close()
		r.Bridge.Close()
		r.Dedup.Close()
		r.wg.Wait()
		r.Chat.Close()
		r.API.Close()
		r.o.ServerHTTP.CloseIdleConnections()
	})
}
func (r *SlackRuntime) Thread(id string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.threads[id]
}
func (r *SlackRuntime) Notice(id, text string) error {
	thread := r.Thread(id)
	for _, chunk := range SplitMessage(NewText(text), 3500) {
		if _, err := r.API.PostMessage(r.ctx, id, chunk, thread); err != nil {
			return err
		}
	}
	return nil
}
func (r *SlackRuntime) SendImage(id string, image OutboundImage) error {
	if check := CheckAttachmentLimit("image", int64(len(image.Buffer)), image.MIME); !check.OK {
		r.o.Log("[Slack] Outbound image rejected: " + check.Hint)
		return nil
	}
	title := image.Alt
	return r.API.UploadFile(r.ctx, SlackUpload{Channel: id, Buffer: image.Buffer, Filename: fmt.Sprintf("claude-%d.%s", r.o.Now().UnixMilli(), ImageExtensionForMIME(image.MIME)), Title: &title, Thread: r.Thread(id)})
}
func (r *SlackRuntime) DisplayName(user string) string {
	r.mu.Lock()
	cached := r.names[user]
	r.mu.Unlock()
	if cached != "" {
		return cached
	}
	info, err := r.API.Get(r.ctx, "users.info", [][2]string{{"user", user}})
	if err != nil {
		return user
	}
	u := object(info.Get("user"))
	name := protocol.TrimJavaScriptString(u.String("real_name"))
	if name == "" {
		name = protocol.TrimJavaScriptString(u.String("name"))
	}
	if name == "" {
		name = user
	}
	r.mu.Lock()
	if len(r.names) >= 500 {
		oldest := r.nameOrder[0]
		r.nameOrder = r.nameOrder[1:]
		delete(r.names, oldest)
	}
	if _, exists := r.names[user]; !exists {
		r.nameOrder = append(r.nameOrder, user)
	}
	r.names[user] = name
	r.mu.Unlock()
	return name
}
func (r *SlackRuntime) CollectAttachments(id string, files []protocol.JSONObject) ([]protocol.JSONObject, error) {
	refs := []protocol.JSONObject{}
	session := id
	if s := r.Store.Get(id); s != nil {
		session = s.SessionID
	}
	failures := 0
	for _, file := range files {
		err := func() error {
			address := file.String("url_private_download")
			if v := file.Get("url_private_download"); len(v) == 0 || string(v) == "null" {
				address = file.String("url_private")
			}
			buffer, err := r.API.DownloadFile(r.ctx, address)
			if err != nil {
				return err
			}
			name := protocol.TrimJavaScriptString(file.String("name"))
			if name == "" {
				name = protocol.TrimJavaScriptString(file.String("title"))
			}
			if name == "" {
				key := file.String("id")
				if v := file.Get("id"); len(v) == 0 || string(v) == "null" {
					key = fmt.Sprint(r.o.Now().UnixMilli())
				}
				name = "slack-file-" + key
			}
			mime := protocol.TrimJavaScriptString(strings.Split(file.String("mimetype"), ";")[0])
			if mime == "" {
				mime = InferMIMEFromFilename(name)
			}
			if mime == "" {
				mime = "application/octet-stream"
			}
			kind := "file"
			if strings.HasPrefix(strings.ToLower(mime), "image/") {
				kind = "image"
			}
			if check := CheckAttachmentLimit(kind, int64(len(buffer)), mime); !check.OK {
				return r.Notice(id, check.Hint)
			}
			ref := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw(kind)}, protocol.JSONField{Name: "name", Value: raw(name)})
			if kind == "image" {
				ref.Set("data", raw(base64.StdEncoding.EncodeToString(buffer)))
			} else {
				path, err := r.Attachments.ResolvePath("slack", session, name)
				if err != nil {
					return err
				}
				if err = r.Attachments.Write(path, buffer); err != nil {
					return err
				}
				ref.Set("path", raw(path))
			}
			ref.Set("mimeType", raw(mime))
			refs = append(refs, ref)
			return nil
		}()
		if err != nil {
			failures++
			r.o.Log("[Slack] file download failed: " + err.Error())
		}
	}
	if failures > 0 {
		notice := fmt.Sprintf("%d 个附件下载失败，已跳过。", failures)
		if failures == len(files) {
			notice = "附件下载失败，请稍后重试。"
		}
		if err := r.Notice(id, notice); err != nil {
			return nil, err
		}
	}
	return refs, nil
}
func (r *SlackRuntime) HandleEnvelope(e protocol.JSONObject) {
	if e.String("type") != "events_api" {
		return
	}
	r.mu.Lock()
	bot := r.botUserID
	r.mu.Unlock()
	p := ExtractSlackPayload(object(object(e.Get("payload")).Get("event")), bot)
	if p == nil {
		return
	}
	r.mu.Lock()
	thread := ""
	if p.ThreadTs != nil {
		thread = *p.ThreadTs
	}
	r.threads[p.ChatID] = thread
	r.mu.Unlock()
	r.launch(func() {
		has := len(p.Files) > 0
		_ = r.Chat.HandleInbound(InboundChatMessage{ChatID: p.ChatID, UserID: raw(p.UserID), DisplayName: r.DisplayName(p.UserID), DedupKey: p.DedupKey, Text: p.Text, HasAttachments: &has, LoadAttachments: func() ([]protocol.JSONObject, error) { return r.CollectAttachments(p.ChatID, p.Files) }})
	})
}

type SlackResponse struct {
	mu               sync.Mutex
	runtime          *SlackRuntime
	chat, ts         string
	current, pending Text
	lastEdit         int64
}

func (s *SlackResponse) Append(delta Text) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(delta) == 0 {
		return nil
	}
	s.pending = s.pending.Join(delta)
	now := s.runtime.o.Now().UnixMilli()
	if s.ts != "" && now-s.lastEdit < 1200 {
		return nil
	}
	s.render(now)
	return nil
}
func (s *SlackResponse) Finish() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) > 0 {
		s.render(s.runtime.o.Now().UnixMilli())
	}
	return nil
}
func (s *SlackResponse) render(now int64) {
	displayed := Text{}
	if s.ts != "" {
		displayed = s.current
	}
	next := s.current.Join(s.pending)
	if len(next.Trim()) == 0 {
		s.pending = nil
		return
	}
	s.pending = nil
	chunks := SplitMessage(next, 3500)
	var err error
	for i, chunk := range chunks {
		if i == 0 && s.ts != "" {
			err = s.runtime.API.UpdateMessage(s.runtime.ctx, s.chat, s.ts, chunk)
		} else {
			s.ts, err = s.runtime.API.PostMessage(s.runtime.ctx, s.chat, chunk, s.runtime.Thread(s.chat))
		}
		if err != nil {
			break
		}
		if i != len(chunks)-1 {
			s.ts = ""
		}
	}
	if err != nil {
		s.runtime.o.Log("[Slack] Failed to render response: " + err.Error())
		s.ts = ""
		s.current = nil
		s.pending = next.Slice(len(displayed), len(next))
		return
	}
	s.current = chunks[len(chunks)-1]
	s.lastEdit = now
}
