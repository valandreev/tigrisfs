package main

import "testing"

func TestParseStartupOptions(t *testing.T) {
	opts := parseStartupOptions([]string{
		"--integration-action", "PIN",
		"--integration-path", "  /tmp/mount/file.txt  ",
		"--integration-recursive",
		"--install-file-manager-integration",
	})

	if opts.Action != "pin" {
		t.Fatalf("expected action pin, got %q", opts.Action)
	}
	if opts.Path != "/tmp/mount/file.txt" {
		t.Fatalf("expected trimmed path, got %q", opts.Path)
	}
	if !opts.Recursive {
		t.Fatal("expected recursive option to be true")
	}
	if !opts.InstallIntegration {
		t.Fatal("expected install integration option to be true")
	}
}

func TestParseStartupOptionsUnmountAction(t *testing.T) {
	opts := parseStartupOptions([]string{
		"--integration-action", "UNMOUNT",
		"--integration-path", "  /tmp/mount/dir  ",
	})
	if opts.Action != "unmount" {
		t.Fatalf("expected action unmount, got %q", opts.Action)
	}
	if opts.Path != "/tmp/mount/dir" {
		t.Fatalf("expected normalized path, got %q", opts.Path)
	}
}
