package clibootstrap

import (
	"bytes"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/remoteio"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

// Runtime Close cancels remote hydration/append before the host waits for its
// Owner. An ordinary tool/ASK interrupt does not cancel committed persistence.
func (m *MainPrint) BeginNativeRemoteRuntimeClose() {
	if m != nil {
		m.nativeIngressLife.close()
	}
}

func (m *MainPrint) nativeRemoteHTTP(env map[string]string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = func(request *http.Request) (*url.URL, error) {
		selection := provider.ResolveNativeProxySelection(request.URL.String(), env)
		if selection.ProxyURL == "" || selection.Bypass {
			return nil, nil
		}
		return url.Parse(selection.ProxyURL)
	}
	if err := tlsconfig.ValidateOriginalTLSOptions(env); err != nil {
		return nil, err
	}
	certs, err := m.tls.Certificates(env)
	if err != nil {
		return nil, err
	}
	transport.TLSClientConfig, err = tlsconfig.VerifiedConfigFromCertificates(certs)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: transport}, nil
}

func (m *MainPrint) prepareNativeResumeIdentifier() {
	if m.nativeResumeParsedSID != "" {
		return
	}
	selector := m.options.Arguments.Resume
	if selector == "" || strings.HasSuffix(strings.ToLower(selector), ".jsonl") {
		return
	}
	if printResumeUUID.MatchString(selector) {
		m.nativeResumeParsedSID = selector
		return
	}
	if parsed, err := url.Parse(selector); err == nil && parsed.Scheme != "" {
		m.nativeResumeIsURL = true
		m.nativeResumeParsedSID = uuidMain()
	}
}

func (m *MainPrint) hydrateNativeLegacyIngress(scope *cooperative.Scope) error {
	if !m.nativeResumeIsURL || m.options.Native == nil || !m.options.Native.NativeProcess || !envTruthyNative(m.nativeOpenAIEnvironment()["ENABLE_SESSION_PERSISTENCE"]) {
		return nil
	}
	if m.nativeIngress == nil {
		env := m.nativeOpenAIEnvironment()
		client, err := m.nativeRemoteHTTP(env)
		if err != nil {
			return err
		}
		m.options.SessionIngressAuth.UpdateEnvironment(env)
		m.nativeIngress = remoteio.NewIngress(client, m.options.SessionIngressAuth, nil)
	}
	ingress, sid, target := m.nativeIngress, m.nativeResumeParsedSID, m.options.Arguments.Resume
	// Capture live environment while owned. The native GET itself never reads
	// owner state from a goroutine or mutates the settings authority.
	afterCompact := envTruthyNative(m.nativeOpenAIEnvironment()["CLAUDE_AFTER_LAST_COMPACT"])
	ingress.SetAfterLastCompact(afterCompact)
	life := m.nativeIngressLife.context()
	files := mainResumeFiles{n: m.options.Native}
	config := m.options.Native.ConfigHomeDirectory()
	if config == "" {
		config = filepath.Join(m.options.Native.Home, ".claude")
	}
	project := filepath.Join(config, "projects", projectpath.Sanitize(m.state.Snapshot().OriginalCWD))
	future, err := nativeSkillWork(m, scope, func() (struct{}, error) {
		rows, readErr := ingress.Read(life, sid, target)
		if readErr != nil {
			if life.Err() != nil {
				return struct{}{}, life.Err()
			}
			return struct{}{}, nil
		}
		if err := files.MkdirAll(project); err != nil {
			return struct{}{}, nil
		}
		var buffer bytes.Buffer
		for _, row := range rows {
			buffer.Write(protocol.StringifyValidatedJavaScriptJSON(row))
			buffer.WriteByte('\n')
		}
		if err := files.WriteFile(filepath.Join(project, sid+".jsonl"), buffer.Bytes()); err != nil {
			return struct{}{}, nil
		}
		return struct{}{}, nil
	})
	if err != nil {
		return err
	}
	_, err = future.Await(scope)
	// Source finally registers this sink even when hydration failed; a later
	// append must use the actual URL and fail rather than silently stay local.
	if bindErr := m.Controller.BindLegacyIngress(scope, ingress, target, life, func() bool { return envTruthyNative(m.nativeOpenAIEnvironment()["ENABLE_SESSION_PERSISTENCE"]) }, m.nativeIngressLife.fail); err == nil {
		err = bindErr
	}
	return err
}
