package sdkpermissions

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

// NativeMigrationAutoModeEnabledState reuses the same cached feature producer
// as the native permission carousel. It neither initializes GrowthBook nor
// treats an external disk cache as a live configuration producer.
func NativeMigrationAutoModeEnabledState(environment func(string) string, global func() (*protocol.JSONObject, error)) string {
	startup := &NativeStartup{options: NativeStartupOptions{Environment: environment, Global: global}}
	return startup.autoEnabledState()
}
