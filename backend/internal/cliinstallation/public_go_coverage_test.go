// Standard Go coverage plumbing only; ordinary and race tests keep their
// existing command arguments and private product environments.
package cliinstallation

import (
	"github.com/NanmiCoder/cc-haha/backend/testdata/gochildcoverage"
	"os/exec"
	"testing"
)

func TestMain(m *testing.M)                             { gochildcoverage.Main(m) }
func capturePublicGoCoverage(t *testing.T, c *exec.Cmd) { gochildcoverage.Capture(t, c) }
