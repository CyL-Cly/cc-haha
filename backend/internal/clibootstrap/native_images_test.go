package clibootstrap

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/imagegeneration"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var nativeImagePNG = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}

func setNativeImageEnvironment(m *MainPrint, scope *cooperative.Scope, url string) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	for key, value := range map[string]string{"CC_HAHA_IMAGE_PROVIDER_KIND": "openai_images", "CC_HAHA_IMAGE_PROVIDER_ID": "private-loopback-images", "CC_HAHA_IMAGE_MODEL": "private-image-model", "CC_HAHA_IMAGE_BASE_URL": url, "CC_HAHA_IMAGE_API_KEY": "private-image-key"} {
		if err := m.environment.SetProperty(quoteMain(key), quoteMain(value)); err != nil {
			return err
		}
	}
	return nil
}

func TestNativeOpenAIImageOAuthUsesSharedFileTokensActualTLSProxyAndOriginalEndpoint(t *testing.T) {
	certificate, ca := nativeOpenAIRuntimeCertificate(t)
	var posts atomic.Int32
	peer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body protocol.JSONObject
		if err := body.UnmarshalJSON(data); err != nil {
			t.Error(err)
			return
		}
		if r.Host != "chatgpt.com" || r.URL.Path != "/backend-api/codex/responses" || r.Header.Get("Authorization") != "Bearer private-image-access" || r.Header.Get("ChatGPT-Account-Id") != "private-image-account" || r.Header.Get("Originator") != "codex_cli_rs" || r.Header.Get("Content-Encoding") != "" {
			t.Error("actual OpenAI image headers/endpoint", r.Host, r.URL.Path, r.Header)
		}
		var tools []protocol.JSONObject
		_ = json.Unmarshal(body.Get("tools"), &tools)
		if len(tools) != 1 || tools[0].String("type") != "image_generation" || tools[0].String("model") != "gpt-image-2" {
			t.Error("actual OpenAI image tool body", body)
		}
		number := posts.Add(1)
		action := "generate"
		if number == 2 {
			action = "edit"
		}
		if tools[0].String("action") != action {
			t.Error("actual generation/edit action", tools[0])
		}
		if number == 2 && !strings.Contains(string(body.Get("input")), "data:image/png;base64,"+base64.StdEncoding.EncodeToString(nativeImagePNG)) {
			t.Error("actual OpenAI edit omitted same-session image bytes")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"item\":{\"type\":\"image_generation_call\",\"result\":%q,\"output_format\":\"png\"}}\n\ndata: [DONE]\n", base64.StdEncoding.EncodeToString(nativeImagePNG))
	}))
	peer.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, NextProtos: []string{"http/1.1"}}
	peer.StartTLS()
	defer peer.Close()
	var tunnels atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != "chatgpt.com:443" {
			t.Error("image proxy endpoint", r.Method, r.Host)
			http.Error(w, "private target", 400)
			return
		}
		upstream, err := net.Dial("tcp", peer.Listener.Addr().String())
		if err != nil {
			t.Error(err)
			http.Error(w, "private dial", 500)
			return
		}
		downstream, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			_ = upstream.Close()
			return
		}
		tunnels.Add(1)
		defer downstream.Close()
		defer upstream.Close()
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffer.Flush()
		copied := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, buffer); close(copied) }()
		_, _ = io.Copy(downstream, upstream)
		_ = upstream.Close()
		_ = downstream.Close()
		<-copied
	}))
	defer proxy.Close()
	m := openAIAuthMainFixture(t)
	tokenPath, caPath := filepath.Join(m.options.Native.Home, "image-openai.json"), filepath.Join(m.options.Native.Home, "image-trust.pem")
	if err := os.WriteFile(tokenPath, rawMain(map[string]any{"accessToken": "private-image-access", "refreshToken": "private-image-refresh", "expiresAt": time.Now().Add(time.Hour).UnixMilli(), "accountId": "private-image-account"}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caPath, ca, 0600); err != nil {
		t.Fatal(err)
	}
	err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for key, value := range map[string]string{"CC_HAHA_IMAGE_PROVIDER_KIND": "openai_oauth", "CC_HAHA_IMAGE_PROVIDER_ID": "openai-official", "CC_HAHA_IMAGE_MODEL": "gpt-image-2", openaiauth.FileEnvironmentKey: tokenPath, "HTTPS_PROXY": proxy.URL, "NODE_EXTRA_CA_CERTS": caPath} {
			if err := m.environment.SetProperty(quoteMain(key), quoteMain(value)); err != nil {
				return err
			}
		}
		input := webObject(`{"prompt":"same native OAuth image", "count":1}`)
		data, err := m.nativeTools.Call(context.Background(), "ImageGen", input, scope, false)
		if err != nil {
			return err
		}
		var output imagegeneration.Output
		if err = json.Unmarshal(data, &output); err != nil {
			return err
		}
		if len(output.Images) != 1 {
			return errors.New("actual OAuth image missing")
		}
		input.Set("referenced_image_paths", rawMain([]string{output.Images[0].Path}))
		_, err = m.nativeTools.Call(context.Background(), "ImageEdit", input, scope, false)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 2 || tunnels.Load() != 2 {
		t.Fatal("actual image physical requests", posts.Load(), tunnels.Load())
	}
	data, err := os.ReadFile(tokenPath)
	if err != nil || !strings.Contains(string(data), "private-image-access") {
		t.Fatal("shared unexpired OAuth file changed", err)
	}
}

func TestNativeImageToolsUseActualOwnerHTTPFilesAuthorizationAndRegistry(t *testing.T) {
	var posts atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		if r.Header.Get("Authorization") != "Bearer private-image-key" {
			t.Error("image API authorization")
		}
		if strings.HasSuffix(r.URL.Path, "/edits") {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				http.Error(w, "multipart", 400)
				return
			}
			file, header, err := r.FormFile("image[]")
			if err != nil {
				t.Error(err)
				http.Error(w, "image missing", 400)
				return
			}
			data, _ := io.ReadAll(file)
			_ = file.Close()
			if string(data) != string(nativeImagePNG) || header.Header.Get("Content-Type") != "image/png" {
				t.Error("actual image multipart bytes")
			}
		} else {
			data, _ := io.ReadAll(r.Body)
			var input protocol.JSONObject
			if err := input.UnmarshalJSON(data); err != nil || input.String("model") != "private-image-model" {
				t.Error("image actual model/body", err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":[{"b64_json":%q,"revised_prompt":"native physical image"}]}`, base64.StdEncoding.EncodeToString(nativeImagePNG))
	}))
	defer peer.Close()
	m := openAIAuthMainFixture(t)
	var generated, edited imagegeneration.Output
	path := filepath.Join(m.options.Native.Home, "human-image.png")
	if err := os.WriteFile(path, nativeImagePNG, 0600); err != nil {
		t.Fatal(err)
	}
	err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := setNativeImageEnvironment(m, scope, peer.URL); err != nil {
			return err
		}
		defs, tools, err := m.nativeTools.Catalog(scope)
		if err != nil {
			return err
		}
		for _, name := range []string{"ImageGen", "ImageEdit"} {
			var found bool
			for _, def := range defs {
				if def.Name == name {
					found = true
					if def.Strict || !def.ShouldDefer {
						return fmt.Errorf("%s schema flags", name)
					}
				}
			}
			if !found {
				return fmt.Errorf("%s absent from actual native catalog", name)
			}
			for _, tool := range tools {
				if tool.Name == name {
					if tool.ValidateInput != nil || !tool.IsConcurrencySafe(protocol.NewJSONObject()) {
						return fmt.Errorf("%s Source handlers", name)
					}
				}
			}
		}
		input := webObject(`{"prompt":"native loopback image","count":1}`)
		decision, err := m.nativeTools.PermissionDecision(scope, "ImageGen", &input)
		if err != nil || decision.Behavior != "allow" || decision.UpdatedInput != &input {
			return fmt.Errorf("Source allow/updatedInput: %v", err)
		}
		data, err := m.nativeTools.Call(context.Background(), "ImageGen", input, scope, false)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(data, &generated); err != nil {
			return err
		}
		if len(generated.Images) != 1 {
			return errors.New("real image missing")
		}
		edit := webObject(fmt.Sprintf(`{"prompt":"edit prior output","count":1,"referenced_image_paths":[%q]}`, generated.Images[0].Path))
		data, err = m.nativeTools.Call(context.Background(), "ImageEdit", edit, scope, false)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(data, &edited); err != nil {
			return err
		}
		edit.Set("referenced_image_paths", rawMain([]string{path}))
		if _, err = m.nativeTools.Call(context.Background(), "ImageEdit", edit, scope, false); err == nil || !strings.Contains(err.Error(), "not provided by the user in this session") {
			return fmt.Errorf("arbitrary disk image accepted: %v", err)
		}
		// Only the explicit user attachment producer can add a resolved path.
		if err = m.nativeImageBindings().RegisterUserImage(context.Background(), scope, path); err != nil {
			return err
		}
		if _, err = m.nativeTools.Call(context.Background(), "ImageEdit", edit, scope, false); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 3 || generated.Operation != "generate" || edited.Operation != "edit" || edited.InputImageCount != 1 {
		t.Fatalf("native image operations posts%d gen%v edit%v", posts.Load(), generated, edited)
	}
	for _, result := range []imagegeneration.Output{generated, edited} {
		path := result.Images[0].Path
		data, err := os.ReadFile(path)
		if err != nil || string(data) != string(nativeImagePNG) {
			t.Fatal("physical output", err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("private generated image mode", err)
		}
		if !strings.Contains(path, filepath.Join("generated-images", m.Controller.SessionID())) {
			t.Fatal("actual activated session image directory", path)
		}
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err = foreign.Run(context.Background(), func(scope *cooperative.Scope) error { _, err := m.nativeImageBindings().Config(scope); return err }); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("foreign image scope", err)
	}
	other := openAIAuthMainFixture(t)
	if err = other.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := setNativeImageEnvironment(other, scope, peer.URL); err != nil {
			return err
		}
		_, err := other.nativeTools.Call(context.Background(), "ImageEdit", webObject(fmt.Sprintf(`{"prompt":"foreign human image","count":1,"referenced_image_paths":[%q]}`, path)), scope, false)
		return err
	}); err == nil || !strings.Contains(err.Error(), "not provided by the user in this session") {
		t.Fatal("cross session human image authorization", err)
	}
}

func TestNativeImageCancellationClosesActualHTTPAndOwnerRemainsUsable(t *testing.T) {
	opened, closed := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[`)
		w.(http.Flusher).Flush()
		close(opened)
		<-r.Context().Done()
		close(closed)
	}))
	defer peer.Close()
	m := openAIAuthMainFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			if err := setNativeImageEnvironment(m, scope, peer.URL); err != nil {
				return err
			}
			_, err := m.nativeTools.Call(ctx, "ImageGen", webObject(`{"prompt":"cancel image","count":1}`), scope, false)
			return err
		})
	}()
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("image physical body did not start")
	}
	cancel()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("image HTTP stream not closed")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancel reported image success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("image owner did not settle")
	}
	if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error { _, err := m.nativeImageBindings().Config(scope); return err }); err != nil {
		t.Fatal("next owner action", err)
	}
	root := filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "cc-haha", "generated-images", m.Controller.SessionID())
	files, err := os.ReadDir(root)
	if !errors.Is(err, os.ErrNotExist) || len(files) != 0 {
		t.Fatal("canceled upstream staged image", files, err)
	}
}
