package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/tigrisdata/tigrisfs/core/cfg"
)

func main() {
	opts := parseStartupOptions(os.Args[1:])

	if opts.InstallIntegration {
		exePath, err := os.Executable()
		if err == nil {
			err = installFileManagerIntegration(exePath)
		}
		a := app.New()
		w := a.NewWindow("TigrisFS")
		if err != nil {
			dialog.ShowError(err, w)
		} else {
			dialog.ShowInformation("TigrisFS", fileManagerIntegrationName()+" integration installed", w)
		}
		w.ShowAndRun()
		return
	}

	if opts.UninstallIntegration {
		err := uninstallFileManagerIntegration()
		a := app.New()
		w := a.NewWindow("TigrisFS")
		if err != nil {
			dialog.ShowError(err, w)
		} else {
			dialog.ShowInformation("TigrisFS", fileManagerIntegrationName()+" integration removed", w)
		}
		w.ShowAndRun()
		return
	}

	var pendingIntegration *integrationCommand
	if opts.Action != "" {
		if opts.Action != "show" && opts.Action != "pin" && opts.Action != "unpin" && opts.Action != "unmount" {
			a := app.New()
			w := a.NewWindow("TigrisFS")
			dialog.ShowError(fmt.Errorf("unsupported integration action %q", opts.Action), w)
			w.ShowAndRun()
			return
		}
		cmd := integrationCommand{
			Action:    opts.Action,
			Path:      opts.Path,
			Recursive: opts.Recursive,
		}
		resp, err := sendIntegrationCommand(cmd)
		if err == nil {
			return
		}
		if resp.Message != "" {
			a := app.New()
			w := a.NewWindow("TigrisFS")
			dialog.ShowError(fmt.Errorf("%s failed: %s", cmd.Action, resp.Message), w)
			w.ShowAndRun()
			return
		}
		pendingIntegration = &cmd
	} else {
		// Normal launch: forward to a running instance if present.
		if _, err := sendIntegrationCommand(integrationCommand{Action: "show"}); err == nil {
			return
		}
	}

	// Check FUSE driver availability.
	if err := checkFUSE(); err != nil {
		a := app.New()
		w := a.NewWindow("TigrisFS")
		dialog.ShowError(err, w)
		w.ShowAndRun()
		return
	}

	a := app.NewWithID("com.tigrisdata.tigrisfs")
	a.Settings().SetTheme(tigrisTheme{})
	a.SetIcon(nil) // TODO: embed app icon
	w := a.NewWindow("TigrisFS")
	w.Resize(fyne.NewSize(1040, 760))

	conf := loadConfig()
	mgr := NewMountManager()
	integrationSrv, err := startIntegrationServer(mgr, func() {
		fyne.Do(func() {
			w.Show()
			w.RequestFocus()
		})
	})
	if err != nil {
		guiLog.Warnf("Unable to start local integration server: %v", err)
	}
	defer func() {
		if integrationSrv != nil {
			integrationSrv.Stop()
		}
	}()

	var refreshBuckets func()
	var refreshMounts func()
	var refreshSettings func()
	var refreshLogs func()
	var updateBuckets func([]string)
	headerPrimary := widget.NewLabel("")
	headerSecondary := widget.NewLabel("")
	headerSecurity := widget.NewLabel("")

	updateHeader := func() {
		active := conf.ActiveProfileCopy()
		endpoint := strings.TrimSpace(active.Endpoint)
		if endpoint == "" {
			endpoint = "endpoint not set"
		}
		headerPrimary.SetText("Profile: " + active.Name + "  •  Endpoint: " + endpoint)
		headerSecondary.SetText(fmt.Sprintf("Active mounts: %d", len(mgr.ActiveMounts())))
		headerSecurity.SetText(endpointSecurityText(active.SkipSSL) + "  •  Credentials in keychain")
	}

	notifyProfileChanged := func() {
		if refreshBuckets != nil {
			refreshBuckets()
		}
		if refreshMounts != nil {
			refreshMounts()
		}
		if refreshSettings != nil {
			refreshSettings()
		}
		if refreshLogs != nil {
			refreshLogs()
		}
		updateHeader()
	}

	bucketsContent, updateBuckets, refreshBuckets := bucketsTab(w, conf, mgr, func() {
		if refreshMounts != nil {
			refreshMounts()
		}
		updateHeader()
	})
	mountsContent, refreshMounts := mountsTab(w, conf, mgr, func() {
		if refreshBuckets != nil {
			refreshBuckets()
		}
		if refreshLogs != nil {
			refreshLogs()
		}
		updateHeader()
	})
	settingsContent, refreshSettings := settingsTab(w, conf, notifyProfileChanged)
	logsContent, refreshLogs := logsTab(w, mgr)

	connTab := container.NewTabItem("Connection",
		connectionTab(
			w,
			conf,
			func(buckets []string) {
				updateBuckets(buckets)
				if refreshBuckets != nil {
					refreshBuckets()
				}
			},
			notifyProfileChanged,
		),
	)
	bucketsTab := container.NewTabItem("Buckets", bucketsContent)
	mountsTab := container.NewTabItem("Mounts", mountsContent)
	settingsTab := container.NewTabItem("Settings", settingsContent)
	logsTab := container.NewTabItem("Logs", logsContent)

	tabs := container.NewAppTabs(connTab, bucketsTab, mountsTab, settingsTab, logsTab)
	tabs.SetTabLocation(container.TabLocationTop)

	// Refresh mounts when switching to the Mounts tab.
	tabs.OnSelected = func(tab *container.TabItem) {
		if tab == mountsTab {
			refreshMounts()
		}
		if tab == bucketsTab {
			refreshBuckets()
		}
		if tab == settingsTab {
			refreshSettings()
		}
		if tab == logsTab && refreshLogs != nil {
			refreshLogs()
		}
		updateHeader()
	}

	// Auto-mount active profile buckets configured in auto_mount.
	active := conf.ActiveProfileCopy()
	if len(active.AutoMount) > 0 {
		go func(profile ConnectionProfile) {
			if profile.UnifiedMount {
				_ = mgr.MountUnified(profile, profile.AutoMount)
			} else {
				for _, bucket := range profile.AutoMount {
					_ = mgr.Mount(profile, bucket)
				}
			}
			fyne.Do(func() {
				notifyProfileChanged()
			})
		}(active)
	}

	header := widget.NewCard(
		"TigrisFS Professional",
		"Version "+cfg.Version,
		container.NewVBox(
			headerPrimary,
			container.NewHBox(headerSecondary, layout.NewSpacer(), headerSecurity),
		),
	)
	updateHeader()

	headerTickerStop := make(chan struct{})
	defer close(headerTickerStop)
	go func() {
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				fyne.Do(updateHeader)
			case <-headerTickerStop:
				return
			}
		}
	}()

	w.SetContent(container.NewBorder(header, nil, nil, nil, tabs))

	if pendingIntegration != nil {
		go func(cmd integrationCommand) {
			resp := executeIntegrationCommand(mgr, nil, cmd)
			if !resp.Success {
				fyne.Do(func() {
					dialog.ShowError(fmt.Errorf("%s failed: %s", cmd.Action, resp.Message), w)
				})
				return
			}
			guiLog.Infof("Integration command applied locally: action=%s path=%s recursive=%v", cmd.Action, cmd.Path, cmd.Recursive)
			fyne.Do(func() {
				if refreshMounts != nil {
					refreshMounts()
				}
				updateHeader()
			})
		}(*pendingIntegration)
	}

	// System tray: minimize on close, mounts persist.
	if desk, ok := a.(desktop.App); ok {
		menu := fyne.NewMenu("TigrisFS",
			fyne.NewMenuItem("Show", func() {
				w.Show()
			}),
			fyne.NewMenuItem("Unmount All & Quit", func() {
				mgr.UnmountAll()
				a.Quit()
			}),
		)
		desk.SetSystemTrayMenu(menu)

		w.SetCloseIntercept(func() {
			w.Hide()
		})
	} else {
		// No system tray support — quit gracefully on close.
		w.SetCloseIntercept(func() {
			mgr.UnmountAll()
			a.Quit()
		})
	}

	w.ShowAndRun()

	// After main loop exits, ensure all mounts are cleaned up.
	mgr.UnmountAll()
}
