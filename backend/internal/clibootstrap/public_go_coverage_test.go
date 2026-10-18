// Instrumentation stays in this test-only adapter; race and ordinary builds
// keep their existing subprocess arguments and private product environments.
package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/testdata/gochildcoverage"
	"os/exec"
	"testing"
)

func TestMain(m *testing.M)                                   { gochildcoverage.Main(m) }
func capturePublicGoCoverage(t *testing.T, command *exec.Cmd) { gochildcoverage.Capture(t, command) }

func registerBuiltPublicGoCoverage(t *testing.T, command *exec.Cmd) {
	gochildcoverage.RegisterBuilt(t, command)
}
