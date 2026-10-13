package windowshell

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestActualNativeDiscoveryRejectsOversizedOutputAndCancelsProcess(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			command := "printf '%9000s' x"
			if stream == "stderr" {
				command += " >&2"
			}
			output, err := runProbe(ctx, "/bin/bash", []string{"--noprofile", "--norc", "-c", command}, t.TempDir(), map[string]string{"HOME": t.TempDir(), "PATH": "/usr/bin:/bin"}, 8192)
			if err == nil || len(output) != 0 || !strings.Contains(err.Error(), "exceeded its Source output buffer") {
				t.Fatal("oversized native discovery was accepted", len(output), err)
			}
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	output, err := runProbe(ctx, "/bin/bash", []string{"--noprofile", "--norc", "-c", "while :; do :; done"}, t.TempDir(), map[string]string{"HOME": t.TempDir(), "PATH": "/usr/bin:/bin"}, 8192)
	if err == nil || len(output) != 0 || time.Since(started) > time.Second {
		t.Fatal("owned cancelled native discovery did not join", time.Since(started), err)
	}
}
