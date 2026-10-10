package imregistration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	wa "github.com/NanmiCoder/cc-haha/backend/internal/whatsappcompat"
	"go.mau.fi/whatsmeow/types/events"
)

type waProtocolAction struct {
	Kind   string `json:"kind"`
	Status *int   `json:"status,omitempty"`
	QR     string `json:"qr,omitempty"`
	Open   bool   `json:"open,omitempty"`
	MS     int64  `json:"ms,omitempty"`
}
type waProtocolRow struct {
	Initial string             `json:"initial,omitempty"`
	Actions []waProtocolAction `json:"actions"`
}
type waTestLoginSocket struct {
	mu      sync.Mutex
	options wa.NodeWhatsAppOptions
	save    func(context.Context) error
	connect func(context.Context) error
	close   func(context.Context) error
	closed  int
}

func (s *waTestLoginSocket) Connect(ctx context.Context) error {
	if s.connect != nil {
		return s.connect(ctx)
	}
	s.options.OnEvent(&events.QR{Codes: []string{"qr-1"}})
	return nil
}
func (s *waTestLoginSocket) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed++
	s.mu.Unlock()
	if s.close != nil {
		return s.close(ctx)
	}
	return nil
}
func (s *waTestLoginSocket) WaitCredsSave(ctx context.Context) error {
	if s.save != nil {
		return s.save(ctx)
	}
	return nil
}
func (s *waTestLoginSocket) AccountJID() string { return "222:3@s.whatsapp.net" }
func waSourceCommand(t *testing.T, fixture string, input any) []byte {
	t.Helper()
	home := t.TempDir()
	data, _ := json.Marshal(input)
	var in map[string]any
	_ = json.Unmarshal(data, &in)
	in["home"] = home
	data, _ = json.Marshal(in)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path, _ := filepath.Abs(filepath.Join("testdata", fixture))
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", path)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_REGISTRATION_FIXTURE=1", "NODE_ENV=test", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal("original WhatsApp", e, stderr.String())
	}
	return out
}
func waWait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("actual WhatsApp event absent")
	}
}
func TestWhatsAppLoginProtocolMatchesActualOriginalSessionConsumer(t *testing.T) {
	rows := []waProtocolRow{{Actions: []waProtocolAction{{Kind: "poll"}}}, {Initial: "open", Actions: []waProtocolAction{{Kind: "poll"}, {Kind: "poll"}}}, {Initial: "timeout", Actions: []waProtocolAction{{Kind: "poll"}}}}
	for _, status := range []int{515, 408, 500, 401} {
		v := status
		rows = append(rows, waProtocolRow{Actions: []waProtocolAction{{Kind: "event", Status: &v}, {Kind: "poll"}, {Kind: "event", Open: true}, {Kind: "poll"}}})
	}
	for _, ms := range []int64{120000, 120001} {
		rows = append(rows, waProtocolRow{Actions: []waProtocolAction{{Kind: "advance", MS: ms}, {Kind: "poll"}}})
	}
	rows = append(rows, waProtocolRow{Actions: []waProtocolAction{{Kind: "logout"}, {Kind: "poll"}}})
	v := 515
	limit := waProtocolRow{}
	for i := 0; i < 6; i++ {
		limit.Actions = append(limit.Actions, waProtocolAction{Kind: "event", Status: &v})
	}
	limit.Actions = append(limit.Actions, waProtocolAction{Kind: "poll"})
	rows = append(rows, limit)
	raw := waSourceCommand(t, "whatsapp-protocol.fixture.ts", map[string]any{"rows": rows})
	var source struct {
		Rows []struct {
			Start   protocol.JSONObject
			Results []protocol.JSONObject
			Sockets int
		}
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(raw, &source); e != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) != 0 {
		t.Fatalf("Source %v %s", e, raw)
	}
	for index, row := range rows {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			var mu sync.Mutex
			now := time.UnixMilli(1000)
			var sockets []*waTestLoginSocket
			created := make(chan struct{}, 20)
			opened := make(chan struct{}, 20)
			service := NewWhatsAppLogin(WhatsAppLoginOptions{Now: func() time.Time { mu.Lock(); defer mu.Unlock(); return now }, UUID: func() string { return source.Rows[index].Start.String("sessionKey") }, CreateSocket: func(ctx context.Context, o wa.NodeWhatsAppOptions) (WhatsAppLoginSocket, error) {
				sock := &waTestLoginSocket{options: o}
				sock.connect = func(context.Context) error {
					created <- struct{}{}
					if row.Initial == "open" {
						o.OnEvent(&events.Connected{})
						opened <- struct{}{}
					} else if row.Initial == "timeout" {
						mu.Lock()
						now = time.UnixMilli(23000)
						mu.Unlock()
					} else {
						o.OnEvent(&events.QR{Codes: []string{"qr-1"}})
					}
					return nil
				}
				mu.Lock()
				sockets = append(sockets, sock)
				mu.Unlock()
				return sock, nil
			}})
			defer service.Close(context.Background())
			dir := t.TempDir()
			start, e := service.Start(context.Background(), dir)
			if e != nil {
				t.Fatal(e)
			}
			compare := func(label string, got, want protocol.JSONObject) {
				a, _ := got.MarshalJSON()
				b, _ := want.MarshalJSON()
				var av, bv any
				_ = json.Unmarshal(a, &av)
				_ = json.Unmarshal(b, &bv)
				if want.String("authDir") != "" {
					got.Set("authDir", encoded(want.String("authDir")))
					a, _ = got.MarshalJSON()
					_ = json.Unmarshal(a, &av)
				}
				if !reflect.DeepEqual(av, bv) {
					t.Fatalf("%s Go%s Source%s", label, a, b)
				}
			}
			compare("start", start, source.Rows[index].Start)
			output := 0
			for _, a := range row.Actions {
				if a.Kind == "advance" {
					mu.Lock()
					now = time.UnixMilli(1000 + a.MS)
					mu.Unlock()
				}
				if a.Kind == "event" {
					mu.Lock()
					sock := sockets[len(sockets)-1]
					old := len(sockets)
					mu.Unlock()
					if a.Open {
						sock.options.OnEvent(&events.Connected{})
						service.mu.Lock()
						current := service.sessions[start.String("sessionKey")]
						service.mu.Unlock()
						if current != nil {
							done := make(chan struct{})
							go func() {
								for {
									service.mu.Lock()
									ready := current.connected
									service.mu.Unlock()
									if ready {
										close(done)
										return
									}
									runtime.Gosched()
								}
							}()
							waWait(t, done)
						}
					} else if a.QR != "" {
						sock.options.OnEvent(&events.QR{Codes: []string{a.QR}})
					} else if a.Status != nil {
						switch *a.Status {
						case 515:
							sock.options.OnEvent(&events.ManualLoginReconnect{})
						case 408:
							sock.options.OnEvent(&events.StreamError{Code: "408"})
						case 401:
							sock.options.OnEvent(&events.LoggedOut{})
						default:
							sock.options.OnEvent(&events.StreamError{Code: fmt.Sprint(*a.Status)})
						}
						if (*a.Status == 515 || *a.Status == 408) && old < 6 {
							done := make(chan struct{})
							go func() {
								for {
									mu.Lock()
									ready := len(sockets) > old
									mu.Unlock()
									if ready {
										close(done)
										return
									}
									runtime.Gosched()
								}
							}()
							waWait(t, done)
						}
					}
				}
				if a.Kind == "poll" {
					result, e := service.Poll(start.String("sessionKey"))
					if e != nil {
						t.Fatal(e)
					}
					compare("poll", result, source.Rows[index].Results[output])
					output++
				}
				if a.Kind == "logout" {
					if e = service.Logout(dir); e != nil {
						t.Fatal(e)
					}
				}
			}
		})
	}
}
