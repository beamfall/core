package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseWorkerShimGuardianDescriptor(t *testing.T) {
	argv, fd, err := parseWorkerShimArgs([]string{
		"--guardian-fd", "4",
		"--",
		"/opt/plugins/worker",
		"--host-api-socket", "/tmp/host.sock",
	})
	if err != nil {
		t.Fatalf("parse worker shim: %v", err)
	}
	want := []string{"/opt/plugins/worker", "--host-api-socket", "/tmp/host.sock"}
	if fd != 4 || !reflect.DeepEqual(argv, want) {
		t.Fatalf("parsed argv=%q fd=%d, want argv=%q fd=4", argv, fd, want)
	}
}

func TestParseWorkerShimRejectsInvalidGuardianDescriptor(t *testing.T) {
	for _, value := range []string{"", "worker", "2"} {
		args := []string{"--guardian-fd"}
		if value != "" {
			args = append(args, value)
		}
		args = append(args, "--", "/opt/plugins/worker")
		_, _, err := parseWorkerShimArgs(args)
		if err == nil || !strings.Contains(err.Error(), "guardian") {
			t.Fatalf("guardian descriptor %q error = %v", value, err)
		}
	}
}
