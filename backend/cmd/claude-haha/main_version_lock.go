package main

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cliinstallation"
	"os"
	"runtime"
)

func holdInstalledCLIVersion() func() {
	env := processEnvironment()
	home := env["HOME"]
	if runtime.GOOS == "windows" && env["USERPROFILE"] != "" {
		home = env["USERPROFILE"]
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	installer, err := cliinstallation.New(cliinstallation.Options{Home: home, Environment: env, Platform: runtime.GOOS, Architecture: runtime.GOARCH})
	if err != nil {
		return func() {}
	}
	return installer.HoldCurrent()
}
