package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

func (m *MainPrint) resolveNativeFallbackModel() error {
	requested := m.options.Arguments.FallbackModel
	if requested == "" || m.nativeModelSelector == nil {
		return nil
	}
	value := quoteMain(requested)
	if requested == "default" {
		value = m.nativeModelSelector.DefaultSetting()
	}
	parsed, err := m.nativeModelSelector.Parse(value)
	if err != nil {
		return err
	}
	model, err := decodeNativeModel(parsed)
	if err == nil {
		m.options.Arguments.FallbackModel = model
	}
	return err
}

func (m *MainPrint) nativeHarnessPolicyHostOptions(options sdkhost.Options) sdkhost.Options {
	if options.Print != nil && options.Print.NativeProcess {
		options.NativeSDKEnvironmentUpdate = m.applyNativeSDKEnvironment
		options.NativeSDKPermissionControl = m.nativeSDKPermissionControl
	}
	if !m.options.Interactive {
		options.AuthStatus = m.options.CloudAuthStatus
		options.EnableAuthStatus = m.options.Arguments.EnableAuthStatus
		options.Workload = m.options.Arguments.Workload
		options.Print.MaxBudgetUSD = m.options.Arguments.MaxBudgetUSD
		options.Print.TaskBudget = m.options.Arguments.TaskBudget
		options.Print.FallbackModel = m.options.Arguments.FallbackModel
	}
	options.Print.ModelDisplayName = func(model string) (string, error) {
		if m.nativeModelSelector == nil {
			return model, nil
		}
		name, err := m.nativeModelSelector.PublicModelDisplayName(quoteMain(model))
		if name == "" {
			name = model
		}
		return name, err
	}
	return options
}
