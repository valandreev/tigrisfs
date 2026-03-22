//go:build !windows && !darwin

package main

import "fmt"

func probePlatformIntegrationHealth(guiBinary string) platformIntegrationHealth {
	_ = guiBinary
	return platformIntegrationHealth{
		Supported: false,
		Details:   []string{"native Finder/Explorer extension checks are not available on this OS"},
	}
}

func enablePlatformIntegration() error {
	return fmt.Errorf("extension enable is not supported on this OS")
}

func restartFileManagerProcess() error {
	return fmt.Errorf("file manager restart is not supported on this OS")
}
