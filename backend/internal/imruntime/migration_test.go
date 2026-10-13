package imruntime

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	wa "github.com/NanmiCoder/cc-haha/backend/internal/whatsappcompat"
)

type deniedMigrationHTTP struct{ calls *atomic.Int32 }

func (d deniedMigrationHTTP) RoundTrip(*http.Request) (*http.Response, error) {
	d.calls.Add(1)
	return nil, errors.New("external messaging disabled in fixture")
}

func TestMigrationAllEightActualRuntimeConstructorsJoinOwnersAndRelocatedStores(t *testing.T) {
	for _, platform := range []string{"feishu", "telegram", "wechat", "dingtalk", "whatsapp", "wecom", "qq", "slack"} {
		t.Run(platform, func(t *testing.T) {
			home := t.TempDir()
			work := filepath.Join(home, "work")
			config := filepath.Join(home, "relocated")
			if err := os.MkdirAll(work, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(config, 0700); err != nil {
				t.Fatal(err)
			}
			cfg := Config{Home: home, ConfigDir: config, WorkDir: work, ServerURL: "ws://127.0.0.1:1", Token: "fake", Environment: map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config}, Document: object(raw(map[string]any{"feishu": map[string]string{"appId": "fake", "appSecret": "fake"}, "telegram": map[string]string{"botToken": "fake"}, "wechat": map[string]string{"accountId": "fake", "botToken": "fake"}, "dingtalk": map[string]string{"clientId": "fake", "clientSecret": "fake"}, "qq": map[string]string{"appId": "fake", "appSecret": "fake"}, "wecom": map[string]string{"botId": "fake", "secret": "fake"}, "slack": map[string]string{"botToken": "fake", "appToken": "fake"}}))}
			var calls atomic.Int32
			client := &http.Client{Transport: deniedMigrationHTTP{&calls}}
			tlsConfig := &tls.Config{}
			var life *MigrationLifecycle
			var attachments *AttachmentStore
			var store *SessionStore
			var stop func(context.Context) error
			var err error
			switch platform {
			case "feishu":
				var r *FeishuRuntime
				r, err = NewFeishuRuntime(context.Background(), FeishuRuntimeOptions{Config: cfg, Credentials: object(raw(map[string]string{"appId": "fake", "appSecret": "fake"})), ServerHTTP: client, SocketTLS: tlsConfig})
				if err == nil {
					life, attachments, store, stop = r.migration, r.Attachments, r.Store, r.Quiesce
				}
			case "telegram":
				var r *TelegramRuntime
				r, err = NewTelegramRuntime(context.Background(), TelegramRuntimeOptions{Config: cfg, ServerHTTP: client, MediaHTTP: client})
				if err == nil {
					life, attachments, store, stop = r.migration, r.Media.Store, r.Store, r.Quiesce
				}
			case "wechat":
				var r *WechatRuntime
				r, err = NewWechatRuntime(context.Background(), WechatRuntimeOptions{Config: cfg, ServerHTTP: client})
				if err == nil {
					life, attachments, store, stop = r.migration, r.Attachments, r.Store, r.Quiesce
				}
			case "dingtalk":
				var r *DingTalkRuntime
				r, err = NewDingTalkRuntime(context.Background(), DingTalkRuntimeOptions{Config: cfg, ServerHTTP: client, SocketTLS: tlsConfig})
				if err == nil {
					life, attachments, store, stop = r.migration, r.Attachments, r.Store, r.Quiesce
				}
			case "wecom":
				var r *WecomRuntime
				r, err = NewWecomRuntime(context.Background(), WecomRuntimeOptions{Config: cfg, ServerHTTP: client, SocketTLS: tlsConfig})
				if err == nil {
					life, attachments, store, stop = r.migration, r.Attachments, r.Store, r.Quiesce
				}
			case "qq":
				var r *QQRuntime
				r, err = NewQQRuntime(context.Background(), QQRuntimeOptions{Config: cfg, ServerHTTP: client, SocketTLS: tlsConfig})
				if err == nil {
					life, attachments, store, stop = r.migration, r.Attachments, r.Store, r.Quiesce
				}
			case "slack":
				var r *SlackRuntime
				r, err = NewSlackRuntime(context.Background(), SlackRuntimeOptions{Config: cfg, ServerHTTP: client, SocketTLS: tlsConfig})
				if err == nil {
					life, attachments, store, stop = r.migration, r.Attachments, r.Store, r.Quiesce
				}
			case "whatsapp":
				dir := filepath.Join(config, "whatsapp-auth", "default")
				if err = os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				creds, e := wa.CreateNodeCredentials()
				if e != nil {
					t.Fatal(e)
				}
				data, _ := json.Marshal(creds)
				if e = os.WriteFile(filepath.Join(dir, "creds.json"), data, 0600); e != nil {
					t.Fatal(e)
				}
				var r *WhatsAppRuntime
				r, err = NewWhatsAppRuntime(context.Background(), WhatsAppRuntimeOptions{Config: cfg, ServerHTTP: client, SocketTLS: tlsConfig, ClientOptions: wa.NodeWhatsAppOptions{HTTPTransport: deniedMigrationHTTP{&calls}}})
				if err == nil {
					life, attachments, store, stop = r.migration, r.Attachments, r.Store, r.Quiesce
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if attachments.Root != filepath.Join(config, "im-downloads") || attachments.Lifecycle != life || store.Lifecycle != life {
				t.Fatal("runtime lost relocated lifecycle storage", attachments.Root)
			}
			if err = store.Set("chat", "same-sid", work); err != nil {
				t.Fatal(err)
			}
			target, err := attachments.ResolvePath(platform, "same-sid", "a.txt")
			if err != nil {
				t.Fatal(err)
			}
			if err = attachments.Write(target, []byte("actual preserved attachment")); err != nil {
				t.Fatal(err)
			}
			pending := life.Track()
			result := make(chan error, 1)
			go func() { result <- stop(context.Background()) }()
			for life.Available() {
				time.Sleep(time.Millisecond)
			}
			select {
			case err := <-result:
				t.Fatal("quiesce escaped accepted physical writer", err)
			case <-time.After(10 * time.Millisecond):
			}
			pending(nil)
			select {
			case err := <-result:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("native resources did not close")
			}
			if err = stop(context.Background()); err != nil {
				t.Fatal("repeat quiesce", err)
			}
			if store.Get("chat").SessionID != "same-sid" {
				t.Fatal("migration removed binding")
			}
			if calls.Load() != 0 {
				t.Fatal("constructor or close contacted external messaging", calls.Load())
			}
		})
	}
}

func TestMigrationLifecycleActualCredentialWriteFailureCannotAcknowledge(t *testing.T) {
	life := NewMigrationLifecycle()
	store := whatsAppOldStore(t, nil)
	store.Lifecycle = life
	path := filepath.Join(store.Dir, "creds.json")
	lock := whatsAppAuthFileLock(path)
	lock.Lock()
	if _, err := store.QueueSaveCreds(); err != nil {
		lock.Unlock()
		t.Fatal(err)
	}
	quiesced := make(chan error, 1)
	go func() { quiesced <- life.Quiesce(context.Background(), store.Close) }()
	for life.Available() {
		time.Sleep(time.Millisecond)
	}
	if err := os.Remove(path); err != nil {
		lock.Unlock()
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		lock.Unlock()
		t.Fatal(err)
	}
	lock.Unlock()
	if err := <-quiesced; err == nil {
		t.Fatal("failed physical creds write acknowledged")
	}
}

func TestMigrationValidationRejectsRuntimeBeforeAnyPlatformState(t *testing.T) {
	cfg := Config{Environment: map[string]string{"CC_HAHA_MIGRATION_VALIDATION": "1"}}
	for _, constructor := range []func() error{
		func() error {
			_, e := NewFeishuRuntime(context.Background(), FeishuRuntimeOptions{Config: cfg})
			return e
		},
		func() error {
			_, e := NewTelegramRuntime(context.Background(), TelegramRuntimeOptions{Config: cfg})
			return e
		},
		func() error {
			_, e := NewWechatRuntime(context.Background(), WechatRuntimeOptions{Config: cfg})
			return e
		},
		func() error {
			_, e := NewSlackRuntime(context.Background(), SlackRuntimeOptions{Config: cfg})
			return e
		},
		func() error {
			_, e := NewWhatsAppRuntime(context.Background(), WhatsAppRuntimeOptions{Config: cfg})
			return e
		},
		func() error { _, e := newPlatformRuntimeCore(context.Background(), cfg, nil, nil, nil, nil); return e },
	} {
		if e := constructor(); e == nil {
			t.Fatal("validation created native state")
		}
	}
}

func TestMigrationAdmissionJoinsAcceptedHandlersAndDeniesLaterIngress(t *testing.T) {
	life := NewMigrationLifecycle()
	done, admitted := life.Admit()
	if !admitted {
		t.Fatal("fresh platform ingress denied")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- life.Quiesce(context.Background(), func() {}) }()
	for life.Available() {
		time.Sleep(time.Millisecond)
	}
	if _, admitted := life.Admit(); admitted {
		t.Fatal("migration admitted new ingress")
	}
	select {
	case err := <-stopped:
		t.Fatal("migration escaped admitted handler", err)
	case <-time.After(10 * time.Millisecond):
	}
	done(nil)
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
}
