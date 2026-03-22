//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

var overlayCLSIDs = []string{
	"{7E58D6B9-6F75-4A44-B56E-3E4DA5039661}",
	"{7E7AB25A-4ACF-48A5-88F8-CE8B06C5D95C}",
	"{7A08AC5D-C713-41F0-B0AB-01984B6D547A}",
}

func probePlatformIntegrationHealth(guiBinary string) platformIntegrationHealth {
	health := platformIntegrationHealth{Supported: true}

	installedCount := 0
	for _, clsid := range overlayCLSIDs {
		if regKeyExists(`HKCR\CLSID\` + clsid) {
			installedCount++
		}
	}
	health.ExtensionInstalled = installedCount == len(overlayCLSIDs)
	if !health.ExtensionInstalled {
		health.Details = append(health.Details, fmt.Sprintf("Overlay COM classes found: %d/%d", installedCount, len(overlayCLSIDs)))
	}

	enabledCount := 0
	overlayKeys := []string{
		`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\ShellIconOverlayIdentifiers\0TigrisFSPinnedOverlay`,
		`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\ShellIconOverlayIdentifiers\1TigrisFSSyncingOverlay`,
		`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\ShellIconOverlayIdentifiers\2TigrisFSCachedOverlay`,
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\ShellIconOverlayIdentifiers\0TigrisFSPinnedOverlay`,
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\ShellIconOverlayIdentifiers\1TigrisFSSyncingOverlay`,
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\ShellIconOverlayIdentifiers\2TigrisFSCachedOverlay`,
	}
	for _, key := range overlayKeys {
		if regKeyExists(key) {
			enabledCount++
		}
	}
	health.ExtensionEnabled = enabledCount > 0
	if !health.ExtensionEnabled {
		health.Details = append(health.Details, "Overlay identifier keys not present")
	}

	actionKeys := []string{
		explorerFilePinKey,
		explorerFileUnpinKey,
		explorerFileUnmountKey,
		explorerFolderPinKey,
		explorerFolderUnpinKey,
		explorerFolderUnmountKey,
	}
	health.ActionInstalled = true
	for _, key := range actionKeys {
		if !regKeyExists(key) {
			health.ActionInstalled = false
		}
	}

	if dllPath, ok := findBundledExplorerExtension(guiBinary); ok {
		health.Details = append(health.Details, "Explorer extension DLL: "+dllPath)
		health.ExtensionVersion = fileVersion(dllPath)
	} else {
		health.Details = append(health.Details, "Explorer extension DLL not found near tigrisfs-gui")
	}

	return health
}

func enablePlatformIntegration() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	return registerExplorerShellExtension(exePath)
}

func restartFileManagerProcess() error {
	_, _ = exec.Command("cmd", "/C", "taskkill /F /IM explorer.exe").CombinedOutput()
	out, err := exec.Command("cmd", "/C", "start explorer.exe").CombinedOutput()
	if err != nil {
		return fmt.Errorf("restart explorer: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func regKeyExists(key string) bool {
	out, err := exec.Command("reg", "query", key).CombinedOutput()
	if err != nil {
		_ = out
		return false
	}
	return true
}

func fileVersion(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	cmd := fmt.Sprintf(`(Get-Item -LiteralPath '%s').VersionInfo.FileVersion`, strings.ReplaceAll(path, `'`, `''`))
	out, err := exec.Command("powershell", "-NoProfile", "-Command", cmd).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
