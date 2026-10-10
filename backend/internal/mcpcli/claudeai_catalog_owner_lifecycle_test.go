package mcpcli_test

import (
	"bytes"
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type ownedCatalogHTTP struct {
	base      *url.URL
	transport http.RoundTripper
}

func (p ownedCatalogHTTP) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	target := *request.URL
	target.Host, target.Scheme = p.base.Host, p.base.Scheme
	copy.URL = &target
	copy.Host = ""
	return p.transport.RoundTrip(copy)
}
func TestCatalogBeginActualMainCommandCredentialOwnerPluginFilesystemAndClose(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	config := filepath.Join(home, ".claude")
	market := filepath.Join(home, "market")
	plugin := filepath.Join(market, "alpha")
	for _, dir := range []string{config, filepath.Join(config, "plugins"), filepath.Join(market, ".claude-plugin"), filepath.Join(plugin, ".claude-plugin")} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		if err = os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(config, "settings.json"), `{"future":{"keep":true},"enabledPlugins":{"alpha@owned":true}}`)
	write(filepath.Join(config, ".credentials.json"), `{"future":{"keep":true},"claudeAiOauth":{"accessToken":"fake-catalog-owner","refreshToken":"fake-refresh","scopes":["user:mcp_servers"],"expiresAt":9000000000000}}`)
	write(filepath.Join(market, ".claude-plugin", "marketplace.json"), `{"name":"owned","owner":{"name":"Owned"},"plugins":[{"name":"alpha","source":"./alpha"}]}`)
	write(filepath.Join(plugin, ".claude-plugin", "plugin.json"), `{"name":"alpha","version":"1.0","settings":{"agent":"Plan"}}`)
	write(filepath.Join(plugin, "physical-owned.txt"), "actual owned plugin consumer\n")
	known := protocol.NewJSONObject(protocol.JSONField{Name: "owned", Value: []byte(`{"source":{"source":"directory","path":"` + market + `"},"installLocation":"` + market + `","lastUpdated":"2024-01-01T00:00:00Z"}`)})
	raw, _ := known.MarshalJSON()
	write(filepath.Join(config, "plugins", "known_marketplaces.json"), string(raw))
	started, closed := make(chan struct{}), make(chan struct{})
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fake-catalog-owner" || r.URL.RequestURI() != "/v1/mcp_servers?limit=1000" {
			t.Error("actual credential/catalog request differs")
		}
		close(started)
		<-r.Context().Done()
		close(closed)
	}))
	defer peer.Close()
	base, _ := url.Parse(peer.URL)
	env := map[string]string{"HOME": home, "USERPROFILE": home, "USER": "owned", "CLAUDE_CONFIG_DIR": config, "TMPDIR": home, "PATH": "/usr/bin:/bin", "USER_TYPE": "external", "NODE_ENV": "production", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1"}
	native, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: home, Platform: "linux", ManagedRoot: filepath.Join(root, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		t.Error("real Keychain primitive reached")
		return nil, os.ErrPermission
	}
	command, err := clibootstrap.NewMainCommand(clibootstrap.MainCommandOptions{Arguments: []string{"mcp", "list"}, Native: native, Platform: "linux", PID: os.Getpid(), Output: &bytes.Buffer{}, Error: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	defer command.Close()
	binding, err := command.NewCLIClaudeAIMCP(context.Background(), &http.Client{Transport: ownedCatalogHTTP{base: base, transport: peer.Client().Transport}})
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Catalog.Close()
	flight := binding.Catalog.Begin(context.Background())
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("actual shared credential owner did not reach TLS")
	}
	// The actual native loader runs to completion while the admitted catalog body is held.
	loaded, err := command.Settings.LoadPlugins(context.Background(), "", false)
	if err != nil || len(loaded.Enabled) != 1 || loaded.Enabled[0].Name != "alpha" {
		t.Fatal("actual plugin consumer while catalog pending", err, loaded)
	}
	bytesRead, err := os.ReadFile(filepath.Join(loaded.Enabled[0].Path, "physical-owned.txt"))
	if err != nil || string(bytesRead) != "actual owned plugin consumer\n" {
		t.Fatal("physical plugin read", err, string(bytesRead))
	}
	// Close is joined through the same command's admitted native wait, rather
	// than blocking its credential callback/owner stack.
	err = command.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, err := scope.AwaitBytes(cooperative.NetworkOpen, func(context.Context) ([]byte, error) { binding.Catalog.Close(); return []byte("joined"), nil })
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("owned TLS request survived native join")
	}
	if len(flight.Await(context.Background()).OwnProperties()) != 0 {
		t.Fatal("closed actual owner flight not empty")
	}
}
