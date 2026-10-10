package sdkhost

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

func nativeAuthHelperBound(scope *cooperative.Scope, options Options) bool {
	if options.Print == nil || !options.Print.NativeProcess || options.NativeAuthHelper == nil {
		return false
	}
	bindings, err := options.NativeAuthHelper(scope)
	return err == nil && bindings.Owns(options.Owner)
}
