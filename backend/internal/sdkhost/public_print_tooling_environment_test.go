package sdkhost

import (
	"reflect"
	"strings"
	"testing"
)

func TestPublicPrintGoToolingEnvironment(t *testing.T) {
	for _, name := range []string{"GOCOVERDIR", "TEST_TELEMETRY_DIR"} {
		t.Run(name, func(t *testing.T) {
			original := map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:12345", "ANTHROPIC_API_KEY": "offline-key", name: "/private/temp/real tooling 一/"}
			before := map[string]string{}
			for k, v := range original {
				before[k] = v
			}
			request, err := publicPrintRequestEnvironment(original)
			if err != nil {
				t.Fatalf("actual Go tooling process input %s must be retained outside request projection: %v", name, err)
			}
			if !reflect.DeepEqual(original, before) {
				t.Fatal("original environment or exact tooling value changed")
			}
			expected := map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:12345", "ANTHROPIC_API_KEY": "offline-key"}
			if !reflect.DeepEqual(request, expected) {
				t.Fatalf("tooling name leaked into provider request options: %#v", request)
			}
		})
	}
}

func TestPublicPrintGoToolingDoesNotAdmitUnknownFeatures(t *testing.T) {
	for _, name := range []string{"CC_HAHA_LIFECYCLE_REPORT_DIR", "CC_HAHA_NATIVE_REF_REPORT_DIR", "GOCOVERDIR_UNMIGRATED_FEATURE", "TEST_TELEMETRY_DIR_UNMIGRATED_FEATURE", "PRIVATE_UNMIGRATED_FEATURE"} {
		t.Run(name, func(t *testing.T) {
			original := map[string]string{name: "1"}
			_, err := publicPrintRequestEnvironment(original)
			if err == nil || !strings.Contains(err.Error(), "normal print original environment "+name) {
				t.Fatalf("unported feature was admitted: %v", err)
			}
			if original[name] != "1" || len(original) != 1 {
				t.Fatal("failed projection changed original environment")
			}
		})
	}
}
