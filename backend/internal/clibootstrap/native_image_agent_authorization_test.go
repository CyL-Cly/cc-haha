package clibootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

func TestActualIndependentAgentImageEditUsesParentsExplicitImageAuthority(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 12, 8))
	for x := 0; x < 12; x++ {
		for y := 0; y < 8; y++ {
			source.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), 50, 137})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	var models, uploads atomic.Int32
	var authorized, unprovided, generated string
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if strings.Contains(r.URL.Path, "/images/") {
			uploads.Add(1)
			if !bytes.Contains(data, encoded.Bytes()) {
				t.Error("independent child did not upload actual parent-provided image")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(encoded.Bytes()))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch models.Add(1) {
		case 1:
			_, _ = io.WriteString(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(fmt.Sprintf(`{"type":"tool_use","id":"child-unprovided-image","name":"ImageEdit","input":{"prompt":"refuse undisclosed path","referenced_image_paths":[%q]}}`, unprovided))}))
		case 2:
			if !bytes.Contains(data, []byte("not provided by the user in this session")) || !bytes.Contains(data, []byte(`"is_error":true`)) {
				t.Error("actual child did not retain original upload denial", string(data))
			}
			_, _ = io.WriteString(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(fmt.Sprintf(`{"type":"tool_use","id":"child-provided-image","name":"ImageEdit","input":{"prompt":"edit actual parent attachment","referenced_image_paths":[%q]}}`, authorized))}))
		case 3:
			var request protocol.JSONObject
			_ = request.UnmarshalJSON(data)
			var messages []protocol.JSONObject
			_ = json.Unmarshal(request.Get("messages"), &messages)
			for _, message := range messages {
				var blocks []protocol.JSONObject
				_ = json.Unmarshal(message.Get("content"), &blocks)
				for _, block := range blocks {
					if block.String("tool_use_id") == "child-provided-image" {
						var output struct{ Images []struct{ Path string } }
						if json.Unmarshal([]byte(block.String("content")), &output) == nil && len(output.Images) == 1 {
							generated = output.Images[0].Path
						}
					}
				}
			}
			if generated == "" {
				t.Error("actual child lost authorized ImageEdit result", string(data))
				http.Error(w, "no physical edit", 400)
				return
			}
			_, _ = io.WriteString(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(fmt.Sprintf(`{"type":"tool_use","id":"child-edited-image-read","name":"Read","input":{"file_path":%q}}`, generated))}))
		case 4:
			if !bytes.Contains(data, []byte(base64.StdEncoding.EncodeToString(encoded.Bytes()))) || !bytes.Contains(data, []byte(`"media_type":"image/png"`)) {
				t.Errorf("child native image processor was not retained: %.7000s", data)
			}
			_, _ = io.WriteString(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(`{"type":"text","text":"CHILD_PARENT_IMAGE_AUTHORITY_OK"}`)}))
		default:
			t.Error("unexpected child inference")
			http.Error(w, "unexpected", 400)
		}
	}))
	defer peer.Close()
	m := newNativeImageHarness(t, peer.URL, peer.Certificate().Raw, func([]byte) error { return nil })
	authorized = filepath.Join(m.options.Native.Home, "parent provided.png")
	unprovided = filepath.Join(m.options.Native.Home, "not provided.png")
	for _, path := range []string{authorized, unprovided} {
		if err := os.WriteFile(path, encoded.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if _, err := m.nativeTools.SkillFileAttachment(ctx, scope, skillcommands.MentionedFile{Filename: authorized}); err != nil {
			return err
		}
		request, err := actualBackgroundAgentRequest(scope, m, "Edit the explicit parent image and read the result.")
		if err != nil {
			return err
		}
		request.ParentModel = "deepseek-flash"
		request.ParentTools, request.Tools = []string{"ImageEdit", "Read"}, []string{"ImageEdit", "Read"}
		future, err := m.startNativeForegroundAgent(ctx, scope, request)
		if err != nil {
			return err
		}
		result, err := future.Await(scope)
		if err != nil {
			return err
		}
		if !strings.Contains(string(rawMain(result)), "CHILD_PARENT_IMAGE_AUTHORITY_OK") {
			return fmt.Errorf("actual child result: %s", rawMain(result))
		}
		resolved, err := filepath.EvalSymlinks(authorized)
		if err != nil {
			return err
		}
		allowed, err := m.nativeUserImageAuthorized(scope, resolved)
		if err != nil || !allowed {
			return fmt.Errorf("parent image authority lost after child: %v %v", allowed, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if models.Load() != 4 || uploads.Load() != 1 {
		t.Fatal("actual child inference/upload cardinality", models.Load(), uploads.Load())
	}
	if output, err := os.ReadFile(generated); err != nil || !bytes.Equal(output, encoded.Bytes()) {
		t.Fatal("actual child generated file", err)
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	var sidechain []byte
	err = filepath.WalkDir(filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "projects"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "agent-") && strings.HasSuffix(path, ".jsonl") {
			data, err := os.ReadFile(path)
			sidechain = append(sidechain, data...)
			return err
		}
		return nil
	})
	if err != nil || !bytes.Contains(sidechain, []byte("CHILD_PARENT_IMAGE_AUTHORITY_OK")) || !bytes.Contains(sidechain, []byte("child-provided-image")) {
		t.Fatal("child canonical sidechain image evidence", err)
	}
}
