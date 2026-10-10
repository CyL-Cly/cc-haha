package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/authhelper"
	"github.com/NanmiCoder/cc-haha/backend/internal/cloudauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func (m *MainPrint) initializeNativeCloudSettings() {
	if !m.options.Native.NativeProcess {
		return
	}
	if parent := m.nativeHelperParent(); parent != nil {
		m.awsCredentials = parent.awsCredentials
		m.awsSSOState = parent.awsSSOState
		m.awsINIState = parent.awsINIState
		m.gcpCredentials = parent.gcpCredentials
		m.cloudCredentialsBorrowed = true
		return
	}
	options := authhelper.Options{NowMillis: m.options.Native.NowMillis, Dispatch: func(settle func()) error {
		receipt, err := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(*cooperative.Scope) error { settle(); return nil })
		if err != nil {
			return err
		}
		return receipt.Wait()
	}, Error: func(text string) { m.environmentHookNotice(text, true) }}
	m.awsSSOState = &cloudauth.SSOState{}
	m.awsINIState = &cloudauth.AWSFileCache{}
	m.awsCredentials = authhelper.NewCloudCache(options)
	m.gcpCredentials = authhelper.NewCloudCache(options)
}

// Source consumes these settings only in Bedrock/Vertex prefetch and GetClient
// branches. Direct Anthropic/DeepSeek must retain old fields without executing
// cloud commands. Active cloud SDK/STS/ADC remains an explicit separate gate.
func (m *MainPrint) nativeCloudSettingsInactive(scope *cooperative.Scope) bool {
	if scope == nil || !scope.BelongsTo(m.Owner) || m.awsCredentials == nil || m.gcpCredentials == nil {
		return false
	}
	env := m.nativeOpenAIEnvironment()
	return !envTruthyNative(env["CLAUDE_CODE_USE_BEDROCK"]) && !envTruthyNative(env["CLAUDE_CODE_USE_VERTEX"])
}

func (m *MainPrint) nativeCloudSettingsBindings(scope *cooperative.Scope) bool {
	if m.nativeCloudSettingsInactive(scope) {
		return true
	}
	return scope != nil && scope.BelongsTo(m.Owner) && m.options.Native.NativeProcess && m.awsCredentials != nil && m.gcpCredentials != nil && (envTruthyNative(m.nativeOpenAIEnvironment()["CLAUDE_CODE_USE_BEDROCK"]) || envTruthyNative(m.nativeOpenAIEnvironment()["CLAUDE_CODE_USE_VERTEX"]))
}
func (m *MainPrint) clearNativeCloudCredentialCaches() {
	if m.awsCredentials != nil {
		m.awsCredentials.Clear()
	}
	if m.gcpCredentials != nil {
		m.gcpCredentials.Clear()
	}
}
func (m *MainPrint) closeNativeCloudCredentialCaches(ctx context.Context) {
	if m.cloudCredentialsBorrowed {
		return
	}
	if m.awsCredentials != nil {
		_ = m.awsCredentials.Close(ctx)
	}
	if m.gcpCredentials != nil {
		_ = m.gcpCredentials.Close(ctx)
	}
}
