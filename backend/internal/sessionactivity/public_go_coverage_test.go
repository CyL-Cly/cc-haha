// Standard Go coverage plumbing only; ordinary and race tests keep their
// existing command arguments and private product environments.
package sessionactivity_test

import (
	"github.com/NanmiCoder/cc-haha/backend/testdata/gochildcoverage"
	"os/exec"
	"testing"
)

func alignPublicGoCoverage()                            { gochildcoverage.Align() }
func capturePublicGoCoverage(t *testing.T, c *exec.Cmd) { gochildcoverage.Capture(t, c) }
