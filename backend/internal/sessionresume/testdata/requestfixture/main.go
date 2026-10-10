package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionresume"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func fail() error {
	return errors.New("Session resume fixture requires disposable HOME/config and ordinary private files")
}
func inside(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
func assertHome(home string) error {
	if !filepath.IsAbs(home) || strings.ContainsRune(home, 0) || home != os.Getenv("HOME") || os.Getenv("USERPROFILE") != home || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") || os.Getenv("BUN_OPTIONS") != "--no-env-file" {
		return fail()
	}
	info, err := os.Lstat(home)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fail()
	}
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		return fail()
	}
	root := os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT")
	if !filepath.IsAbs(root) {
		return fail()
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil || canonical == canonicalRoot || !inside(canonicalRoot, canonical) {
		return fail()
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fail()
	}
	canonicalCwd, err := filepath.EvalSymlinks(cwd)
	if err != nil || canonicalCwd != canonical {
		return fail()
	}
	config := filepath.Join(home, ".claude")
	if info, err := os.Lstat(config); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fail()
	}
	canonicalConfig, err := filepath.EvalSymlinks(config)
	if err != nil || !inside(canonical, canonicalConfig) {
		return fail()
	}
	return nil
}
func guard(home, path string, ordinary bool) error {
	if !inside(home, path) {
		return fail()
	}
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return fail()
	}
	for probe := path; ; probe = filepath.Dir(probe) {
		info, err := os.Lstat(probe)
		if errors.Is(err, os.ErrNotExist) {
			if probe == filepath.Dir(probe) {
				return fail()
			}
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || probe == path && ordinary && !info.Mode().IsRegular() {
			return fail()
		}
		canonical, err := filepath.EvalSymlinks(probe)
		if err != nil || !inside(canonicalHome, canonical) {
			return fail()
		}
		return nil
	}
}

type privateFS struct{ home string }

func (files privateFS) Open(path string) (sessionstore.File, error) {
	if err := guard(files.home, path, true); err != nil {
		panic(err)
	}
	return (sessionstore.OSFileSystem{}).Open(path)
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}

type input struct {
	Home, Origin, PromptSource string
	Runtime                    provider.SourceRuntime
	Classifier                 bool
}

func serialized(value any) string {
	raw, err := json.Marshal(value)
	must(err)
	raw, err = protocol.ParseAndStringifyJavaScriptJSON(raw)
	must(err)
	return string(raw)
}
func main() {
	var input input
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	must(decoder.Decode(&input))
	if decoder.Decode(new(any)) != io.EOF {
		panic("single original input required")
	}
	must(assertHome(input.Home))
	origin, err := url.Parse(input.Origin)
	must(err)
	if os.Getenv("CC_HAHA_SESSION_RESUME_REQUEST") != "1" || os.Getenv("NODE_ENV") != "production" || !input.Classifier || origin.Scheme != "http" || origin.Hostname() != "127.0.0.1" || origin.Port() == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || input.Origin != os.Getenv("ANTHROPIC_BASE_URL") {
		panic("Session resume request requires original private loopback inputs")
	}
	path := filepath.Join(input.Home, "session.jsonl")
	must(guard(input.Home, path, true))
	must(guard(input.Home, filepath.Join(input.Home, ".claude", ".claude.json"), true))
	cwd, err := os.Getwd()
	must(err)
	allocations := []map[string]any{}
	calls := 0
	uuid := func() string {
		calls++
		value := fmt.Sprintf("00000000-0000-4000-8000-%012d", calls)
		allocations = append(allocations, map[string]any{"kind": "uuid", "value": value})
		return value
	}
	now := func() time.Time {
		allocations = append(allocations, map[string]any{"kind": "time", "value": "2023-11-14T22:13:20.000Z"})
		return time.UnixMilli(1700000000000)
	}
	factory, err := messages.NewFactory(uuid, now)
	must(err)
	reducer, err := sessionresume.New(sessionresume.Options{Factory: factory, TranscriptClassifier: input.Classifier, RelativePath: func(raw json.RawMessage) (json.RawMessage, error) {
		return sessionresume.RelativePath(input.Home, cwd, raw)
	}, OnDiagnostic: func(event sessionresume.Diagnostic) { panic("unexpected request profile diagnostic: " + event.Name) }, OnError: func(err error) { panic(err) }})
	must(err)
	loader, err := sessionhistory.New(sessionhistory.Options{Files: privateFS{input.Home}, Stat: func(path string) (int64, error) {
		must(guard(input.Home, path, false))
		info, err := os.Stat(path)
		if err != nil {
			return 0, err
		}
		return info.Size(), nil
	}, ReadFile: func(path string) ([]byte, error) { must(guard(input.Home, path, false)); return os.ReadFile(path) }, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return false }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(event sessionhistory.Diagnostic) {
		panic("unexpected transcript profile diagnostic: " + event.Message)
	}})
	must(err)
	loaded, err := reducer.LoadPath(loader, path)
	must(err)
	resultSource := serialized(loaded)
	result, err := reducer.AppendUser(loaded.Result, json.RawMessage(input.PromptSource))
	must(err)
	generation := append([]map[string]any{}, allocations...)
	before := []string{}
	for _, message := range result.Messages {
		before = append(before, serialized(message))
	}
	var sid string
	must(json.Unmarshal(loaded.SessionID, &sid))
	if sid == "" {
		panic("actual recovered SID required")
	}
	env := map[string]string{"ANTHROPIC_BASE_URL": input.Origin, "ANTHROPIC_API_KEY": "offline-fixture-key"}
	prepared, err := sessionresume.BuildRequest(result, requestcontext.Context{SystemPrompt: []json.RawMessage{}, Thinking: requestcontext.Thinking{Type: "disabled"}, Options: requestcontext.Options{Model: "fixture-model", QuerySource: "sdk", IsNonInteractiveSession: true}, Host: requestcontext.Host{Profile: requestcontext.DirectRelayProfile, Env: env, DeviceID: "fixture-resume-device", SessionID: sid, SDKBetas: []string{}, TTLEligible: false, TTLAllowlist: []string{}}}, requestcontext.Dependencies{UUID: uuid, Now: now})
	must(err)
	key := "offline-fixture-key"
	options := provider.ResolveDirectClientOptions(provider.DirectClientInput{Env: env, FallbackAPIKey: &key, SessionID: sid, Runtime: input.Runtime, RequestID: uuid})
	config := options.StreamConfig()
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	config.HTTPClient = &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	requests, prefetch := []map[string]any{}, []any{}
	finalize := config.FinalizeHeaders
	config.FinalizeHeaders = func(target string, header http.Header) (http.Header, error) {
		resolved, err := finalize(target, header)
		if err != nil {
			return nil, err
		}
		url, err := url.Parse(target)
		if err != nil {
			return nil, err
		}
		headers := [][2]string{}
		for name, values := range resolved {
			headers = append(headers, [2]string{strings.ToLower(name), strings.Join(values, ", ")})
		}
		sort.Slice(headers, func(i, j int) bool { return headers[i][0] < headers[j][0] })
		requests = append(requests, map[string]any{"path": url.RequestURI(), "method": "POST", "headers": headers, "body": string(prepared.PreparedRequest.Body)})
		prefetch = append(prefetch, append([]map[string]any{}, allocations...))
		return resolved, nil
	}
	stream, err := provider.NewAnthropic(config).OpenMessagesStream(context.Background(), prepared.PreparedRequest)
	must(err)
	for {
		_, err := stream.Next()
		if err == io.EOF {
			break
		}
		must(err)
	}
	must(stream.Close())
	fmt.Print(serialized(map[string]any{"observation": map[string]any{"resultSource": resultSource, "generation": generation, "historyBefore": before, "requests": requests}, "sourceOnly": map[string]any{"prefetchAllocations": prefetch}}))
}
