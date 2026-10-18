//go:build linux

package tui

import (
	"strings"
	"testing"
)

func TestEditorLinuxProcessMetadataPreservesBirthAndParentForComplexComm(t *testing.T) {
	fields := make([]string, 20)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0] = "Z"
	fields[1] = "200"
	fields[19] = "123456"
	row, err := parseFileEditorLinuxProcess([]byte("201 (owned editor) odd) "+strings.Join(fields, " ")), 501)
	if err != nil || row.PID != 201 || row.Parent != 200 || row.UID != 501 || row.BirthFraction != 123456 || row.Stopped {
		t.Fatal(row, err)
	}
	fields[0] = "T"
	stopped, err := parseFileEditorLinuxProcess([]byte("201 (editor) "+strings.Join(fields, " ")), 501)
	if err != nil || !stopped.Stopped {
		t.Fatal("kernel stopped state lost", stopped, err)
	}
	for _, bad := range []string{"", "201 (editor) Z 200", "bad (editor) " + strings.Join(fields, " ")} {
		if _, err := parseFileEditorLinuxProcess([]byte(bad), 501); err == nil {
			t.Fatal("malformed process identity accepted", bad)
		}
	}
}
