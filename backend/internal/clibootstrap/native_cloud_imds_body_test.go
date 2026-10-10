package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cloudauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceAndNativeIMDSToken400ClosesWithoutDrainingLongErrorBody(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	closed := make(chan struct{}, 2)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.URL.Path != "/latest/api/token" {
			t.Error("unexpected credential/model request")
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Length", "1048576")
		w.WriteHeader(400)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		closed <- struct{}{}
	}))
	defer peer.Close()
	defer peer.CloseClientConnections()
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-aws-imds-physical.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_AWS_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	raw, _ := json.Marshal(map[string]string{"url": peer.URL})
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct{ Error string }
	if json.Unmarshal(out, &source) != nil || source.Error != "EC2 Metadata token request returned error" {
		t.Fatal("unexpected Source", string(out))
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("actual Source did not destroy metadata response")
	}
	m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Read"} })
	var failure error
	err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if err := m.environment.Assign(protocol.NewJSONObject(protocol.JSONField{Name: "CLAUDE_CODE_USE_BEDROCK", Value: quoteMain("1")}, protocol.JSONField{Name: "AWS_EC2_METADATA_SERVICE_ENDPOINT", Value: quoteMain(peer.URL)})); err != nil {
			return err
		}
		resolver, err := m.nativeAWSResolver(scope)
		if err != nil {
			return err
		}
		_, failure = cloudauth.NewInstanceProvider(resolver).Resolve(ctx)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var status *cloudauth.MetadataError
	if !errors.As(failure, &status) || failure.Error() != source.Error || status.Status != 400 {
		t.Fatal("native error boundary", failure)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("native delayed rejection/drained error body")
	}
	cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err = m.ShutdownSession(cleanup); err != nil {
		t.Fatal(err)
	}
}
