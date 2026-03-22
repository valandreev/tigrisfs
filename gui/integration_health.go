package main

import (
	"fmt"
	"strings"
)

type platformIntegrationHealth struct {
	Supported          bool
	ExtensionInstalled bool
	ExtensionEnabled   bool
	ExtensionVersion   string
	ActionInstalled    bool
	Details            []string
}

type integrationHealthSnapshot struct {
	Summary string
	Details []string
}

func gatherIntegrationHealth(guiBinary string) integrationHealthSnapshot {
	snapshot := integrationHealthSnapshot{}
	details := make([]string, 0, 12)
	serverHealthy := false

	if err := sendIntegrationHealth(); err != nil {
		details = append(details, "Local API: unreachable ("+err.Error()+")")
	} else {
		serverHealthy = true
		details = append(details, "Local API: healthy")
	}

	platform := probePlatformIntegrationHealth(guiBinary)
	if !platform.Supported {
		snapshot.Summary = "Integration health: platform diagnostics unavailable"
		snapshot.Details = append(details, "Extension probe: not supported on this platform")
		return snapshot
	}

	installed := "not installed"
	if platform.ExtensionInstalled {
		installed = "installed"
	}
	enabled := "disabled"
	if platform.ExtensionEnabled {
		enabled = "enabled"
	}
	version := platform.ExtensionVersion
	if strings.TrimSpace(version) == "" {
		version = "unknown"
	}
	actions := "missing"
	if platform.ActionInstalled {
		actions = "installed"
	}

	details = append(details,
		fmt.Sprintf("%s extension: %s (%s, version %s)", fileManagerIntegrationName(), installed, enabled, version),
		fmt.Sprintf("Context actions: %s", actions),
	)
	details = append(details, platform.Details...)

	summary := "Integration health: degraded"
	if platform.ExtensionInstalled && platform.ExtensionEnabled {
		summary = "Integration health: extension active"
	} else if platform.ExtensionInstalled {
		summary = "Integration health: extension installed, not enabled"
	}

	if serverHealthy && platform.ExtensionInstalled && platform.ExtensionEnabled {
		summary = "Integration health: healthy"
	}

	snapshot.Summary = summary
	snapshot.Details = details
	return snapshot
}

func formatHealthDetails(details []string) string {
	if len(details) == 0 {
		return ""
	}
	return "- " + strings.Join(details, "\n- ")
}
