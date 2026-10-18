package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestPrivateSmokeAbsentConfigValidatesDefaultWithoutChangingEnvironment(t *testing.T) {
	environment := privateSmokePrefetchEnvironment(t)
	delete(environment, "CLAUDE_CONFIG_DIR")
	original := make(map[string]string, len(environment))
	for key, value := range environment {
		original[key] = value
	}
	read, err := SmokeSecurity(environment)
	if err != nil || read == nil {
		t.Fatalf("private absent configuration was rejected: %v", err)
	}
	for _, operation := range []string{"find-generic-password", "add-generic-password", "delete-generic-password"} {
		if _, err := read(context.Background(), securestorage.Command{Args: []string{operation}}); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("actual credential operation was not denied: %s %v", operation, err)
		}
	}
	if !reflect.DeepEqual(environment, original) {
		t.Fatal("validation filled or changed the original environment")
	}
	for _, value := range []string{"", "relative-config"} {
		copy := make(map[string]string, len(environment)+1)
		for key, value := range environment {
			copy[key] = value
		}
		copy["CLAUDE_CONFIG_DIR"] = value
		if read, err := SmokeSecurity(copy); read != nil || err == nil {
			t.Fatalf("explicit nonabsolute configuration was admitted: %q %v", value, err)
		}
		if start, err := SmokeSecurityPrefetch(copy); start != nil || err == nil {
			t.Fatalf("explicit nonabsolute configuration admitted prefetch: %q %v", value, err)
		}
	}
	home := environment["HOME"]
	if runtime.GOOS == "windows" {
		home = environment["USERPROFILE"]
	}
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(home, ".claude")); err != nil {
		t.Fatal(err)
	}
	if read, err := SmokeSecurity(environment); read != nil || err == nil {
		t.Fatal("implicit default configuration escaped through a symlink", err)
	}
	if start, err := SmokeSecurityPrefetch(environment); start != nil || err == nil {
		t.Fatal("implicit default configuration admitted escaped prefetch", err)
	}
	if !reflect.DeepEqual(environment, original) {
		t.Fatal("rejected validation changed the original environment")
	}
}

func TestPrivateSmokeAbsentConfigPrefetchPhysicallyJoinsAbsentResults(t *testing.T) {
	environment := privateSmokePrefetchEnvironment(t)
	delete(environment, "CLAUDE_CONFIG_DIR")
	start, err := SmokeSecurityPrefetch(environment)
	if err != nil || start == nil {
		t.Fatalf("private absent configuration did not bind prefetch: %v", err)
	}
	read, err := SmokeSecurity(environment)
	if err != nil || read == nil {
		t.Fatal(err)
	}
	home := environment["HOME"]
	if runtime.GOOS == "windows" {
		home = environment["USERPROFILE"]
	}
	reads := 0
	var processes []*securestorage.KeychainPrefetchProcess
	store, err := securestorage.New(securestorage.Options{
		Home: home, ConfigDir: filepath.Join(home, ".claude"), Platform: "darwin",
		Environment: map[string]string{"USER": "private-default-config"},
		RunSecurity: func(ctx context.Context, call securestorage.Command) ([]byte, error) {
			reads++
			return read(ctx, call)
		},
		StartSecurity: func(ctx context.Context, call securestorage.Command) (*securestorage.KeychainPrefetchProcess, error) {
			process, err := start(ctx, call)
			if err == nil {
				processes = append(processes, process)
			}
			return process, err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prefetch, err := store.NewKeychainPrefetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer prefetch.Close()
	if err := prefetch.StartOAuth(); err != nil {
		t.Fatal(err)
	}
	if err := prefetch.StartLegacy(); err != nil {
		t.Fatal(err)
	}
	if len(processes) != 2 || processes[0].PID() <= 0 || processes[1].PID() <= 0 || processes[0].PID() == processes[1].PID() {
		t.Fatal("prefetch did not start two distinct actual absent-result processes")
	}
	if err := prefetch.RunOAuth(); err != nil {
		t.Fatal(err)
	}
	if err := prefetch.RunLegacy(); err != nil {
		t.Fatal(err)
	}
	if err := prefetch.Publish(); err != nil {
		t.Fatal(err)
	}
	for _, process := range processes {
		select {
		case <-process.Done():
		default:
			t.Fatal("absent result was published before actual child Wait", process.PID())
		}
	}
	if value, err := store.ReadSourceJSON(ctx); err != nil || string(value) != "{}" || reads != 0 {
		t.Fatal("actual absent prefetch did not prime the normal credential cache", string(value), reads, err)
	}
	if _, present := environment["CLAUDE_CONFIG_DIR"]; present {
		t.Fatal("prefetch filled the original environment")
	}
}
