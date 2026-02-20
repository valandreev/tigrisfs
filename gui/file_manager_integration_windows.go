//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	explorerFilePinKey       = `HKCU\Software\Classes\*\shell\TigrisFS.Pin`
	explorerFileUnpinKey     = `HKCU\Software\Classes\*\shell\TigrisFS.Unpin`
	explorerFileUnmountKey   = `HKCU\Software\Classes\*\shell\TigrisFS.Unmount`
	explorerFolderPinKey     = `HKCU\Software\Classes\Directory\shell\TigrisFS.Pin`
	explorerFolderUnpinKey   = `HKCU\Software\Classes\Directory\shell\TigrisFS.Unpin`
	explorerFolderUnmountKey = `HKCU\Software\Classes\Directory\shell\TigrisFS.Unmount`
	explorerExtDLLName       = "TigrisFS.ExplorerExtension.dll"
)

func fileManagerIntegrationName() string {
	return "Windows Explorer"
}

func fileManagerIntegrationDescription() string {
	return "Installs Explorer shell overlays plus right-click actions for pin, unpin, and unmount."
}

func regAdd(key, valueName, value string) error {
	args := []string{"add", key, "/f"}
	if valueName == "" {
		args = append(args, "/ve")
	} else {
		args = append(args, "/v", valueName)
	}
	args = append(args, "/d", value)
	out, err := exec.Command("reg", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("reg add %s: %v (%s)", key, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func regDelete(key string) error {
	out, err := exec.Command("reg", "delete", key, "/f").CombinedOutput()
	if err != nil {
		msg := strings.ToLower(strings.TrimSpace(string(out)))
		if strings.Contains(msg, "unable to find") || strings.Contains(msg, "cannot find") {
			return nil
		}
		return fmt.Errorf("reg delete %s: %v (%s)", key, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func installFileManagerIntegration(guiBinary string) error {
	if err := registerExplorerShellExtension(guiBinary); err != nil {
		guiLog.Warnf("Explorer overlay registration warning: %v", err)
	}

	filePinCmd := fmt.Sprintf(`"%s" --integration-action pin --integration-path "%%1"`, guiBinary)
	fileUnpinCmd := fmt.Sprintf(`"%s" --integration-action unpin --integration-path "%%1"`, guiBinary)
	fileUnmountCmd := fmt.Sprintf(`"%s" --integration-action unmount --integration-path "%%1"`, guiBinary)
	folderPinCmd := fmt.Sprintf(`"%s" --integration-action pin --integration-path "%%1" --integration-recursive`, guiBinary)
	folderUnpinCmd := fmt.Sprintf(`"%s" --integration-action unpin --integration-path "%%1" --integration-recursive`, guiBinary)
	folderUnmountCmd := fmt.Sprintf(`"%s" --integration-action unmount --integration-path "%%1"`, guiBinary)

	entries := []struct {
		key     string
		label   string
		command string
	}{
		{explorerFilePinKey, "TigrisFS: Pin in Cache", filePinCmd},
		{explorerFileUnpinKey, "TigrisFS: Unpin from Cache", fileUnpinCmd},
		{explorerFileUnmountKey, "TigrisFS: Unmount Mount", fileUnmountCmd},
		{explorerFolderPinKey, "TigrisFS: Pin Folder in Cache", folderPinCmd},
		{explorerFolderUnpinKey, "TigrisFS: Unpin Folder from Cache", folderUnpinCmd},
		{explorerFolderUnmountKey, "TigrisFS: Unmount Mount", folderUnmountCmd},
	}

	for _, entry := range entries {
		if err := regAdd(entry.key, "", entry.label); err != nil {
			return err
		}
		if err := regAdd(entry.key, "Icon", guiBinary); err != nil {
			return err
		}
		if err := regAdd(entry.key+`\command`, "", entry.command); err != nil {
			return err
		}
	}
	return nil
}

func uninstallFileManagerIntegration() error {
	if err := unregisterExplorerShellExtension(); err != nil {
		guiLog.Warnf("Explorer overlay unregister warning: %v", err)
	}

	keys := []string{
		explorerFilePinKey + `\command`,
		explorerFilePinKey,
		explorerFileUnpinKey + `\command`,
		explorerFileUnpinKey,
		explorerFileUnmountKey + `\command`,
		explorerFileUnmountKey,
		explorerFolderPinKey + `\command`,
		explorerFolderPinKey,
		explorerFolderUnpinKey + `\command`,
		explorerFolderUnpinKey,
		explorerFolderUnmountKey + `\command`,
		explorerFolderUnmountKey,
	}
	for _, key := range keys {
		if err := regDelete(key); err != nil {
			return err
		}
	}
	return nil
}

func registerExplorerShellExtension(guiBinary string) error {
	dllPath, ok := findBundledExplorerExtension(guiBinary)
	if !ok {
		return nil
	}
	return runRegAsm([]string{"/nologo", "/codebase", dllPath})
}

func unregisterExplorerShellExtension() error {
	if regasm, err := findRegAsm(); err == nil {
		candidates := likelyExplorerExtensionDLLs()
		if exe, exeErr := os.Executable(); exeErr == nil {
			candidates = append(candidates, likelyExplorerExtensionDLLsFromBinary(exe)...)
		}
		for _, dll := range candidates {
			if _, statErr := os.Stat(dll); statErr == nil {
				out, runErr := exec.Command(regasm, "/nologo", "/u", dll).CombinedOutput()
				if runErr != nil {
					return fmt.Errorf("regasm /u %s: %v (%s)", dll, runErr, strings.TrimSpace(string(out)))
				}
			}
		}
	}
	return nil
}

func findBundledExplorerExtension(guiBinary string) (string, bool) {
	for _, candidate := range likelyExplorerExtensionDLLsFromBinary(guiBinary) {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true
		}
	}
	return "", false
}

func likelyExplorerExtensionDLLs() []string {
	home, _ := os.UserHomeDir()
	return []string{
		filepath.Join(home, "TigrisFS", "extensions", "windows", explorerExtDLLName),
	}
}

func likelyExplorerExtensionDLLsFromBinary(guiBinary string) []string {
	base := filepath.Dir(guiBinary)
	candidates := []string{
		filepath.Join(base, explorerExtDLLName),
		filepath.Join(base, "extensions", "windows", explorerExtDLLName),
		filepath.Join(base, "explorer-extension", explorerExtDLLName),
		filepath.Join(base, "..", "Resources", "windows", explorerExtDLLName),
	}
	return append(candidates, likelyExplorerExtensionDLLs()...)
}

func runRegAsm(args []string) error {
	regasm, err := findRegAsm()
	if err != nil {
		return err
	}
	out, err := exec.Command(regasm, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("regasm %v: %v (%s)", args, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func findRegAsm() (string, error) {
	winDir := os.Getenv("WINDIR")
	if winDir == "" {
		winDir = `C:\Windows`
	}
	candidates := []string{
		filepath.Join(winDir, "Microsoft.NET", "Framework64", "v4.0.30319", "RegAsm.exe"),
		filepath.Join(winDir, "Microsoft.NET", "Framework", "v4.0.30319", "RegAsm.exe"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("RegAsm.exe not found")
}
