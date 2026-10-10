package imregistration

import (
	"context"
	"encoding/json"
	"fmt"
	wa "github.com/NanmiCoder/cc-haha/backend/internal/whatsappcompat"
	walog "go.mau.fi/whatsmeow/util/log"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type officialQRProbeLog struct{ t *testing.T }

func (l officialQRProbeLog) Warnf(format string, v ...any) {
	l.t.Log("WA warning", fmt.Sprintf(format, v...))
}
func (l officialQRProbeLog) Errorf(format string, v ...any) {
	l.t.Log("WA error", fmt.Sprintf(format, v...))
}
func (l officialQRProbeLog) Infof(format string, v ...any)  {}
func (l officialQRProbeLog) Debugf(format string, v ...any) {}
func (l officialQRProbeLog) Sub(string) walog.Logger        { return l }
func TestOfficialUnboundWhatsAppQRDiagnostic(t *testing.T) {
	if os.Getenv("CC_HAHA_OFFICIAL_UNBOUND_QR") != "1" {
		t.Skip("separate authorized unbound QR evidence")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	service, e := NewWhatsApp(WhatsAppOptions{ConfigDir: config, HomeDir: home, WorkDir: home, Environment: map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config}, Login: WhatsAppLoginOptions{ClientOptions: wa.NodeWhatsAppOptions{Log: officialQRProbeLog{t}, OnEvent: func(event any) { t.Logf("actual event %T", event) }}}})
	if e != nil {
		t.Fatal(e)
	}
	defer service.Close(context.Background())
	started := time.Now()
	result, e := service.Begin(ctx)
	if e != nil {
		t.Logf("actual Begin cause %T %v after %s", e, e, time.Since(started))
		t.Fatal("official unbound QR start failed")
	}
	data, _ := json.Marshal(map[string]any{"elapsedMs": time.Since(started).Milliseconds(), "hasQR": result.String("qr") != "", "hasQRDataURL": result.String("qrDataUrl") != "", "message": result.String("message")})
	t.Log(string(data))
	if result.String("qr") == "" {
		t.Log("actual direct network returned waiting; no QR or account was obtained")
	}
}
