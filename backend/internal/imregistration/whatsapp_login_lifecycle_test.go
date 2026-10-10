package imregistration

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	wa "github.com/NanmiCoder/cc-haha/backend/internal/whatsappcompat"
	"go.mau.fi/whatsmeow/types/events"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWhatsAppOriginalQRRotationIs60Then20AndStopsOnClose(t *testing.T) {
	type scheduled struct {
		delay    time.Duration
		fire     func()
		canceled bool
	}
	var mu sync.Mutex
	var timers []*scheduled
	var clients []*waTestLoginSocket
	s := NewWhatsAppLogin(WhatsAppLoginOptions{Schedule: func(delay time.Duration, fire func()) func() {
		entry := &scheduled{delay: delay, fire: fire}
		mu.Lock()
		timers = append(timers, entry)
		mu.Unlock()
		return func() { mu.Lock(); entry.canceled = true; mu.Unlock() }
	}, CreateSocket: func(_ context.Context, o wa.NodeWhatsAppOptions) (WhatsAppLoginSocket, error) {
		client := &waTestLoginSocket{options: o}
		client.connect = func(context.Context) error {
			o.OnEvent(&events.QR{Codes: []string{"first", "next", "last"}})
			return nil
		}
		mu.Lock()
		clients = append(clients, client)
		mu.Unlock()
		return client, nil
	}})
	start, e := s.Start(context.Background(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	for i, want := range []struct {
		qr    string
		delay time.Duration
	}{{"first", 60 * time.Second}, {"next", 20 * time.Second}, {"last", 20 * time.Second}} {
		poll, e := s.Poll(start.String("sessionKey"))
		if e != nil || poll.String("qr") != want.qr {
			t.Fatal(poll, e)
		}
		mu.Lock()
		timer := timers[i]
		mu.Unlock()
		if timer.delay != want.delay {
			t.Fatal("original SDK cadence", timer.delay, want.delay)
		}
		if i < 2 {
			timer.fire()
		}
	}
	if e = s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	last := timers[len(timers)-1]
	canceled := last.canceled
	mu.Unlock()
	if !canceled {
		t.Fatal("QR timer escaped actual close")
	}
	last.fire()
	mu.Lock()
	count := len(clients)
	mu.Unlock()
	if count != 1 {
		t.Fatal("canceled timer started socket", count)
	}
}
func TestWhatsAppConnectedAndPollWaitActualCredentialBarrierBeforeDeletion(t *testing.T) {
	saveStarted, release := make(chan struct{}, 2), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	var phase atomic.Int32
	var socket *waTestLoginSocket
	s := NewWhatsAppLogin(WhatsAppLoginOptions{CreateSocket: func(_ context.Context, o wa.NodeWhatsAppOptions) (WhatsAppLoginSocket, error) {
		socket = &waTestLoginSocket{options: o, save: func(ctx context.Context) error {
			if phase.Load() != 0 {
				saveStarted <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		}}
		return socket, nil
	}})
	defer s.Close(context.Background())
	start, e := s.Start(context.Background(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	socket.options.OnEvent(&events.Connected{})
	s.mu.Lock()
	session := s.sessions[start.String("sessionKey")]
	s.mu.Unlock()
	settled := make(chan struct{})
	go func() {
		for {
			s.mu.Lock()
			ready := session.connected
			s.mu.Unlock()
			if ready {
				close(settled)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	waWait(t, settled)
	phase.Store(1)
	result := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			poll, e := s.Poll(start.String("sessionKey"))
			if e == nil && string(poll.Get("connected")) != "true" {
				e = errors.New("concurrent connected poll became expired before save barrier")
			}
			result <- e
		}()
	}
	waWait(t, saveStarted)
	waWait(t, saveStarted)
	s.mu.Lock()
	present := s.sessions[start.String("sessionKey")] != nil
	s.mu.Unlock()
	if !present {
		t.Fatal("deleted before original waitSave stage")
	}
	unblock()
	for i := 0; i < 2; i++ {
		select {
		case e := <-result:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(time.Second):
			t.Fatal("poll did not settle")
		}
	}
}
func TestWhatsAppStartupStaleDirectoryCleanupStrictBoundaryAndSymlinkProtection(t *testing.T) {
	config := filepath.Join(t.TempDir(), ".claude")
	root := filepath.Join(config, "whatsapp-auth")
	_ = os.MkdirAll(root, 0700)
	now := time.Unix(1000, 0)
	outside := filepath.Join(filepath.Dir(config), "protected")
	_ = os.MkdirAll(outside, 0700)
	_ = os.WriteFile(filepath.Join(outside, "private"), []byte("KEEP"), 0600)
	for _, row := range []struct {
		name string
		age  time.Duration
	}{{".login-old", 3*time.Minute + time.Millisecond}, {".login-boundary", 3 * time.Minute}, {".backup-old", 4 * time.Minute}, {"default", 4 * time.Minute}} {
		path := filepath.Join(root, row.name)
		_ = os.Mkdir(path, 0700)
		_ = os.Chtimes(path, now.Add(-row.age), now.Add(-row.age))
	}
	link := filepath.Join(root, ".login-link")
	if e := os.Symlink(outside, link); e != nil {
		t.Fatal(e)
	}
	s, e := NewWhatsApp(WhatsAppOptions{ConfigDir: config, Login: WhatsAppLoginOptions{Now: func() time.Time { return now }}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	if _, e = os.Stat(filepath.Join(root, ".login-old")); !os.IsNotExist(e) {
		t.Fatal("stale login directory retained", e)
	}
	for _, name := range []string{".login-boundary", ".backup-old", "default", ".login-link"} {
		if _, e = os.Lstat(filepath.Join(root, name)); e != nil {
			t.Fatal("protected/boundary entry removed", name, e)
		}
	}
	if e = s.remove(link); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Lstat(link); e != nil {
		t.Fatal("cleanup followed/deleted symlink", e)
	}
	data, e := os.ReadFile(filepath.Join(outside, "private"))
	if e != nil || string(data) != "KEEP" {
		t.Fatal("external state changed", e)
	}
}

func TestWhatsAppCloseJoinsAlreadyAcceptedLogoutAndSharesDeadlineFuture(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var client *waTestLoginSocket
	s := NewWhatsAppLogin(WhatsAppLoginOptions{CreateSocket: func(_ context.Context, o wa.NodeWhatsAppOptions) (WhatsAppLoginSocket, error) {
		client = &waTestLoginSocket{options: o}
		return client, nil
	}})
	dir := t.TempDir()
	if _, e := s.Start(context.Background(), dir); e != nil {
		t.Fatal(e)
	}
	client.close = func(context.Context) error { once.Do(func() { close(entered) }); <-release; return nil }
	logoutDone := make(chan error, 1)
	go func() { logoutDone <- s.Logout(dir) }()
	waWait(t, entered)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if e := s.Close(ctx); !errors.Is(e, context.DeadlineExceeded) {
		close(release)
		<-logoutDone
		t.Fatalf("Close returned before accepted Logout drained: %v", e)
	}
	joined := make(chan error, 1)
	go func() { joined <- s.Close(context.Background()) }()
	select {
	case e := <-joined:
		close(release)
		<-logoutDone
		t.Fatalf("second Close bypassed same cleanup future: %v", e)
	default:
	}
	close(release)
	if e := <-logoutDone; e != nil {
		t.Fatal(e)
	}
	if e := <-joined; e != nil {
		t.Fatal(e)
	}
}

func TestWhatsAppQRPresentationFailureMatchesOriginalSuccessfulStart(t *testing.T) {
	qr := strings.Repeat("z", 5000)
	out := waSourceCommand(t, "whatsapp-api.fixture.ts", map[string]any{"qr": qr, "actions": []any{map[string]any{"tail": "login/start"}}})
	var source struct {
		Rows []struct {
			Status int
			Body   map[string]any
		}
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(out, &source); e != nil || len(source.Rows) != 1 || len(source.Capabilities.Blocked) != 0 {
		t.Fatalf("original QR %v %s", e, out)
	}
	if source.Rows[0].Status != 200 {
		t.Fatal("original QR failure should be caught", source.Rows)
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	s, e := NewWhatsApp(WhatsAppOptions{ConfigDir: config, HomeDir: home, WorkDir: home, Environment: map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config}, Login: WhatsAppLoginOptions{CreateSocket: func(_ context.Context, o wa.NodeWhatsAppOptions) (WhatsAppLoginSocket, error) {
		c := &waTestLoginSocket{options: o}
		c.connect = func(context.Context) error { o.OnEvent(&events.QR{Codes: []string{qr}}); return nil }
		return c, nil
	}}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	result, e := s.Begin(context.Background())
	if e != nil {
		t.Fatalf("original start succeeded without qrDataUrl, Go failed: %v", e)
	}
	var got map[string]any
	data, _ := result.MarshalJSON()
	_ = json.Unmarshal(data, &got)
	delete(got, "sessionKey")
	delete(source.Rows[0].Body, "sessionKey")
	if !reflect.DeepEqual(got, source.Rows[0].Body) {
		t.Fatalf("original QR response differs Go%v Source%v", got, source.Rows[0].Body)
	}
}

func TestWhatsAppConcurrentConnectedPollKeepsOriginalUncancelledSavePromise(t *testing.T) {
	out := waSourceCommand(t, "whatsapp-protocol.fixture.ts", map[string]any{"rows": []any{map[string]any{"initial": "open", "actions": []any{map[string]any{"kind": "pollPair"}}}}})
	var source struct {
		Rows         []struct{ Results []protocol.JSONObject }
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(out, &source); e != nil || len(source.Rows) != 1 || len(source.Rows[0].Results) != 2 || len(source.Capabilities.Blocked) != 0 {
		t.Fatalf("original concurrent poll %v %s", e, out)
	}
	var phase atomic.Bool
	var count atomic.Int32
	entered := make(chan struct{}, 2)
	first, second := make(chan struct{}), make(chan struct{})
	var once1, once2 sync.Once
	defer once1.Do(func() { close(first) })
	defer once2.Do(func() { close(second) })
	var socket *waTestLoginSocket
	s := NewWhatsAppLogin(WhatsAppLoginOptions{CreateSocket: func(_ context.Context, o wa.NodeWhatsAppOptions) (WhatsAppLoginSocket, error) {
		socket = &waTestLoginSocket{options: o, save: func(ctx context.Context) error {
			if !phase.Load() {
				return nil
			}
			gate := first
			if count.Add(1) == 2 {
				gate = second
			}
			entered <- struct{}{}
			select {
			case <-gate:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}
		socket.connect = func(context.Context) error { o.OnEvent(&events.Connected{}); return nil }
		return socket, nil
	}})
	defer s.Close(context.Background())
	directory := t.TempDir()
	start, e := s.Start(context.Background(), directory)
	if e != nil {
		t.Fatal(e)
	}
	phase.Store(true)
	type result struct {
		value protocol.JSONObject
		err   error
	}
	results := make(chan result, 2)
	go func() { value, e := s.Poll(start.String("sessionKey")); results <- result{value, e} }()
	waWait(t, entered)
	go func() { value, e := s.Poll(start.String("sessionKey")); results <- result{value, e} }()
	waWait(t, entered)
	once1.Do(func() { close(first) })
	a := <-results
	once2.Do(func() { close(second) })
	b := <-results
	for i, r := range []result{a, b} {
		if r.err != nil {
			t.Fatalf("poll%d was canceled by another successful poll, unlike Source: %v", i, r.err)
		}
		if r.value.String("authDir") != directory {
			t.Fatal("actual connected directory changed", r.value.String("authDir"))
		}
		want := source.Rows[0].Results[i].Clone()
		r.value.Set("authDir", want.Get("authDir"))
		actual, _ := r.value.MarshalJSON()
		original, _ := want.MarshalJSON()
		var av, wv any
		_ = json.Unmarshal(actual, &av)
		_ = json.Unmarshal(original, &wv)
		if !reflect.DeepEqual(av, wv) {
			t.Fatalf("poll%d original result mismatch Go%s Source%s", i, actual, original)
		}
	}
}

type waHeldPromoteFiles struct {
	WhatsAppFiles
	entered, release chan struct{}
	once             sync.Once
}

func (f *waHeldPromoteFiles) Rename(from, to string) error {
	if strings.Contains(filepath.Base(from), ".login-") {
		f.once.Do(func() { close(f.entered) })
		<-f.release
	}
	return f.WhatsAppFiles.Rename(from, to)
}
func TestWhatsAppRegistrationCloseJoinsPhysicalPromotionBeforeReturning(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	files := &waHeldPromoteFiles{WhatsAppFiles: nativeWhatsAppFiles{}, entered: make(chan struct{}), release: make(chan struct{})}
	var unblock sync.Once
	release := func() { unblock.Do(func() { close(files.release) }) }
	s, e := NewWhatsApp(WhatsAppOptions{Files: files, ConfigDir: config, HomeDir: home, WorkDir: home, Environment: map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config}, Login: WhatsAppLoginOptions{CreateSocket: func(_ context.Context, o wa.NodeWhatsAppOptions) (WhatsAppLoginSocket, error) {
		if e := os.MkdirAll(o.AuthDir, 0700); e != nil {
			return nil, e
		}
		if e := os.WriteFile(filepath.Join(o.AuthDir, "creds.json"), []byte(`{"physicalPrivate":true}`), 0600); e != nil {
			return nil, e
		}
		c := &waTestLoginSocket{options: o}
		c.connect = func(context.Context) error { o.OnEvent(&events.Connected{}); return nil }
		return c, nil
	}}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	defer release()
	start, e := s.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	result := make(chan error, 1)
	go func() { _, e := s.Poll(start.String("sessionKey")); result <- e }()
	waWait(t, files.entered)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if e = s.Close(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("Close returned before accepted physical Rename: %v", e)
	}
	joined := make(chan error, 1)
	go func() { joined <- s.Close(context.Background()) }()
	select {
	case e := <-joined:
		t.Fatalf("second Close released pending Rename: %v", e)
	default:
	}
	release()
	if e := <-result; e != nil {
		t.Fatal(e)
	}
	if e := <-joined; e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(filepath.Join(s.DefaultAuthDir(), "creds.json"))
	if e != nil || string(data) != `{"physicalPrivate":true}` {
		t.Fatalf("physical promotion failed %s %v", data, e)
	}
}

func TestWhatsAppRestartFailureMatchesOriginalWarningAndTerminalPoll(t *testing.T) {
	out := waSourceCommand(t, "whatsapp-protocol.fixture.ts", map[string]any{"rows": []any{map[string]any{"failAt": 1, "actions": []any{map[string]any{"kind": "event", "status": 515}, map[string]any{"kind": "poll"}}}}})
	var source struct {
		Rows []struct {
			Results  []protocol.JSONObject
			Warnings []string
		}
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(out, &source); e != nil || len(source.Rows) != 1 || len(source.Rows[0].Warnings) != 1 || len(source.Capabilities.Blocked) != 0 {
		t.Fatalf("original warning %v %s", e, out)
	}
	var count atomic.Int32
	var socket *waTestLoginSocket
	warning := make(chan error, 1)
	s := NewWhatsAppLogin(WhatsAppLoginOptions{Warn: func(e error) { warning <- e }, CreateSocket: func(_ context.Context, o wa.NodeWhatsAppOptions) (WhatsAppLoginSocket, error) {
		if count.Add(1) > 1 {
			return nil, errors.New("private restart failure")
		}
		socket = &waTestLoginSocket{options: o}
		return socket, nil
	}})
	defer s.Close(context.Background())
	start, e := s.Start(context.Background(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	socket.options.OnEvent(&events.ManualLoginReconnect{})
	select {
	case e := <-warning:
		if "[WhatsApp] Login socket restart failed: "+e.Error() != source.Rows[0].Warnings[0] {
			t.Fatal("warning differs", e, source.Rows[0].Warnings)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("restart warning absent")
	}
	result, e := s.Poll(start.String("sessionKey"))
	if e != nil {
		t.Fatal(e)
	}
	actual, _ := result.MarshalJSON()
	want, _ := source.Rows[0].Results[0].MarshalJSON()
	var av, wv any
	_ = json.Unmarshal(actual, &av)
	_ = json.Unmarshal(want, &wv)
	if !reflect.DeepEqual(av, wv) {
		t.Fatalf("restart failure differs Go%s Source%s", actual, want)
	}
}
