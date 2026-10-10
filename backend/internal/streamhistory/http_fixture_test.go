package streamhistory

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

type httpFixtureInput struct {
	fixtureInput
	HTTPOrigin string `json:"httpOrigin"`
	Prepared   struct {
		Path      string            `json:"path"`
		BodyBytes string            `json:"bodyBytes"`
		Headers   map[string]string `json:"headers"`
	} `json:"prepared"`
}

func validateHTTPFixture(input httpFixtureInput) error {
	if os.Getenv("NODE_ENV") != "production" || os.Getenv("CC_HAHA_STREAM_HTTP_FIXTURE") != "1" || os.Getenv("CC_HAHA_STREAM_HISTORY_FIXTURE") != "1" {
		return errors.New("explicit HTTP fixture production profile required")
	}
	if input.HomeDir == "" || !filepath.IsAbs(input.HomeDir) || input.HomeDir != os.Getenv("HOME") || input.HomeDir != os.Getenv("USERPROFILE") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.HomeDir, ".claude") || os.Getenv("BUN_OPTIONS") != "--no-env-file" {
		return errors.New("explicit disposable HTTP fixture HOME/config required")
	}
	home, err := filepath.EvalSymlinks(input.HomeDir)
	if err != nil {
		return err
	}
	allocationRoot := os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT")
	if allocationRoot == "" || !filepath.IsAbs(allocationRoot) {
		return errors.New("HTTP fixture requires a trusted allocation root")
	}
	root, err := filepath.EvalSymlinks(allocationRoot)
	if err != nil || !strings.HasPrefix(home, root+string(filepath.Separator)) {
		return errors.New("HTTP fixture requires a trusted allocation root")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil || cwd != home {
		return errors.New("HTTP fixture cwd must be its private HOME")
	}
	stat, err := os.Lstat(input.HomeDir)
	if err != nil || stat.Mode()&os.ModeSymlink != 0 || !stat.IsDir() {
		return errors.New("HTTP fixture HOME must be a regular directory")
	}
	config, err := os.Lstat(filepath.Join(input.HomeDir, ".claude"))
	if err != nil || config.Mode()&os.ModeSymlink != 0 || !config.IsDir() {
		return errors.New("HTTP fixture config must be a regular private directory")
	}
	origin, err := url.Parse(input.HTTPOrigin)
	if err != nil || origin.Scheme != "http" || origin.Hostname() != "127.0.0.1" || origin.Port() == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || input.HTTPOrigin != os.Getenv("ANTHROPIC_BASE_URL") {
		return errors.New("HTTP fixture requires its exact explicit loopback origin")
	}
	path, err := url.ParseRequestURI(input.Prepared.Path)
	if err != nil || path.Path != "/v1/messages" || path.Host != "" || path.Fragment != "" {
		return errors.New("HTTP fixture requires the captured Messages path")
	}
	return nil
}

func observeHTTPFixture(input httpFixtureInput) (fixtureObservation, error) {
	body, err := hex.DecodeString(input.Prepared.BodyBytes)
	if err != nil || !json.Valid(body) {
		return fixtureObservation{}, errors.New("HTTP fixture requires captured JSON request bytes")
	}
	headers := http.Header{}
	for name, value := range input.Prepared.Headers {
		if name != "anthropic-beta" && name != "x-stainless-helper" {
			return fixtureObservation{}, errors.New("HTTP fixture only accepts resolved beta/helper caller headers")
		}
		headers.Set(name, value)
	}
	// Config is explicit and never reads provider/auth/user state. Redirects are
	// denied; only the captured Messages path at the validated origin is sent.
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	client := provider.NewAnthropic(provider.AnthropicConfig{
		BaseURL: input.HTTPOrigin, APIKey: "offline-fixture-key", Timeout: time.Minute,
		HTTPClient: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	})
	prepared := &provider.MessagesRequest{Body: body, Path: input.Prepared.Path, Headers: headers}
	stream, err := client.OpenMessagesStream(context.Background(), prepared)
	if err != nil {
		return fixtureObservation{}, fmt.Errorf("outside HTTP history profile: request failure: %w", err)
	}
	defer stream.Close()
	var transportFailure error
	result, err := observeFixtureFromParts(input.fixtureInput, stream.RequestID, func(feed func(json.RawMessage) error) error {
		for {
			part, err := stream.Next()
			if err == io.EOF {
				// Pinned SDK abort can be a quiet iterator EOF. This corpus only
				// accepts a genuine transport EOF; cancellation is another policy.
				if cause := stream.AbortCause(); cause != nil {
					transportFailure = &fixtureProducerFailure{fmt.Errorf("outside HTTP history profile: aborted EOF: %w", cause)}
				}
				return transportFailure
			}
			if err != nil {
				transportFailure = &fixtureProducerFailure{fmt.Errorf("outside HTTP history profile: SDK/transport failure: %w", err)}
				return transportFailure
			}
			if err := feed(part); err != nil {
				return err // Source switch failure, handled by Controller.Finish.
			}
		}
	})
	if transportFailure != nil {
		// Do not publish the projection or counterfeit assistant feedback for
		// an error policy this component has not migrated.
		return fixtureObservation{}, transportFailure
	}
	return result, err
}

func TestStreamHTTPFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_STREAM_HTTP_FIXTURE") != "1" {
		t.Skip("used only by the private loopback differential lane")
	}
	var input httpFixtureInput
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		t.Fatal(err)
	}
	if err := validateHTTPFixture(input); err != nil {
		t.Fatal(err)
	}
	result, err := observeHTTPFixture(input)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("STREAM_HTTP_RESULT:%s\n", raw)
}
