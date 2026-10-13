package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/automode"
	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

func (m *MainCommand) AutoMode(ctx context.Context, scope *cooperative.Scope, operation, requestedModel string) (int, error) {
	if m == nil || !scope.BelongsTo(m.Owner) || m.Native == nil || !m.Native.NativeProcess {
		return 1, cooperative.ErrRestricted
	}
	if operation == "defaults" {
		return 0, automode.DefaultsHandler(m.Output, true)
	}
	snapshot := automode.Snapshot{TranscriptClassifier: true, AnthropicInternal: m.Native.Environment["USER_TYPE"] == "ant", Sources: map[string]json.RawMessage{}}
	for _, source := range []string{"userSettings", "localSettings", "flagSettings", "policySettings"} {
		settings, err := m.Startup.GetSettingsForSource(source)
		if err != nil {
			return 1, err
		}
		if settings != nil {
			snapshot.Sources[source], err = settings.MarshalJSON()
			if err != nil {
				return 1, err
			}
		}
	}
	if operation == "config" {
		return 0, automode.ConfigHandler(m.Output, snapshot)
	}
	if operation != "critique" {
		return 1, mainCap("auto-mode parsed action")
	}
	options, custom, err := automode.CritiqueOptions(snapshot)
	if err != nil {
		return 1, err
	}
	if !custom {
		_, err = io.WriteString(m.Output, automode.CritiqueNoCustomRules)
		return 0, err
	}
	if _, err = io.WriteString(m.Output, automode.CritiqueAnalyzing); err != nil {
		return 1, err
	}
	response, err := m.autoModeCritiqueQuery(ctx, scope, options, requestedModel)
	if err != nil {
		_, writeError := fmt.Fprintln(m.Error, "Failed to analyze rules: "+err.Error())
		return 1, writeError
	}
	return 0, automode.WriteCritiqueResponse(m.Output, response.Raw)
}

// Borrow the same credential, helper, cloud and TLS producers as ordinary MCP.
// This bundle creates no ASK, Controller, tools, hooks or transcript writer.
func (m *MainCommand) autoModeCritiqueQuery(ctx context.Context, scope *cooperative.Scope, options requestcontext.SideQueryInput, requested string) (provider.MessageResponse, error) {
	var refs ownerasync.NativeReferences
	binding, err := refs.Bind(m.Owner)
	if err != nil {
		return provider.MessageResponse{}, err
	}
	state, err := bootstrapstate.New(m.Owner, uuidMain(), m.Native.CWD, func() (string, error) { return uuidMain(), nil })
	if err != nil {
		return provider.MessageResponse{}, err
	}
	tls, err := tlsconfig.NewState(m.Native.readFile)
	if err != nil {
		return provider.MessageResponse{}, err
	}
	auth := &MainPrint{Owner: m.Owner, Global: m.Global, Startup: m.Startup, state: state, environment: m.Settings.environment, secure: m.Secure, tls: tls, commandBinding: binding, options: MainPrintOptions{Native: m.Native, Version: m.version, Platform: m.credentialPlatform, PID: m.pid, Error: m.Error}}
	auth.initializeNativeAPIKeyHelper()
	auth.initializeNativeCloudSettings()
	defer func() {
		if auth.apiKeyHelper != nil {
			_ = auth.apiKeyHelper.Close(context.Background())
		}
		auth.closeNativeCloudCredentialCaches(context.Background())
	}()
	auth.openAIAuth, err = auth.NewNativeOpenAIAuth(auth.nativeOpenAIRefresh)
	if err != nil {
		return provider.MessageResponse{}, err
	}
	settings, err := m.Startup.GetInitialSettings()
	if err != nil {
		return provider.MessageResponse{}, err
	}
	model, _, err := auth.resolveNativeModel(ctx, scope, settings, requested)
	if err != nil {
		return provider.MessageResponse{}, err
	}
	env, err := sdkhost.NativePrintRequestEnvironment(auth.nativeOpenAIEnvironment())
	if err != nil {
		return provider.MessageResponse{}, err
	}
	global, err := m.Global.Get()
	if err != nil {
		return provider.MessageResponse{}, err
	}
	device := global.String("userID")
	if device == "" {
		device = uuidMain()
		if err = m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			next := current.Clone()
			next.Set("userID", quoteMain(device))
			return &next, nil
		}); err != nil {
			return provider.MessageResponse{}, err
		}
	}
	authority, err := cooperative.NewNativeAutoCritiqueModels(scope, m.Owner)
	if err != nil {
		return provider.MessageResponse{}, err
	}
	var response provider.MessageResponse
	err = authority.Run(scope, ctx, func(producer *cooperative.Scope) error {
		deps := requestretry.Dependencies{StreamOwner: m.Owner, StreamScope: producer, TLSState: tls, UUID: uuidMain, Now: m.Native.Now, Random: nativeMathRandom, NativeOpenIO: providernative.NewFactory(m.Owner, producer, nil), BodyIOAwait: func(kind provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
			if kind != provider.OwnerBodyRead && kind != provider.OwnerErrorBody {
				return nil, cooperative.ErrRestricted
			}
			return producer.AwaitBytes(cooperative.StreamNext, work)
		}, Sleep: func(ctx context.Context, millis float64) error {
			_, err := producer.AwaitBytes(cooperative.RetryTimer, func(wait context.Context) ([]byte, error) {
				timer := time.NewTimer(time.Duration(millis * float64(time.Millisecond)))
				defer timer.Stop()
				select {
				case <-timer.C:
					return nil, nil
				case <-ctx.Done():
					return nil, context.Cause(ctx)
				case <-wait.Done():
					return nil, context.Cause(wait)
				}
			})
			return err
		}}
		deps.NativeClaudeAuth, err = auth.nativeModelValidationClaudeAuth(producer)
		if err != nil {
			return err
		}
		deps.NativeAuthHelper, err = auth.nativeAuthHelperBinding(producer)
		if err != nil {
			return err
		}
		deps.NativeBedrock, err = auth.nativeBedrockBinding(producer)
		if err != nil {
			return err
		}
		deps.NativeVertex, err = auth.nativeVertexBinding(producer)
		if err != nil {
			return err
		}
		deps.NativeCodexOAuth = auth.nativeOpenAIConfig(producer)
		deps.NativeGrokOAuth = auth.nativeGrokConfig(producer)
		input := requestretry.Input{Context: requestcontext.Context{Options: requestcontext.Options{Model: model, QuerySource: "auto_mode_critique"}, Host: requestcontext.Host{Profile: requestcontext.NativeDirectRelayProfile, Env: env, DeviceID: device, SessionID: state.Snapshot().SessionID}}, Runtime: provider.SourceRuntime{Platform: m.credentialPlatform, Arch: runtime.GOARCH, Version: "v24.3.0"}}
		response, err = requestretry.RunNativeAutoCritique(ctx, input, settings.Clone(), options, deps, authority)
		return err
	})
	return response, err
}
