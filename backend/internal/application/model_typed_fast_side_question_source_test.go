package application

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTypedModelEnabledFastActualSourceSideQuestionReissuesScopeKeepsParentPrefix(t *testing.T) {
	runTypedModelFastActualSideQuestion(t, false, false)
}

func TestTypedModelEnabledFastActualSourceSideQuestionFallbackReadsLiveStateOnChild(t *testing.T) {
	runTypedModelFastActualSideQuestion(t, true, false)
}

func TestTypedModelEnabledFastActualSourceSideQuestionSamplesChangedParentState(t *testing.T) {
	runTypedModelFastActualSideQuestion(t, false, true)
}

func runTypedModelFastActualSideQuestion(t *testing.T, fallback, switchModel bool) {
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("actual Source requires pinned Bun")
	}
	type request struct {
		Body map[string]any
		Beta string
	}
	seen := map[string][]request{}
	var mu sync.Mutex
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		if e := json.Unmarshal(data, &body); e != nil {
			t.Error(e)
			return
		}
		mu.Lock()
		client := r.Header.Get("X-M680-Client")
		seen[client] = append(seen[client], request{body, r.Header.Get("Anthropic-Beta")})
		mu.Unlock()
		if bytes.Contains(data, []byte("This is a side question from the user")) {
			response(w, "side-fast", "", nil, "SIDE FAST ANSWER")
		} else {
			response(w, "parent-fast", "", nil, "PARENT FAST ANSWER")
		}
	}))
	defer peer.Close()
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw})
	home := t.TempDir()
	for _, p := range []string{"tmp", "config", "cache"} {
		if e := os.Mkdir(filepath.Join(home, p), 0700); e != nil {
			t.Fatal(e)
		}
	}
	fixture, _ := filepath.Abs("../../testdata/native-model-fast-side-question.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/loopback-only.sb")
	input, _ := json.Marshal(map[string]any{"home": home, "origin": peer.URL, "certificate": string(cert), "fallback": fallback, "switchModel": switchModel})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = []string{"BUN_OPTIONS=--no-env-file", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "XDG_CACHE_HOME=" + filepath.Join(home, "cache"), "PATH=/usr/bin:/bin", "ANTHROPIC_API_KEY=fixture-model-key", "ANTHROPIC_CUSTOM_HEADERS=X-M680-Client: Source", "ANTHROPIC_BASE_URL=" + peer.URL, "ANTHROPIC_DEFAULT_OPUS_MODEL=claude-opus-4-6", "CLAUDE_CODE_MAX_OUTPUT_TOKENS=1", "CLAUDE_CODE_MAX_RETRIES=0", "CC_HAHA_MODEL_COMMAND_FIXTURE=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "NODE_ENV=production"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil || stderr.Len() > 0 {
		t.Fatalf("actual query→runSideQuestion Source %v: %s", e, stderr.String())
	}
	var source struct {
		Response              string
		ParentUnchanged, Fast bool
	}
	if e = json.Unmarshal(out, &source); e != nil || source.Response != "SIDE FAST ANSWER" || !source.ParentUnchanged || source.Fast == switchModel {
		t.Fatal("original actual side state", e, string(out))
	}
	t.Logf("actual original main query → runForkedAgent → side query: parent unchanged, sampled fast=%v, three physical HTTPS requests", source.Fast)
	o := nativeEnabledFastSideOptions(t, peer.URL, cert)
	s, e := NewSession(ctx, o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	if !fallback {
		if e = s.Submit(ctx, tui.Input{Text: "PARENT_FAST"}, nil); e != nil {
			t.Fatal(e)
		}
	}
	history := func() []protocol.JSONObject {
		var rows []protocol.JSONObject
		receipt, e := s.process.Owner.SubmitIndependent(ctx, cooperative.Read, func(*cooperative.Scope) error { rows = s.process.Controller.History(); return nil })
		if e == nil {
			e = receipt.Wait()
		}
		if e != nil {
			t.Fatal(e)
		}
		return rows
	}
	if switchModel {
		if e = s.Submit(ctx, tui.Input{Mode: "prompt", Text: "/model sonnet"}, func(tui.Event) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	before := raw(history())
	question := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: raw("side_question")}, protocol.JSONField{Name: "question_id", Value: raw("M680-enabled-fast")}, protocol.JSONField{Name: "question", Value: raw("SIDE_FAST")}, protocol.JSONField{Name: "history", Value: raw([]any{})})
	answer, e := s.process.SideQuestionControl(ctx, question)
	if e != nil {
		t.Fatal("actual sealed same Host fast side request", e)
	}
	if answer.String("response") != source.Response || !bytes.Equal(before, raw(history())) {
		t.Fatal("actual side response/parent canonical", answer)
	}
	if fallback {
		if e = s.Submit(ctx, tui.Input{Text: "PARENT_FAST"}, nil); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.Submit(ctx, tui.Input{Text: "PARENT_NEXT_FAST"}, nil); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen["Source"]) != 3 || len(seen["Go"]) != 3 {
		t.Fatal("physical consumer count", seen)
	}
	for i, want := range seen["Source"] {
		got := seen["Go"][i]
		for _, key := range []string{"model", "speed", "stream", "max_tokens"} {
			if !reflect.DeepEqual(got.Body[key], want.Body[key]) {
				t.Fatal("actual Source side fast policy", i, key, got.Body[key], want.Body[key])
			}
		}
		if got.Beta != want.Beta {
			t.Fatal("Source fast beta latch", i, got.Beta, want.Beta)
		}
	}
	for _, client := range []string{"Source", "Go"} {
		parent, side, next := seen[client][0].Body, seen[client][1].Body, seen[client][2].Body
		if fallback {
			parent, side = side, parent
		}
		for _, key := range []string{"system", "thinking", "tools"} {
			if !reflect.DeepEqual(fastSideStablePrefix(key, parent[key]), fastSideStablePrefix(key, side[key])) {
				t.Fatal("parent cached prefix", client, key, string(raw(parent[key])), string(raw(side[key])))
			}
		}
		if !fallback && !bytes.Contains(raw(side["messages"]), []byte("PARENT FAST ANSWER")) || bytes.Contains(raw(next["messages"]), []byte("SIDE FAST ANSWER")) {
			t.Fatal("side history isolation", client)
		}
	}
}

// The original billing header's cch and version suffix are request/prompt
// hashes outside its cached
// system blocks. Preserve and compare every other byte/property of the prefix.
func fastSideStablePrefix(key string, value any) any {
	if key != "system" {
		return value
	}
	encoded := raw(value)
	encoded = regexp.MustCompile(`cch=[0-9a-f]+;`).ReplaceAll(encoded, []byte("cch=<request-hash>;"))
	encoded = regexp.MustCompile(`(cc_version=[^;]+)\.[0-9a-f]{3};`).ReplaceAll(encoded, []byte("$1.<prompt-hash>;"))
	var result any
	_ = json.Unmarshal(encoded, &result)
	return result
}

func TestTypedModelEnabledFastSameHostSideQuestionOverlapAndCloseOwnFork(t *testing.T) {
	for _, order := range []string{"main-first", "side-first-close"} {
		t.Run(order, func(t *testing.T) {
			mainEntered, sideEntered, sidePeerJoined := make(chan struct{}), make(chan struct{}), make(chan struct{})
			releaseMain, releaseSide := make(chan struct{}), make(chan struct{})
			var mainOnce, sideOnce sync.Once
			defer mainOnce.Do(func() { close(releaseMain) })
			defer sideOnce.Do(func() { close(releaseSide) })
			peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				var body map[string]any
				if e := json.Unmarshal(data, &body); e != nil {
					t.Error(e)
					return
				}
				if body["speed"] != "fast" {
					t.Error("actual enabled-fast request", body["speed"])
				}
				if bytes.Contains(data, []byte("This is a side question from the user")) {
					close(sideEntered)
					defer close(sidePeerJoined)
					select {
					case <-releaseSide:
					case <-r.Context().Done():
						return
					}
					response(w, "side-held", "", nil, "SIDE HELD ANSWER")
					return
				}
				if bytes.Contains(data, []byte("held fast main")) {
					close(mainEntered)
					select {
					case <-releaseMain:
					case <-r.Context().Done():
						return
					}
				}
				response(w, "parent-held", "", nil, "PARENT HELD ANSWER")
			}))
			defer peer.Close()
			cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw})
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			s, e := NewSession(ctx, nativeEnabledFastSideOptions(t, peer.URL, cert))
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close(context.Background())
			if e = s.Submit(ctx, tui.Input{Text: "seed enabled fast parent"}, nil); e != nil {
				t.Fatal(e)
			}
			mainDone, sideDone := make(chan error, 1), make(chan error, 1)
			startMain := func() {
				go func() { mainDone <- s.Submit(ctx, tui.Input{Text: "held fast main"}, nil) }()
				select {
				case <-mainEntered:
				case <-ctx.Done():
					t.Fatal("main socket not admitted", context.Cause(ctx))
				}
			}
			startSide := func() {
				go func() {
					question := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: raw("side_question")}, protocol.JSONField{Name: "question_id", Value: raw("M680-fast-held")}, protocol.JSONField{Name: "question", Value: raw("held fast side")}, protocol.JSONField{Name: "history", Value: raw([]any{})})
					_, e := s.process.SideQuestionControl(ctx, question)
					sideDone <- e
				}()
				select {
				case <-sideEntered:
				case e := <-sideDone:
					t.Fatal("side socket not admitted", e)
				case <-ctx.Done():
					t.Fatal("side socket timeout", context.Cause(ctx))
				}
			}
			if order == "main-first" {
				startMain()
				startSide()
			} else {
				startSide()
				startMain()
			}
			mainOnce.Do(func() { close(releaseMain) })
			select {
			case e := <-mainDone:
				if e != nil {
					t.Fatal("main completion", e)
				}
			case <-ctx.Done():
				t.Fatal("held side monopolized main Owner", context.Cause(ctx))
			}
			select {
			case e := <-sideDone:
				t.Fatal("side settled before release", e)
			default:
			}
			if order == "side-first-close" {
				if e = s.Close(ctx); e != nil {
					t.Fatal("actual Close join", e)
				}
				select {
				case e := <-sideDone:
					if e == nil {
						t.Fatal("cancelled fork returned success")
					}
				case <-ctx.Done():
					t.Fatal("Close did not join fork", context.Cause(ctx))
				}
			} else {
				sideOnce.Do(func() { close(releaseSide) })
				select {
				case e := <-sideDone:
					if e != nil {
						t.Fatal("side completion", e)
					}
				case <-ctx.Done():
					t.Fatal("side did not settle", context.Cause(ctx))
				}
			}
			select {
			case <-sidePeerJoined:
			case <-ctx.Done():
				t.Fatal("physical side socket not joined", context.Cause(ctx))
			}
		})
	}
}

func nativeEnabledFastSideOptions(t *testing.T, origin string, cert []byte) Options {
	t.Helper()
	o := nativeSessionOptions(t, origin)
	o.Interactive = true
	o.Arguments.Model = "opus"
	o.Arguments.Tools = nil
	certPath := filepath.Join(t.TempDir(), "root.pem")
	if e := os.WriteFile(certPath, cert, 0600); e != nil {
		t.Fatal(e)
	}
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		delete(env, "CLAUDE_CODE_DISABLE_FAST_MODE")
		env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = "claude-opus-4-6"
		env["NODE_EXTRA_CA_CERTS"] = certPath
		env["ANTHROPIC_CUSTOM_HEADERS"] = "X-M680-Client: Go"
		env["CLAUDE_CODE_MAX_OUTPUT_TOKENS"] = "1"
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if e := os.MkdirAll(config, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"model":"opus","fastMode":true,"future":true}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(config, ".claude.json"), []byte(`{"penguinModeOrgEnabled":true,"future":true}`), 0600); e != nil {
		t.Fatal(e)
	}
	return o
}
