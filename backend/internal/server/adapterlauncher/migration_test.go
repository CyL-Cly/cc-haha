package adapterlauncher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type migrationFixtureProcess struct {
	once                   sync.Once
	done, entered, release chan struct{}
	err                    error
}

func (p *migrationFixtureProcess) Wait() error { <-p.done; return nil }
func (p *migrationFixtureProcess) Close()      { p.once.Do(func() { close(p.done) }) }
func (p *migrationFixtureProcess) Quiesce(context.Context) error {
	close(p.entered)
	<-p.release
	p.Close()
	return p.err
}

func TestMigrationLauncherAllPlatformPipeDrainsBeforeAuthenticAck(t *testing.T) {
	for _, adapter := range adapters {
		t.Run(adapter.key, func(t *testing.T) {
			for _, failure := range []bool{false, true} {
				t.Run(map[bool]string{false: "success", true: "failure"}[failure], func(t *testing.T) {
					o := fixture(t)
					output := &bytes.Buffer{}
					o.Stdout = output
					o.Environment["CC_HAHA_MIGRATION_CONTROL"] = "1"
					o.Environment["CC_HAHA_LOCAL_ACCESS_TOKEN"] = "private-control"
					o.Environment[adapter.firstEnv] = "fake"
					if adapter.secondEnv != "" {
						o.Environment[adapter.secondEnv] = "fake"
					}
					if adapter.key == "whatsapp" {
						dir := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "whatsapp-auth", "default")
						if err := os.MkdirAll(dir, 0700); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(dir, "creds.json"), []byte(`{}`), 0600); err != nil {
							t.Fatal(err)
						}
					}
					p := &migrationFixtureProcess{done: make(chan struct{}), entered: make(chan struct{}), release: make(chan struct{})}
					if failure {
						p.err = errors.New("credential write failed")
					}
					start := func(context.Context, Options) (Process, error) { return p, nil }
					o.StartFeishu, o.StartTelegram, o.StartWechat, o.StartDingTalk, o.StartWhatsApp, o.StartWecom, o.StartQQ, o.StartSlack = start, start, start, start, start, start, start, start
					o.Stdin = strings.NewReader(`{"type":"migration_quiesce","requestId":"wrong","token":"wrong"}` + "\n" + `{"type":"migration_quiesce","requestId":"right","token":"private-control"}` + "\n")
					result := make(chan struct {
						code int
						err  error
					}, 1)
					go func() {
						code, err := Run(context.Background(), []string{adapter.flag}, o)
						result <- struct {
							code int
							err  error
						}{code, err}
					}()
					<-p.entered
					if strings.Contains(output.String(), "migration_quiesced") {
						t.Fatal("acknowledged before actual close/write drain")
					}
					close(p.release)
					done := <-result
					if failure && done.code != 1 || !failure && (done.code != 0 || done.err != nil) {
						t.Fatal(done)
					}
					lines := strings.Split(strings.TrimSpace(output.String()), "\n")
					var reply map[string]string
					if err := json.Unmarshal([]byte(lines[len(lines)-1]), &reply); err != nil {
						t.Fatal(err, output.String())
					}
					want := "migration_quiesced"
					if failure {
						want = "migration_quiesce_failed"
					}
					if reply["type"] != want || reply["requestId"] != "right" || strings.Contains(output.String(), "private-control") {
						t.Fatal(reply, output.String())
					}
				})
			}
		})
	}
}

func TestMigrationLauncherInactiveAndValidationHaveNoPlatformStartup(t *testing.T) {
	for _, adapter := range adapters {
		t.Run(adapter.key, func(t *testing.T) {
			o := fixture(t)
			o.Stdout = &bytes.Buffer{}
			o.Environment["CC_HAHA_MIGRATION_CONTROL"] = "1"
			code, err := Run(context.Background(), []string{adapter.flag}, o)
			if code != 0 || err != nil || !strings.Contains(o.Stdout.(*bytes.Buffer).String(), `"type":"migration_adapter_inactive"`) {
				t.Fatal(code, err, o.Stdout)
			}
			o.Environment["CC_HAHA_MIGRATION_VALIDATION"] = "1"
			code, err = Run(context.Background(), []string{adapter.flag}, o)
			if code != 1 || err == nil || !strings.Contains(err.Error(), "validation") {
				t.Fatal("validation started adapter", code, err)
			}
		})
	}
}

func TestMigrationCommandRejectsMalformedNullWrongTokenAndKeepsEmptyStringID(t *testing.T) {
	input := strings.Join([]string{"bad", `{"type":"migration_quiesce","requestId":null,"token":"secret"}`, `{"type":"migration_quiesce","requestId":"x","token":1}`, `{"type":"migration_quiesce","requestId":"x","token":"wrong"}`, `{"type":"migration_quiesce","requestId":"","token":"secret"}`}, "\n") + "\n"
	id, err := migrationCommand(strings.NewReader(input), "secret")
	if err != nil || id != "" {
		t.Fatal(id, err)
	}
}
