package mcptools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type asyncSDKAttemptPeer struct {
	mu            sync.Mutex
	input         io.WriteCloser
	pending       map[string]chan json.RawMessage
	notifications []chan json.RawMessage
	held          chan struct{}
	closed        chan struct{}
	counts        sdkAttemptCounts
	failure       error
}

func startAsyncSDKAttemptPeer(t *testing.T, ctx context.Context, home string) *asyncSDKAttemptPeer {
	t.Helper()
	fixture, _ := filepath.Abs("../../testdata/native-sdk-mcp-attempt-peer.fixture.ts")
	command := sdkAttemptCommand(ctx, home, fixture, "async")
	input, e := command.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	output, e := command.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	diagnostics, e := command.StderrPipe()
	if e != nil {
		t.Fatal(e)
	}
	p := &asyncSDKAttemptPeer{input: input, pending: map[string]chan json.RawMessage{}, held: make(chan struct{}), closed: make(chan struct{})}
	if e = command.Start(); e != nil {
		t.Fatal(e)
	}
	var ready, stderrDone = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stderrDone)
		scanner := bufio.NewScanner(diagnostics)
		for scanner.Scan() {
			switch scanner.Text() {
			case "READY":
				close(ready)
			case "HELD":
				close(p.held)
			case "CLOSED":
			default:
				p.mu.Lock()
				p.failure = errors.New(scanner.Text())
				p.mu.Unlock()
			}
		}
	}()
	go func() {
		defer close(p.closed)
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			data := append(json.RawMessage(nil), scanner.Bytes()...)
			var message protocol.JSONObject
			if e := message.UnmarshalJSON(data); e != nil {
				p.mu.Lock()
				p.failure = e
				p.mu.Unlock()
				break
			}
			p.mu.Lock()
			id := string(message.Get("id"))
			var reply chan json.RawMessage
			if id == "" {
				if len(p.notifications) > 0 {
					reply, p.notifications = p.notifications[0], p.notifications[1:]
				}
			} else {
				reply = p.pending[id]
				delete(p.pending, id)
			}
			p.mu.Unlock()
			if reply != nil {
				reply <- data
			}
		}
		p.mu.Lock()
		for _, reply := range p.pending {
			close(reply)
		}
		for _, reply := range p.notifications {
			close(reply)
		}
		p.mu.Unlock()
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("actual peer readiness", ctx.Err())
	}
	t.Cleanup(func() {
		_ = input.Close()
		if e := command.Wait(); e != nil {
			t.Error("actual async SDK peer exit", e)
		}
		<-p.closed
		<-stderrDone
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.failure != nil {
			t.Error(p.failure)
		}
	})
	return p
}
func (p *asyncSDKAttemptPeer) send(_ context.Context, packet protocol.JSONObject) (json.RawMessage, error) {
	p.mu.Lock()
	switch packet.String("method") {
	case "initialize":
		p.counts.Initialize++
	case "tools/list":
		p.counts.List++
	case "tools/call":
		p.counts.Call++
	default:
		p.counts.Notifications++
	}
	response := make(chan json.RawMessage, 1)
	id := string(packet.Get("id"))
	if id == "" {
		p.notifications = append(p.notifications, response)
	} else {
		if p.pending[id] != nil {
			p.mu.Unlock()
			return nil, errors.New("physical peer received concurrent duplicate SDK RPC id")
		}
		p.pending[id] = response
	}
	line, e := protocol.EncodeNDJSON(packet)
	if e == nil {
		_, e = p.input.Write(line)
	}
	p.mu.Unlock()
	if e != nil {
		return nil, e
	}
	data, ok := <-response
	if !ok {
		return nil, io.EOF
	}
	return data, nil
}
func (p *asyncSDKAttemptPeer) release() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, e := p.input.Write([]byte("{\"fixtureRelease\":true}\n"))
	return e
}
func sdkRuntimeForPhysicalPeer(t *testing.T, home string, send func(context.Context, protocol.JSONObject) (json.RawMessage, error)) *mcpruntime.Service {
	t.Helper()
	s, e := mcpruntime.New(mcpruntime.Options{CWD: home, Environment: map[string]string{}, Version: "fixture", Auth: func(context.Context, string, protocol.JSONObject) (mcpclient.AuthProvider, error) { return nil, nil }, Enabled: func(context.Context, string, protocol.JSONObject) (bool, error) { return true, nil }, Authenticate: func(context.Context, string, protocol.JSONObject, func(string) error) error {
		return errors.New("no auth in SDK fixture")
	}, ClearOAuth: func(context.Context, string, protocol.JSONObject) error { return nil }, SDK: func(context.Context, string, protocol.JSONObject) (*mcpclient.SDKControlBindings, error) {
		return &mcpclient.SDKControlBindings{Send: send}, nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := s.Close(context.Background()); e != nil {
			t.Error(e)
		}
	})
	return s
}
func TestSDKFreshHandshakePreservesHeldOldPeerAndBorrowedNamespaceClose(t *testing.T) {
	if os.Getenv("CC_HAHA_SOURCE_BUN") == "" {
		t.Skip("pinned SDK peer required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	home := sdkAttemptHome(t)
	peer := startAsyncSDKAttemptPeer(t, ctx, home)
	root := sdkRuntimeForPhysicalPeer(t, home, peer.send)
	config := object(raw(map[string]any{"type": "sdk", "name": "async", "scope": "dynamic"}))
	first, e := root.ConnectSDKAttempt(ctx, "async", config)
	if e != nil {
		t.Fatal(e)
	}
	result := make(chan error, 1)
	go func() {
		_, e := first.Client.CallTool(ctx, "physical", object(raw(map[string]string{"text": "HELD_PHYSICAL_WRITE"})), protocol.NewJSONObject(), mcpclient.RequestOptions{})
		result <- e
	}()
	select {
	case <-peer.held:
	case <-ctx.Done():
		t.Fatal("actual first SDK call did not enter physical held handler")
	}
	second, e := root.ConnectSDKAttempt(ctx, "async", config)
	if e != nil || second.Client == first.Client {
		t.Fatal("held original prevented fresh SDK handshake", e)
	}
	select {
	case e := <-result:
		t.Fatal("rebuild aborted original held call", e)
	default:
	}
	candidate := sdkRuntimeForPhysicalPeer(t, sdkAttemptHome(t), peer.send)
	view, e := root.ShareNamespace(candidate)
	if e != nil {
		t.Fatal(e)
	}
	borrowed, e := view.RefreshConnection(ctx, second.ExecutionConnection())
	if e != nil || borrowed.Client != first.Client {
		t.Fatal("cached Source Tool closure rebound to current/child peer", e)
	}
	if e = view.Close(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-first.Client.Done():
		t.Fatal("borrowed worker Close retired parent SDK peer")
	default:
	}
	foreign := sdkRuntimeForPhysicalPeer(t, sdkAttemptHome(t), peer.send)
	if _, e = foreign.RefreshConnection(ctx, first); e == nil {
		t.Fatal("foreign namespace accepted minted peer")
	}
	if e = foreign.CloseConnection(ctx, first); e == nil {
		t.Fatal("foreign namespace closed minted peer")
	}
	if e = root.CloseConnection(ctx, second); e != nil {
		t.Fatal(e)
	}
	select {
	case <-second.Client.Done():
	default:
		t.Fatal("current attempt did not close")
	}
	select {
	case <-first.Client.Done():
		t.Fatal("current peer cleanup also killed old cache/child execution peer")
	default:
	}
	if e = peer.release(); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-result:
		if e != nil {
			t.Fatal("retained original call failed after replacement/current cleanup", e)
		}
	case <-ctx.Done():
		t.Fatal("held physical call failed to finish")
	}
	physical, e := os.ReadFile(filepath.Join(home, "async.txt"))
	if e != nil || string(physical) != "HELD_PHYSICAL_WRITE" {
		t.Fatal("physical retained write", string(physical), e)
	}
	if e = root.Close(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-first.Client.Done():
	default:
		t.Fatal("Root Close missed superseded attempt")
	}
	if e = root.CloseConnection(ctx, first); e != nil {
		t.Fatal("actual repeated retired-peer cleanup rejected", e)
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if peer.counts.Initialize != 2 || peer.counts.List != 1 {
		t.Fatal("actual Source fresh-handshake/cached-list count", peer.counts)
	}
}
func TestSDKLatestNotificationClientAndOwnerProof(t *testing.T) {
	if os.Getenv("CC_HAHA_SOURCE_BUN") == "" {
		t.Skip("pinned SDK peer required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	expected := sdkAttemptOriginal(t, ctx)
	home := sdkAttemptHome(t)
	peer := startAsyncSDKAttemptPeer(t, ctx, home)
	root := sdkRuntimeForPhysicalPeer(t, home, peer.send)
	config := object(raw(map[string]string{"type": "sdk", "name": "async", "scope": "dynamic"}))
	first, e := root.ConnectSDKAttempt(ctx, "async", config)
	if e != nil {
		t.Fatal(e)
	}
	latest, e := root.ConnectSDKAttempt(ctx, "async", config)
	if e != nil {
		t.Fatal(e)
	}
	var oldNotices, newNotices int
	_, e = first.Client.SubscribeNotifications(func(string, protocol.JSONObject) { oldNotices++ })
	if e != nil {
		t.Fatal(e)
	}
	_, e = latest.Client.SubscribeNotifications(func(string, protocol.JSONObject) { newNotices++ })
	if e != nil {
		t.Fatal(e)
	}
	owner := cooperative.New()
	defer owner.Close()
	s := &Service{options: Options{Owner: owner}, connections: []mcpruntime.Connection{latest}}
	notice := object(raw(map[string]any{"jsonrpc": "2.0", "method": "notifications/tools/list_changed", "params": map[string]any{"_meta": map[string]string{"actual": "LATEST_ONLY"}}}))
	if e = owner.Run(ctx, func(scope *cooperative.Scope) error { return s.DeliverSDKMessage(scope, "async", notice) }); e != nil {
		t.Fatal(e)
	}
	if oldNotices != expected.NoticeCounts.Old || newNotices != expected.NoticeCounts.Current {
		t.Fatal("notification used cached execution peer instead of latest SDK client", oldNotices, newNotices)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	e = foreign.Run(ctx, func(scope *cooperative.Scope) error { return s.DeliverSDKMessage(scope, "async", notice) })
	if !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal("foreign owner delivered notification", e)
	}
	if _, e = root.ConnectSDKAttempt(ctx, "ordinary", object(raw(map[string]string{"type": "http", "url": "http://127.0.0.1:1"}))); e == nil {
		t.Fatal("fresh SDK API accepted normal transport")
	}
}
