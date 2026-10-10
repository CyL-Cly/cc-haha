package sandboxruntime

type LinuxDependencyStatus struct {
	HasBwrap        bool `json:"hasBwrap"`
	HasSocat        bool `json:"hasSocat"`
	HasSeccompBPF   bool `json:"hasSeccompBpf"`
	HasSeccompApply bool `json:"hasSeccompApply"`
}
type DependencyCheck struct {
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

func CheckLinuxDependencies(status LinuxDependencyStatus) DependencyCheck {
	result := DependencyCheck{Errors: []string{}, Warnings: []string{}}
	if !status.HasBwrap {
		result.Errors = append(result.Errors, "bubblewrap (bwrap) not installed")
	}
	if !status.HasSocat {
		result.Errors = append(result.Errors, "socat not installed")
	}
	if !status.HasSeccompBPF || !status.HasSeccompApply {
		result.Warnings = append(result.Warnings, "seccomp not available - unix socket access not restricted")
	}
	return result
}
