package requestretry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestQuestionProcessedImageAdmissionReachesPhysicalRequest(t *testing.T) {
	server := startFixtureServer(t, "sequence", nil)
	input, deps := standardInput(), standardDeps()
	input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.origin
	input.Context.History = []protocol.JSONObject{originalHistoryRecord(t, `{"type":"user","uuid":"image-permission-user","imagePasteIds":[3],"message":{"role":"user","content":[{"type":"text","text":"question answer"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"cHJvY2Vzc2VkLWltYWdl"},"future":"preserved"}]}}`)}
	stream, err := Open(context.Background(), input, deps)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for {
		_, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	body := <-server.bodies
	if !strings.Contains(body, `"type":"image","source":{"type":"base64","media_type":"image/png","data":"cHJvY2Vzc2VkLWltYWdl"},"future":"preserved"`) {
		t.Fatalf("original image missing in physical model request: %s", body)
	}
}

func TestNativeMCPScreenshotNestedImageReachesPhysicalRequest(t *testing.T) {
	server := startFixtureServer(t, "sequence", nil)
	input, deps := standardInput(), standardDeps()
	input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.origin
	input.Context.History = []protocol.JSONObject{
		originalHistoryRecord(t, `{"type":"user","uuid":"user","message":{"role":"user","content":"capture screenshot"}}`),
		originalHistoryRecord(t, `{"type":"assistant","uuid":"assistant","message":{"role":"assistant","id":"assistant-id","model":"fixture-model","content":[{"type":"tool_use","id":"capture","name":"mcp__computer-use__js","input":{"code":"await app.getScreenshot()"}}]}}`),
		originalHistoryRecord(t, `{"type":"user","uuid":"result","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"capture","content":[{"type":"text","text":"Image 1: 1×1 pixels"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"cHJvY2Vzc2VkLWltYWdl"},"future":"preserved"}]}]}}`),
	}
	stream, err := Open(context.Background(), input, deps)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for {
		_, err = stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	body := <-server.bodies
	if !strings.Contains(body, `"content":[{"type":"text","text":"Image 1: 1×1 pixels"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"cHJvY2Vzc2VkLWltYWdl"},"future":"preserved"}]`) {
		t.Fatal("nested screenshot removed or moved", body)
	}
}

func TestQuestionProcessedImageAdmissionRejectsUnmigratedSources(t *testing.T) {
	for _, block := range []string{
		`{"type":"image"}`,
		`{"type":"image","source":{"type":"base64","data":"private"}}`,
		`{"type":"image","source":{"type":"base64","media_type":"image/png","data":4}}`,
		`{"type":"image","source":{"type":"base64","media_type":"image/tiff","data":"private"}}`,
		`{"type":"image","source":{"type":"url","url":4}}`,
		`{"type":"image","source":{"type":"file","file_id":"private-file"}}`,
	} {
		input, deps := standardInput(), standardDeps()
		input.Context.History = []protocol.JSONObject{originalHistoryRecord(t, `{"type":"user","message":{"role":"user","content":[`+block+`]}}`)}
		deps.OnPreparation = func([]protocol.JSONObject) { t.Fatal("unported image prepared") }
		deps.OnClient = func() { t.Fatal("unported image reached client/auth") }
		deps.ObserveRequest = func(string, http.Header, json.RawMessage) { t.Fatal("unported image sent") }
		_, err := Open(context.Background(), input, deps)
		var capability *CapabilityError
		if !errors.As(err, &capability) || capability.Boundary != "unported history media/tool result" {
			t.Fatalf("unmigrated image accepted: %s %v", block, err)
		}
	}
}
