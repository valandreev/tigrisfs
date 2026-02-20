//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func probePlatformIntegrationHealth(guiBinary string) platformIntegrationHealth {
	_ = guiBinary
	health := platformIntegrationHealth{Supported: true}

	appsDir, err := finderAppsDir()
	if err != nil {
		health.Details = append(health.Details, "Finder apps directory unavailable: "+err.Error())
		return health
	}

	hostPath := filepath.Join(appsDir, finderHostAppName)
	appexPath := filepath.Join(hostPath, filepath.FromSlash(finderSyncAppexSubdir))

	if pathExists(hostPath) && pathExists(appexPath) {
		health.ExtensionInstalled = true
		health.ExtensionVersion = plistString(filepath.Join(hostPath, "Contents", "Info.plist"), "CFBundleShortVersionString")
		enabled, state := finderSyncEnabledState()
		health.ExtensionEnabled = enabled
		health.Details = append(health.Details, "Finder Sync state: "+state)
	} else {
		health.Details = append(health.Details, "Finder Sync host not found at "+hostPath)
	}

	actions := []string{
		filepath.Join(appsDir, finderPinAppName),
		filepath.Join(appsDir, finderUnpinAppName),
		filepath.Join(appsDir, finderUnmountAppName),
	}
	health.ActionInstalled = true
	for _, p := range actions {
		if !pathExists(p) {
			health.ActionInstalled = false
			health.Details = append(health.Details, "Missing Finder action app: "+filepath.Base(p))
		}
	}

	if health.ExtensionInstalled {
		health.Details = append(health.Details, "Finder Sync host: "+hostPath)
	}
	return health
}

func enablePlatformIntegration() error {
	out, err := exec.Command("pluginkit", "-e", "use", "-i", finderSyncBundleID).CombinedOutput()
	if err != nil {
		return fmt.Errorf("enable Finder Sync: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func restartFileManagerProcess() error {
	out, err := exec.Command("killall", "Finder").CombinedOutput()
	if err != nil {
		msg := strings.ToLower(strings.TrimSpace(string(out)))
		if !strings.Contains(msg, "no matching") {
			return fmt.Errorf("restart Finder: %v (%s)", err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func finderSyncEnabledState() (bool, string) {
	out, err := exec.Command("pluginkit", "-m", "-i", finderSyncBundleID).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if strings.Contains(strings.ToLower(text), "no plugin") || strings.Contains(strings.ToLower(text), "not found") {
			return false, "not registered"
		}
		return false, "probe failed"
	}

	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, finderSyncBundleID) {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "+") {
			return true, "enabled"
		}
		if strings.HasPrefix(trimmed, "-") {
			return false, "disabled"
		}
	}

	if strings.Contains(strings.ToLower(text), "enabled") {
		return true, "enabled"
	}
	return false, "not enabled"
}

func plistString(plistPath, key string) string {
	if !pathExists(plistPath) {
		return ""
	}
	out, err := exec.Command("/usr/libexec/PlistBuddy", "-c", "Print:"+key, plistPath).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
