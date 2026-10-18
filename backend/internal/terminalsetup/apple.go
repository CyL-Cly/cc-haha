package terminalsetup

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Execution struct {
	Code           int
	Stdout, Stderr string
}

func (options Options) log(err error) {
	if options.LogError != nil {
		options.LogError(err)
	}
}
func (options Options) plistPath() string {
	return filepath.Join(options.Home, "Library", "Preferences", "com.apple.Terminal.plist")
}
func (options Options) markAppleComplete() error {
	return options.SaveFields(protocol.NewJSONObject(protocol.JSONField{Name: "appleTerminalSetupInProgress", Value: json.RawMessage("false")}))
}

func (options Options) backupApple(ctx context.Context) (string, error) {
	path := options.plistPath()
	backup := path + ".bak"
	if options.Execute(ctx, "defaults", []string{"export", "com.apple.Terminal", path}).Code != 0 {
		return "", nil
	}
	if _, err := options.Stat(path); err != nil {
		return "", nil
	}
	// The original does not inspect this second export's code. Recovery still
	// checks whether that physical backup exists before attempting an import.
	options.Execute(ctx, "defaults", []string{"export", "com.apple.Terminal", backup})
	err := options.SaveFields(protocol.NewJSONObject(protocol.JSONField{Name: "appleTerminalSetupInProgress", Value: json.RawMessage("true")}, protocol.JSONField{Name: "appleTerminalBackupPath", Value: quoteApple(backup)}))
	if err != nil {
		options.log(err)
		return "", nil
	}
	return backup, nil
}

func quoteApple(value string) json.RawMessage { data, _ := json.Marshal(value); return data }

func (options Options) restoreApple(ctx context.Context) (status, path string, err error) {
	global, err := options.GetFields()
	if err != nil {
		return "", "", err
	}
	if !appleTruthy(global.Get("appleTerminalSetupInProgress")) {
		return "no_backup", "", nil
	}
	path = global.String("appleTerminalBackupPath")
	if path == "" {
		return "no_backup", "", options.markAppleComplete()
	}
	if _, err = options.Stat(path); err != nil {
		return "no_backup", "", options.markAppleComplete()
	}
	if options.Execute(ctx, "defaults", []string{"import", "com.apple.Terminal", path}).Code != 0 {
		return "failed", path, nil
	}
	options.Execute(ctx, "killall", []string{"cfprefsd"})
	return "restored", "", options.markAppleComplete()
}

func appleTruthy(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case float64:
		return typed != 0
	case string:
		return typed != ""
	default:
		return true
	}
}

func (options Options) setAppleProfile(ctx context.Context, profile, property, value string) bool {
	path := options.plistPath()
	command := "Add :'Window Settings':'" + profile + "':" + property + " bool " + value
	if options.Execute(ctx, "/usr/libexec/PlistBuddy", []string{"-c", command, path}).Code != 0 {
		command = "Set :'Window Settings':'" + profile + "':" + property + " " + value
		if options.Execute(ctx, "/usr/libexec/PlistBuddy", []string{"-c", command, path}).Code != 0 {
			message := "Failed to enable Option as Meta key for Terminal.app profile: "
			if property == "Bell" {
				message = "Failed to disable audio bell for Terminal.app profile: "
			}
			options.log(errors.New(message + profile))
			return false
		}
	}
	return true
}

func (options Options) configureApple(ctx context.Context) error {
	backup, err := options.backupApple(ctx)
	if err != nil {
		return err
	}
	if backup == "" {
		return errors.New("Failed to create backup of Terminal.app preferences, bailing out")
	}
	defaultProfile := options.Execute(ctx, "defaults", []string{"read", "com.apple.Terminal", "Default Window Settings"})
	defaultName := protocol.TrimJavaScriptString(defaultProfile.Stdout)
	if defaultProfile.Code != 0 || defaultName == "" {
		return errors.New("Failed to read default Terminal.app profile")
	}
	startupProfile := options.Execute(ctx, "defaults", []string{"read", "com.apple.Terminal", "Startup Window Settings"})
	startupName := protocol.TrimJavaScriptString(startupProfile.Stdout)
	if startupProfile.Code != 0 || startupName == "" {
		return errors.New("Failed to read startup Terminal.app profile")
	}
	updated := false
	profiles := []string{defaultName}
	if defaultName != startupName {
		profiles = append(profiles, startupName)
	}
	for _, profile := range profiles {
		meta := options.setAppleProfile(ctx, profile, "useOptionAsMetaKey", "true")
		bell := options.setAppleProfile(ctx, profile, "Bell", "false")
		updated = updated || meta || bell
	}
	if !updated {
		return errors.New("Failed to enable Option as Meta key or disable audio bell for any Terminal.app profile")
	}
	options.Execute(ctx, "killall", []string{"cfprefsd"})
	return options.markAppleComplete()
}

func (options Options) installApple(ctx context.Context) (string, error) {
	if options.Execute == nil || options.Stat == nil || options.GetFields == nil {
		return "", errors.New("Apple Terminal setup requires its actual preference process and recovery consumers")
	}
	if err := options.configureApple(ctx); err != nil {
		options.log(err)
		status, backup, restoreErr := options.restoreApple(ctx)
		if restoreErr != nil {
			return "", restoreErr
		}
		message := "Failed to enable Option as Meta key for Terminal.app."
		switch status {
		case "restored":
			message += " Your settings have been restored from backup."
		case "failed":
			message += " Restoring from backup failed, try manually with: defaults import com.apple.Terminal " + backup
		default:
			message += " No backup was available to restore from."
		}
		return "", errors.New(message)
	}
	return options.style("success", "Configured Terminal.app settings:") + "\n" + options.style("success", "- Enabled \"Use Option as Meta key\"") + "\n" + options.style("success", "- Switched to visual bell") + "\n" + options.style("dim", "Option+Enter will now enter a newline.") + "\n" + options.style("dim", "You must restart Terminal.app for changes to take effect.") + "\n", nil
}
