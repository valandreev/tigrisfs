package main

import "testing"

func TestExecuteIntegrationCommandShow(t *testing.T) {
	called := false
	resp := executeIntegrationCommand(nil, func() { called = true }, integrationCommand{Action: "show"})
	if !resp.Success {
		t.Fatalf("expected show command success, got message %q", resp.Message)
	}
	if !called {
		t.Fatal("expected show callback to be invoked")
	}
}

func TestExecuteIntegrationCommandValidation(t *testing.T) {
	resp := executeIntegrationCommand(nil, nil, integrationCommand{Action: "pin"})
	if resp.Success {
		t.Fatal("expected pin command without path to fail")
	}
	if resp.Message == "" {
		t.Fatal("expected validation error message")
	}

	resp = executeIntegrationCommand(nil, nil, integrationCommand{Action: "invalid"})
	if resp.Success {
		t.Fatal("expected unsupported command to fail")
	}

	resp = executeIntegrationCommand(nil, nil, integrationCommand{Action: "unmount", Path: "/tmp/demo"})
	if resp.Success {
		t.Fatal("expected unmount without manager to fail")
	}
}
