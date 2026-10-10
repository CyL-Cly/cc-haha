package sdkhost

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

func nativeCloudSettingsBound(scope *cooperative.Scope, options Options) bool {
	return options.Print != nil && options.Print.NativeProcess && options.Print.NativeCloudSettings != nil && scope != nil && scope.BelongsTo(options.Owner) && options.Print.NativeCloudSettings(scope)
}
