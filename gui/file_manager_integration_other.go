//go:build !windows && !darwin

package main

import "fmt"

func fileManagerIntegrationName() string {
	return "File Manager"
}

func fileManagerIntegrationDescription() string {
	return "File manager integration is currently available on macOS Finder and Windows Explorer builds."
}

func installFileManagerIntegration(guiBinary string) error {
	_ = guiBinary
	return fmt.Errorf("file manager integration is not supported on this OS")
}

func uninstallFileManagerIntegration() error {
	return fmt.Errorf("file manager integration is not supported on this OS")
}
