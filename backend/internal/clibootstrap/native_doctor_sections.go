package clibootstrap

import (
	"fmt"
	"strings"
)

// The original SandboxDoctorSection is absent on unsupported/disabled hosts
// and when every dependency check passes. Warnings alone still have a section.
func nativeDoctorSandboxText(supported, enabled bool, errors, warnings []string) string {
	if !supported || !enabled || len(errors)+len(warnings) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString("\nSandbox\n")
	if len(errors) > 0 {
		out.WriteString("Status: Missing dependencies\n")
	} else {
		out.WriteString("Status: Available (with warnings)\n")
	}
	for _, rows := range [][]string{errors, warnings} {
		for _, row := range rows {
			fmt.Fprintf(&out, "  %s\n", row)
		}
	}
	if len(errors) > 0 {
		out.WriteString("Run /sandbox for install instructions\n")
	}
	return out.String()
}
