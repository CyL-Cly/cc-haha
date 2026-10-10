package whatsappcandidate

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"go.mau.fi/libsignal/session"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWa6"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

type NodeWhatsAppOptions struct {
	AuthDir              string
	DisableAutoReconnect bool
	HTTPTransport        http.RoundTripper
	Now                  func() time.Time
	OnEvent              func(any)
	Log                  waLog.Logger
	Warn                 func(error)
	ImageThumbnail       func([]byte) (NodeImageThumbnail, error)
}

// NodeWhatsAppClient uses the real pinned Noise and Signal client with the
// existing Baileys directory. HTTPTransport is a low-level transport seam:
// URLs and protocol endpoints are always selected by the actual library.
type NodeWhatsAppClient struct {
	Client               *whatsmeow.Client
	Store                *NodeDeviceStore
	ctx                  context.Context
	cancel               context.CancelFunc
	mu                   sync.Mutex
	closed               bool
	terminal             bool
	disableAutoReconnect bool
	reconnect            *time.Timer
	attempts             uint
	wg                   sync.WaitGroup
	onEvent              func(any)
	payloadVersion       [3]uint32
	http                 *http.Client
	connectionStops      []func()
	imageThumbnail       func([]byte) (NodeImageThumbnail, error)
	now                  func() time.Time
	mediaRetries         map[string][]chan *events.MediaRetry
}

func NewNodeWhatsAppClient(ctx context.Context, o NodeWhatsAppOptions) (*NodeWhatsAppClient, error) {
	device, storage, e := OpenNodeDevice(o.AuthDir, o.Now)
	if e != nil {
		return nil, e
	}
	storage.Warn = o.Warn
	lifetime, cancel := context.WithCancel(ctx)
	n := &NodeWhatsAppClient{Store: storage, ctx: session.WithNodeSessionTransactions(lifetime, storage.AcquireSessions), cancel: cancel, onEvent: o.OnEvent, disableAutoReconnect: o.DisableAutoReconnect, imageThumbnail: o.ImageThumbnail, now: o.Now, mediaRetries: map[string][]chan *events.MediaRetry{}}
	if n.now == nil {
		n.now = time.Now
	}
	n.Client = whatsmeow.NewClient(device, o.Log)
	n.http = &http.Client{Transport: o.HTTPTransport}
	n.Client.GetClientPayload = func() *waWa6.ClientPayload {
		n.mu.Lock()
		version := n.payloadVersion
		n.mu.Unlock()
		return NodeClientPayload(n.Client.Store, version)
	}
	n.Client.BackgroundEventCtx = n.ctx
	n.Client.EnableAutoReconnect = false
	n.Client.InitialAutoReconnect = false
	n.Client.DisableLoginAutoReconnect = o.DisableAutoReconnect
	if o.HTTPTransport != nil {
		client := &http.Client{Transport: o.HTTPTransport}
		n.Client.SetPreLoginHTTPClient(client)
		n.Client.SetWebsocketHTTPClient(client)
		n.Client.SetMediaHTTPClient(client)
	}
	n.Client.AddEventHandler(n.event)
	return n, nil
}
func (n *NodeWhatsAppClient) prepareConnection(ctx context.Context) (context.Context, func(), error) {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return nil, nil, errors.New("WhatsApp client closed")
	}
	n.wg.Add(1)
	if n.reconnect != nil {
		n.reconnect.Stop()
		n.reconnect = nil
	}
	n.mu.Unlock()
	callCtx, cancel := context.WithCancel(ctx)
	callCtx = session.WithNodeSessionTransactions(callCtx, n.Store.AcquireSessions)
	stop := context.AfterFunc(n.ctx, cancel)
	n.mu.Lock()
	n.connectionStops = append(n.connectionStops, func() { stop(); cancel() })
	n.mu.Unlock()
	version := fetchNodeVersion(callCtx, n.http)
	n.mu.Lock()
	n.payloadVersion = version
	closed := n.closed
	n.mu.Unlock()
	if closed {
		stop()
		cancel()
		n.wg.Done()
		return nil, nil, errors.New("WhatsApp client closed")
	}
	return callCtx, func() { stop(); cancel() }, nil
}

// Connect retains the ordinary runtime's synchronous dial/Noise contract.
func (n *NodeWhatsAppClient) Connect(ctx context.Context) error {
	callCtx, stop, err := n.prepareConnection(ctx)
	if err != nil {
		return err
	}
	defer n.wg.Done()
	err = n.Client.ConnectContext(callCtx)
	if err != nil {
		stop()
	}
	return err
}

// StartLoginConnection matches createWhatsAppSocket: auth/version preparation
// is awaited, while the socket connection itself starts without awaiting the
// WSS/Noise handshake. The existing client owns and joins that work on Close.
func (n *NodeWhatsAppClient) StartLoginConnection(ctx context.Context, onError func(error)) error {
	callCtx, stop, err := n.prepareConnection(ctx)
	if err != nil {
		return err
	}
	if callCtx.Err() != nil {
		cause := context.Cause(callCtx)
		stop()
		n.wg.Done()
		return cause
	}
	go func() {
		defer n.wg.Done()
		if err := n.Client.ConnectContext(callCtx); err != nil {
			active := callCtx.Err() == nil
			stop()
			if active && onError != nil {
				onError(err)
			}
		}
	}()
	return nil
}

func (n *NodeWhatsAppClient) event(event any) {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return
	}
	n.wg.Add(1)
	n.mu.Unlock()
	defer n.wg.Done()
	n.deliverMediaRetry(event)
	switch event.(type) {
	case *events.Connected:
		n.mu.Lock()
		n.attempts = 0
		n.mu.Unlock()
	case *events.Disconnected:
		if !n.disableAutoReconnect {
			n.scheduleReconnect()
		}
	case *events.LoggedOut:
		n.mu.Lock()
		n.terminal = true
		if n.reconnect != nil {
			n.reconnect.Stop()
			n.reconnect = nil
		}
		n.mu.Unlock()
	}
	if n.onEvent != nil {
		n.onEvent(event)
	}
}
func (n *NodeWhatsAppClient) scheduleReconnect() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed || n.terminal || n.reconnect != nil {
		return
	}
	delay := time.Second
	for i := uint(0); i < n.attempts && delay < 30*time.Second; i++ {
		delay *= 2
	}
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	n.attempts++
	n.reconnect = time.AfterFunc(delay, func() {
		n.mu.Lock()
		n.reconnect = nil
		if n.closed {
			n.mu.Unlock()
			return
		}
		n.wg.Add(1)
		n.mu.Unlock()
		defer n.wg.Done()
		version := fetchNodeVersion(n.ctx, n.http)
		n.mu.Lock()
		n.payloadVersion = version
		n.mu.Unlock()
		if e := n.Client.ConnectContext(n.ctx); e != nil && n.ctx.Err() == nil {
			n.scheduleReconnect()
		}
	})
}
func (n *NodeWhatsAppClient) Close(ctx context.Context) error {
	n.mu.Lock()
	if !n.closed {
		n.closed = true
		if n.reconnect != nil {
			n.reconnect.Stop()
			n.reconnect = nil
		}
		n.cancel()
		for _, stop := range n.connectionStops {
			stop()
		}
		n.connectionStops = nil
	}
	n.mu.Unlock()
	n.Client.Disconnect()
	settled := make(chan struct{})
	go func() { n.wg.Wait(); close(settled) }()
	select {
	case <-settled:
		return n.Store.WaitCredsSave(ctx)
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (n *NodeWhatsAppClient) SendText(ctx context.Context, to types.JID, text string) (whatsmeow.SendResponse, error) {
	return n.Client.SendMessage(session.WithNodeSessionTransactions(ctx, n.Store.AcquireSessions), to, &waE2E.Message{Conversation: proto.String(text)})
}
func (n *NodeWhatsAppClient) ReadAndCompose(ctx context.Context, info types.MessageInfo) {
	_ = n.Client.MarkRead(ctx, []types.MessageID{info.ID}, time.Now(), info.Chat, info.Sender)
	_ = n.Client.SendChatPresence(ctx, info.Chat, types.ChatPresenceComposing, types.ChatPresenceMediaText)
}
