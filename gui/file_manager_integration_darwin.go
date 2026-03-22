//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	finderPinAppName      = "TigrisFS Pin.app"
	finderUnpinAppName    = "TigrisFS Unpin.app"
	finderUnmountAppName  = "TigrisFS Unmount.app"
	finderHostAppName     = "TigrisFS Finder Host.app"
	finderSyncBundleID    = "com.tigrisdata.tigrisfs.findersync"
	finderSyncAppexSubdir = "Contents/PlugIns/TigrisFSFinderSync.appex"
)

func fileManagerIntegrationName() string {
	return "Finder"
}

func fileManagerIntegrationDescription() string {
	return "Installs Finder Sync overlays plus Finder actions for pin, unpin, and unmount."
}

func finderAppsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "Applications")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func escapeAppleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

func compileFinderActionApp(appPath, guiBinary, action string) error {
	bin := escapeAppleScriptString(guiBinary)
	lines := []string{
		"on open inputItems",
		"	repeat with anItem in inputItems",
		"		set posixPath to POSIX path of anItem",
		fmt.Sprintf(`		set cmd to quoted form of "%s" & " --integration-action %s --integration-path " & quoted form of posixPath`, bin, action),
		"		try",
		`			do shell script "test -d " & quoted form of posixPath`,
		`			set cmd to cmd & " --integration-recursive"`,
		"		end try",
		"		do shell script cmd",
		"	end repeat",
		"end open",
	}

	args := []string{"-o", appPath}
	for _, line := range lines {
		args = append(args, "-e", line)
	}
	out, err := exec.Command("osacompile", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("osacompile %s: %v (%s)", appPath, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func installFileManagerIntegration(guiBinary string) error {
	if err := installBundledFinderSync(guiBinary); err != nil {
		guiLog.Warnf("Finder Sync install warning: %v", err)
	}

	dir, err := finderAppsDir()
	if err != nil {
		return err
	}
	pinApp := filepath.Join(dir, finderPinAppName)
	unpinApp := filepath.Join(dir, finderUnpinAppName)
	unmountApp := filepath.Join(dir, finderUnmountAppName)

	if err := compileFinderActionApp(pinApp, guiBinary, "pin"); err != nil {
		return err
	}
	if err := compileFinderActionApp(unpinApp, guiBinary, "unpin"); err != nil {
		return err
	}
	if err := compileFinderActionApp(unmountApp, guiBinary, "unmount"); err != nil {
		return err
	}
	return nil
}

func uninstallFileManagerIntegration() error {
	if err := uninstallBundledFinderSync(); err != nil {
		guiLog.Warnf("Finder Sync uninstall warning: %v", err)
	}

	dir, err := finderAppsDir()
	if err != nil {
		return err
	}
	paths := []string{
		filepath.Join(dir, finderPinAppName),
		filepath.Join(dir, finderUnpinAppName),
		filepath.Join(dir, finderUnmountAppName),
	}
	for _, p := range paths {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	return nil
}

func installBundledFinderSync(guiBinary string) error {
	src, ok := findBundledFinderHost(guiBinary)
	if !ok {
		return nil
	}
	appsDir, err := finderAppsDir()
	if err != nil {
		return err
	}

	dst := filepath.Join(appsDir, finderHostAppName)
	_ = os.RemoveAll(dst)
	if err := copyDir(src, dst); err != nil {
		return err
	}

	appex := filepath.Join(dst, filepath.FromSlash(finderSyncAppexSubdir))
	if _, err := os.Stat(appex); err != nil {
		return fmt.Errorf("finder sync extension not found in host bundle: %w", err)
	}

	if out, err := exec.Command("pluginkit", "-a", appex).CombinedOutput(); err != nil {
		return fmt.Errorf("pluginkit add failed: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("pluginkit", "-e", "use", "-i", finderSyncBundleID).CombinedOutput(); err != nil {
		return fmt.Errorf("pluginkit enable failed: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func uninstallBundledFinderSync() error {
	if out, err := exec.Command("pluginkit", "-e", "ignore", "-i", finderSyncBundleID).CombinedOutput(); err != nil {
		msg := strings.ToLower(strings.TrimSpace(string(out)))
		if !strings.Contains(msg, "no plugin") && !strings.Contains(msg, "not found") {
			return fmt.Errorf("pluginkit disable failed: %v (%s)", err, strings.TrimSpace(string(out)))
		}
	}
	appsDir, err := finderAppsDir()
	if err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Join(appsDir, finderHostAppName))
	return nil
}

func findBundledFinderHost(guiBinary string) (string, bool) {
	base := filepath.Dir(guiBinary)
	candidates := []string{
		filepath.Join(base, finderHostAppName),
		filepath.Join(base, "finder-sync", finderHostAppName),
		filepath.Join(base, "extensions", "macos", finderHostAppName),
		filepath.Join(base, "..", "Resources", finderHostAppName),
		filepath.Join(base, "..", "Resources", "finder-sync", finderHostAppName),
	}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}

	if _, err := out.ReadFrom(in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
