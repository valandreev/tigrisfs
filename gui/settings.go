package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

func parseIntInRange(label, value string, min, max int) (int, error) {
	v, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s must be a number", label)
	}
	if v < min || (max > 0 && v > max) {
		if max > 0 {
			return 0, fmt.Errorf("%s must be between %d and %d", label, min, max)
		}
		return 0, fmt.Errorf("%s must be at least %d", label, min)
	}
	return v, nil
}

// settingsTab builds profile-specific mount/cache settings.
func settingsTab(
	win fyne.Window,
	conf *AppConfig,
	onProfileChanged func(),
) (*fyne.Container, func()) {
	profileLabel := widget.NewLabel("")

	mountRootEntry := widget.NewEntry()
	mountBrowseBtn := widget.NewButton("Browse...", func() {
		d := dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil || uri == nil {
				return
			}
			mountRootEntry.SetText(uri.Path())
		}, win)
		d.Show()
	})

	memoryEntry := widget.NewEntry()

	cacheDirEntry := widget.NewEntry()
	cacheDirEntry.SetPlaceHolder("Leave empty for no disk cache")
	cacheBrowseBtn := widget.NewButton("Browse...", func() {
		d := dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil || uri == nil {
				return
			}
			cacheDirEntry.SetText(uri.Path())
		}, win)
		d.Show()
	})

	cacheSizeEntry := widget.NewEntry()
	writebackCheck := widget.NewCheck("Enable writeback (faster writes, data cached locally before upload)", nil)
	unifiedMountCheck := widget.NewCheck("Prefer unified namespace mount (single mountpoint with buckets as folders)", nil)

	entryLimitEntry := widget.NewEntry()
	maxFlushersEntry := widget.NewEntry()
	readAheadKBEntry := widget.NewEntry()
	statTTLSecondsEntry := widget.NewEntry()
	httpTimeoutSecondsEntry := widget.NewEntry()
	retryIntervalSecondsEntry := widget.NewEntry()
	cheapCheck := widget.NewCheck("Cost-optimized mode (fewer backend ops, slightly lower performance)", nil)
	noPreloadDirCheck := widget.NewCheck("Disable directory preload on file open", nil)
	explicitDirCheck := widget.NewCheck("Assume directories exist (no implicit dir checks)", nil)
	ignoreFsyncCheck := widget.NewCheck("Ignore fsync calls", nil)
	fsyncOnCloseCheck := widget.NewCheck("Sync file on close", nil)
	disableXattrCheck := widget.NewCheck("Disable xattr support", nil)

	statusLabel := widget.NewLabel("")
	integrationStatusLabel := widget.NewLabel("")
	integrationHealthSummaryLabel := widget.NewLabel("Integration health: unknown")
	integrationHealthDetailsLabel := widget.NewLabel("")
	integrationHealthDetailsLabel.Wrapping = fyne.TextWrapWord

	var installIntegrationBtn *widget.Button
	var removeIntegrationBtn *widget.Button
	var checkHealthBtn *widget.Button
	var enableExtensionBtn *widget.Button
	var restartManagerBtn *widget.Button

	runIntegrationOp := func(action string, fn func(string) error) {
		exePath, err := os.Executable()
		if err != nil {
			integrationStatusLabel.SetText("Integration error: " + err.Error())
			return
		}
		installIntegrationBtn.Disable()
		removeIntegrationBtn.Disable()
		if checkHealthBtn != nil {
			checkHealthBtn.Disable()
		}
		if enableExtensionBtn != nil {
			enableExtensionBtn.Disable()
		}
		if restartManagerBtn != nil {
			restartManagerBtn.Disable()
		}
		integrationStatusLabel.SetText(action + "...")
		go func(bin string) {
			err := fn(bin)
			fyne.Do(func() {
				installIntegrationBtn.Enable()
				removeIntegrationBtn.Enable()
				if checkHealthBtn != nil {
					checkHealthBtn.Enable()
				}
				if enableExtensionBtn != nil {
					enableExtensionBtn.Enable()
				}
				if restartManagerBtn != nil {
					restartManagerBtn.Enable()
				}
				if err != nil {
					integrationStatusLabel.SetText("Integration error: " + err.Error())
					return
				}
				integrationStatusLabel.SetText(action + " complete")
			})
		}(exePath)
	}

	var refreshIntegrationHealth func()

	loadFromActive := func() {
		p := conf.ActiveProfileCopy()
		profileLabel.SetText("Profile: " + p.Name)
		mountRootEntry.SetText(p.MountRoot)
		memoryEntry.SetText(strconv.Itoa(p.MemoryMB))
		cacheDirEntry.SetText(p.CachePath)
		cacheSizeEntry.SetText(strconv.Itoa(p.CacheSize))
		writebackCheck.SetChecked(p.Writeback)
		unifiedMountCheck.SetChecked(p.UnifiedMount)

		entryLimitEntry.SetText(strconv.Itoa(p.EntryLimit))
		maxFlushersEntry.SetText(strconv.Itoa(p.MaxFlushers))
		readAheadKBEntry.SetText(strconv.Itoa(p.ReadAheadKB))
		statTTLSecondsEntry.SetText(strconv.Itoa(p.StatCacheTTLSeconds))
		httpTimeoutSecondsEntry.SetText(strconv.Itoa(p.HTTPTimeoutSeconds))
		retryIntervalSecondsEntry.SetText(strconv.Itoa(p.RetryIntervalSec))
		cheapCheck.SetChecked(p.Cheap)
		noPreloadDirCheck.SetChecked(p.NoPreloadDir)
		explicitDirCheck.SetChecked(p.ExplicitDir)
		ignoreFsyncCheck.SetChecked(p.IgnoreFsync)
		fsyncOnCloseCheck.SetChecked(p.FsyncOnClose)
		disableXattrCheck.SetChecked(p.DisableXattr)
		if refreshIntegrationHealth != nil {
			refreshIntegrationHealth()
		}
	}

	refreshIntegrationHealth = func() {
		exePath, err := os.Executable()
		if err != nil {
			integrationHealthSummaryLabel.SetText("Integration health: unavailable")
			integrationHealthDetailsLabel.SetText("- " + err.Error())
			return
		}

		integrationHealthSummaryLabel.SetText("Integration health: checking...")
		integrationHealthDetailsLabel.SetText("")

		go func(bin string) {
			snapshot := gatherIntegrationHealth(bin)
			fyne.Do(func() {
				integrationHealthSummaryLabel.SetText(snapshot.Summary)
				integrationHealthDetailsLabel.SetText(formatHealthDetails(snapshot.Details))
			})
		}(exePath)
	}

	resetAdvancedDefaults := func() {
		defaults := defaultProfile("defaults")
		entryLimitEntry.SetText(strconv.Itoa(defaults.EntryLimit))
		maxFlushersEntry.SetText(strconv.Itoa(defaults.MaxFlushers))
		readAheadKBEntry.SetText(strconv.Itoa(defaults.ReadAheadKB))
		statTTLSecondsEntry.SetText(strconv.Itoa(defaults.StatCacheTTLSeconds))
		httpTimeoutSecondsEntry.SetText(strconv.Itoa(defaults.HTTPTimeoutSeconds))
		retryIntervalSecondsEntry.SetText(strconv.Itoa(defaults.RetryIntervalSec))
		cheapCheck.SetChecked(defaults.Cheap)
		noPreloadDirCheck.SetChecked(defaults.NoPreloadDir)
		explicitDirCheck.SetChecked(defaults.ExplicitDir)
		ignoreFsyncCheck.SetChecked(defaults.IgnoreFsync)
		fsyncOnCloseCheck.SetChecked(defaults.FsyncOnClose)
		disableXattrCheck.SetChecked(defaults.DisableXattr)
		statusLabel.SetText("Advanced settings reset to defaults")
	}

	saveBtn := widget.NewButton("Save Settings", func() {
		p := conf.ActiveProfileRef()
		if p == nil {
			dialog.ShowError(fmt.Errorf("no active profile"), win)
			return
		}

		mb, err := parseIntInRange("memory limit", memoryEntry.Text, 100, 131072)
		if err != nil {
			dialog.ShowError(err, win)
			return
		}
		cs, err := parseIntInRange("cache size", cacheSizeEntry.Text, 1, 262144)
		if err != nil {
			dialog.ShowError(err, win)
			return
		}
		entryLimit, err := parseIntInRange("entry limit", entryLimitEntry.Text, 1000, 50000000)
		if err != nil {
			dialog.ShowError(err, win)
			return
		}
		maxFlushers, err := parseIntInRange("max flushers", maxFlushersEntry.Text, 1, 512)
		if err != nil {
			dialog.ShowError(err, win)
			return
		}
		readAheadKB, err := parseIntInRange("read-ahead KB", readAheadKBEntry.Text, 64, 2097152)
		if err != nil {
			dialog.ShowError(err, win)
			return
		}
		statTTL, err := parseIntInRange("stat cache TTL", statTTLSecondsEntry.Text, 1, 3600)
		if err != nil {
			dialog.ShowError(err, win)
			return
		}
		httpTimeout, err := parseIntInRange("HTTP timeout", httpTimeoutSecondsEntry.Text, 5, 3600)
		if err != nil {
			dialog.ShowError(err, win)
			return
		}
		retryInterval, err := parseIntInRange("retry interval", retryIntervalSecondsEntry.Text, 1, 3600)
		if err != nil {
			dialog.ShowError(err, win)
			return
		}

		p.MountRoot = strings.TrimSpace(mountRootEntry.Text)
		p.MemoryMB = mb
		p.CachePath = strings.TrimSpace(cacheDirEntry.Text)
		p.CacheSize = cs
		p.Writeback = writebackCheck.Checked
		p.UnifiedMount = unifiedMountCheck.Checked

		p.EntryLimit = entryLimit
		p.MaxFlushers = maxFlushers
		p.ReadAheadKB = readAheadKB
		p.StatCacheTTLSeconds = statTTL
		p.HTTPTimeoutSeconds = httpTimeout
		p.RetryIntervalSec = retryInterval
		p.Cheap = cheapCheck.Checked
		p.NoPreloadDir = noPreloadDirCheck.Checked
		p.ExplicitDir = explicitDirCheck.Checked
		p.IgnoreFsync = ignoreFsyncCheck.Checked
		p.FsyncOnClose = fsyncOnCloseCheck.Checked
		p.DisableXattr = disableXattrCheck.Checked
		p.applyDefaults(p.Name)

		if err := saveConfig(conf); err != nil {
			dialog.ShowError(err, win)
			return
		}
		statusLabel.SetText("Settings saved (applies to new mounts)")
		if onProfileChanged != nil {
			onProfileChanged()
		}
	})

	resetAdvancedBtn := widget.NewButton("Reset Advanced Defaults", resetAdvancedDefaults)

	installIntegrationBtn = widget.NewButton("Install "+fileManagerIntegrationName()+" Integration", func() {
		runIntegrationOp("Installing "+fileManagerIntegrationName()+" integration", func(bin string) error {
			if err := installFileManagerIntegration(bin); err != nil {
				return err
			}
			refreshIntegrationHealth()
			return nil
		})
	})

	removeIntegrationBtn = widget.NewButton("Remove "+fileManagerIntegrationName()+" Integration", func() {
		runIntegrationOp("Removing "+fileManagerIntegrationName()+" integration", func(_ string) error {
			if err := uninstallFileManagerIntegration(); err != nil {
				return err
			}
			refreshIntegrationHealth()
			return nil
		})
	})

	checkHealthBtn = widget.NewButton("Check Health", func() {
		refreshIntegrationHealth()
	})

	enableExtensionBtn = widget.NewButton("Enable Extension", func() {
		integrationStatusLabel.SetText("Enabling " + fileManagerIntegrationName() + " extension...")
		go func() {
			err := enablePlatformIntegration()
			fyne.Do(func() {
				if err != nil {
					integrationStatusLabel.SetText("Integration error: " + err.Error())
					return
				}
				integrationStatusLabel.SetText("Enabled " + fileManagerIntegrationName() + " extension")
				refreshIntegrationHealth()
			})
		}()
	})

	restartManagerBtn = widget.NewButton("Restart "+fileManagerIntegrationName(), func() {
		integrationStatusLabel.SetText("Restarting " + fileManagerIntegrationName() + "...")
		go func() {
			err := restartFileManagerProcess()
			fyne.Do(func() {
				if err != nil {
					integrationStatusLabel.SetText("Integration error: " + err.Error())
					return
				}
				integrationStatusLabel.SetText(fileManagerIntegrationName() + " restarted")
				refreshIntegrationHealth()
			})
		}()
	})

	generalForm := container.NewVBox(
		widget.NewLabel("Mount Root"),
		container.NewBorder(nil, nil, nil, mountBrowseBtn, mountRootEntry),
		widget.NewLabel("Memory Limit (MB)"),
		memoryEntry,
		widget.NewLabel("Cache Directory"),
		container.NewBorder(nil, nil, nil, cacheBrowseBtn, cacheDirEntry),
		widget.NewLabel("Cache Size (GB)"),
		cacheSizeEntry,
		writebackCheck,
		unifiedMountCheck,
	)

	advancedForm := container.NewVBox(
		widget.NewLabel("Entry Limit (metadata entries)"),
		entryLimitEntry,
		widget.NewLabel("Max Flushers"),
		maxFlushersEntry,
		widget.NewLabel("Read Ahead (KB)"),
		readAheadKBEntry,
		widget.NewLabel("Stat Cache TTL (seconds)"),
		statTTLSecondsEntry,
		widget.NewLabel("HTTP Timeout (seconds)"),
		httpTimeoutSecondsEntry,
		widget.NewLabel("Retry Interval (seconds)"),
		retryIntervalSecondsEntry,
		cheapCheck,
		noPreloadDirCheck,
		explicitDirCheck,
		ignoreFsyncCheck,
		fsyncOnCloseCheck,
		disableXattrCheck,
	)

	generalCard := widget.NewCard("Mount & Cache", "Core profile settings", generalForm)
	advancedCard := widget.NewCard("Advanced Performance", "Tune caching, retries, and consistency behavior", advancedForm)
	integrationCard := widget.NewCard(
		fileManagerIntegrationName()+" Integration",
		fileManagerIntegrationDescription(),
		container.NewVBox(
			container.NewHBox(installIntegrationBtn, removeIntegrationBtn),
			container.NewHBox(checkHealthBtn, enableExtensionBtn, restartManagerBtn),
			integrationStatusLabel,
			widget.NewSeparator(),
			integrationHealthSummaryLabel,
			integrationHealthDetailsLabel,
		),
	)

	form := container.NewVBox(
		profileLabel,
		generalCard,
		advancedCard,
		integrationCard,
		container.NewHBox(saveBtn, resetAdvancedBtn, layout.NewSpacer(), statusLabel),
	)

	loadFromActive()

	content := container.NewVBox(
		container.NewVScroll(form),
	)
	return content, loadFromActive
}
