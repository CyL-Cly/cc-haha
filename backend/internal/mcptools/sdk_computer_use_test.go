package mcptools

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/curepl"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceComputerUseCLIResultProjection(t *testing.T) {
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("pinned Bun required for actual Source")
	}
	image := image.NewRGBA(image.Rect(0, 0, 3, 2))
	image.Set(0, 0, color.RGBA{R: 255, A: 255})
	var pngBytes, jpegBytes bytes.Buffer
	_ = png.Encode(&pngBytes, image)
	_ = jpeg.Encode(&jpegBytes, image, nil)
	png64 := base64.StdEncoding.EncodeToString(pngBytes.Bytes())
	jpeg64 := base64.StdEncoding.EncodeToString(jpegBytes.Bytes())
	inputs := []struct {
		Name   string          `json:"name"`
		Result json.RawMessage `json:"result"`
	}{
		{"js", json.RawMessage(`{"content":[{"type":"text","text":"ok"}],"telemetry":{"error_kind":"ignored"}}`)},
		{"js", json.RawMessage(`{"content":[{"type":"text","text":"ok"}],"structuredContent":{"z":1,"a":2},"_meta":{"future":{"keep":true}}}`)},
		{"js", json.RawMessage(`{"content":[{"type":"text","text":"first"},{"type":"text","text":"second"}],"structuredContent":{"z":"last","a":"first"},"isError":true}`)},
		{"js", json.RawMessage(`{"content":[],"isError":true}`)},
		{"js", json.RawMessage(`{"content":[{"type":"text","text":"ok"}],"structuredContent":null,"_meta":false}`)},
		{"js", json.RawMessage(`{"content":[],"structuredContent":{},"_meta":{}}`)},
		{"js", json.RawMessage(`{"content":[{"type":"image","data":"unreadable"}]}`)},
		{"js", json.RawMessage(`{"content":[{"type":"resource","future":"unknown"}]}`)},
	}
	for _, name := range []string{"js", "zoom"} {
		r, _ := json.Marshal(map[string]any{"content": []any{map[string]any{"type": "image", "data": png64, "mimeType": "image/png"}, map[string]any{"type": "text", "text": "middle"}, map[string]any{"type": "image", "data": jpeg64, "mimeType": "image/jpeg"}}})
		inputs = append(inputs, struct {
			Name   string          `json:"name"`
			Result json.RawMessage `json:"result"`
		}{name, r})
	}
	root, _ := filepath.Abs("../../..")
	home := t.TempDir()
	tmp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(tmp, 0700)
	script := `globalThis.MACRO={VERSION:'fixture',BUILD_TIME:'',PACKAGE_URL:'',README_URL:'',ISSUES_URL:''};const root=Bun.argv[2];const inputs=await Bun.stdin.json();const w=await import(root+'/src/utils/computerUse/wrapper.tsx');const out=[];for(const c of inputs){try{const result=await w.dispatchComputerUseCall(async()=>c.result,c.name,{}, {abortController:new AbortController()});out.push({result})}catch(e){out.push({error:e.message})}}console.log(JSON.stringify(out))`
	file := filepath.Join(home, "source.ts")
	_ = os.WriteFile(file, []byte(script), 0600)
	encoded, _ := json.Marshal(inputs)
	command := exec.Command(bun, file, root)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + tmp, "NODE_ENV=test", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=" + os.Getenv("PATH")}
	command.Stdin = bytes.NewReader(encoded)
	output, e := command.CombinedOutput()
	if e != nil {
		t.Fatalf("Source=%s %v", output, e)
	}
	var expected []any
	if e := json.Unmarshal(output, &expected); e != nil {
		t.Fatalf("Source=%s %v", output, e)
	}
	for i, input := range inputs {
		t.Run(input.Name+string(rune('a'+i)), func(t *testing.T) {
			var value protocol.JSONObject
			_ = value.UnmarshalJSON(input.Result)
			result, e := ProjectComputerUseJSONResult(value, input.Name)
			out := map[string]any{}
			if e != nil {
				out["error"] = e.Error()
			} else {
				payload := map[string]json.RawMessage{"data": result.Data}
				if result.MCPMeta != nil {
					payload["mcpMeta"] = result.MCPMeta
				}
				out["result"] = payload
			}
			encoded, _ := json.Marshal(out)
			var actual any
			_ = json.Unmarshal(encoded, &actual)
			if !reflect.DeepEqual(actual, expected[i]) {
				t.Fatalf("Source=%v\nGo=%s", expected[i], encoded)
			}
		})
	}
	// The actual typed binder result retains raw metadata order through JSON.
	var result curepl.Result
	if e := json.Unmarshal(inputs[1].Result, &result); e != nil {
		t.Fatal(e)
	}
	projected, e := ProjectComputerUseResult(result, "js")
	if e != nil || !strings.Contains(string(projected.Data), `{\"z\":1,\"a\":2}`) {
		t.Fatalf("typed result order=%s %v", projected.Data, e)
	}
}
