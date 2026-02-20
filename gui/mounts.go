package main

import (
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

type mountMetrics struct {
	Info          MountInfo
	CacheUsed     uint64
	CacheErr      error
	CacheFS       fsUsage
	DownloadBps   float64
	UploadBps     float64
	TotalDownload uint64
	TotalUpload   uint64
}

type transferSample struct {
	ReadBytes  int64
	WriteBytes int64
	At         time.Time
}

type cacheTelemetry struct {
	CacheUsed uint64
	CacheErr  error
	CacheFS   fsUsage
	UpdatedAt time.Time
}

// mountsTab builds the Mounts tab with active mount telemetry.
func mountsTab(
	win fyne.Window,
	conf *AppConfig,
	mgr *MountManager,
	onMountsChanged func(),
) (*fyne.Container, func()) {
	_ = win

	activeProfileLabel := widget.NewLabel("")
	mountRootUsageLabel := widget.NewLabel("")
	mountRootUsageBar := widget.NewProgressBar()
	cacheRootUsageLabel := widget.NewLabel("")
	cacheRootUsageBar := widget.NewProgressBar()
	transferSummaryLabel := widget.NewLabel("")
	statusLabel := widget.NewLabel("")
	pinPathEntry := widget.NewEntry()
	pinPathEntry.SetPlaceHolder("Absolute mounted path to pin/unpin")
	pinRecursiveCheck := widget.NewCheck("Recursive", nil)
	pinRecursiveCheck.SetChecked(true)

	mountList := container.NewVBox()
	scrollMounts := container.NewVScroll(mountList)
	scrollMounts.SetMinSize(fyne.NewSize(0, 260))
	transferByMount := make(map[string]transferSample)
	cacheByMount := make(map[string]cacheTelemetry)
	const cacheTelemetryTTL = 30 * time.Second

	refreshing := false
	var refreshMu sync.Mutex
	tryStartRefresh := func() bool {
		refreshMu.Lock()
		defer refreshMu.Unlock()
		if refreshing {
			return false
		}
		refreshing = true
		return true
	}
	endRefresh := func() {
		refreshMu.Lock()
		refreshing = false
		refreshMu.Unlock()
	}

	var refresh func()
	refreshWithMode := func(quiet bool) {
		if !tryStartRefresh() {
			return
		}
		if !quiet {
			statusLabel.SetText("Refreshing mount telemetry...")
		}

		profile := conf.ActiveProfileCopy()
		mounts := mgr.ActiveMounts()

		go func(p ConnectionProfile, mountsSnapshot []MountInfo) {
			now := time.Now()
			rootUsage := getFSUsage(p.MountRoot)
			cacheRootUsage := fsUsage{}
			if p.CachePath != "" {
				cacheRootUsage = getFSUsage(p.CachePath)
				cacheRootUsage.Path = p.CachePath
			}

			metrics := make([]mountMetrics, 0, len(mountsSnapshot))
			totalDownloadBps := float64(0)
			totalUploadBps := float64(0)
			activeKeys := make(map[string]bool, len(mountsSnapshot))
			for _, info := range mountsSnapshot {
				m := mountMetrics{
					Info: info,
				}
				activeKeys[info.Key] = true

				if info.fs != nil {
					snapshot := info.fs.TransferStatsSnapshot()
					if snapshot.ReadBytes > 0 {
						m.TotalDownload = uint64(snapshot.ReadBytes)
					}
					if snapshot.WriteBytes > 0 {
						m.TotalUpload = uint64(snapshot.WriteBytes)
					}
					prev := transferByMount[info.Key]
					if !prev.At.IsZero() {
						deltaSeconds := now.Sub(prev.At).Seconds()
						if deltaSeconds > 0 {
							deltaDown := float64(snapshot.ReadBytes-prev.ReadBytes) / deltaSeconds
							deltaUp := float64(snapshot.WriteBytes-prev.WriteBytes) / deltaSeconds
							if deltaDown > 0 {
								m.DownloadBps = deltaDown
							}
							if deltaUp > 0 {
								m.UploadBps = deltaUp
							}
						}
					}
					transferByMount[info.Key] = transferSample{
						ReadBytes:  snapshot.ReadBytes,
						WriteBytes: snapshot.WriteBytes,
						At:         now,
					}
				}

				if info.CachePath != "" {
					cached := cacheByMount[info.Key]
					if cached.UpdatedAt.IsZero() || now.Sub(cached.UpdatedAt) > cacheTelemetryTTL {
						cached.CacheUsed, cached.CacheErr = getDirSize(info.CachePath)
						cached.CacheFS = getFSUsage(info.CachePath)
						cached.UpdatedAt = now
						cacheByMount[info.Key] = cached
					}
					m.CacheUsed = cached.CacheUsed
					m.CacheErr = cached.CacheErr
					m.CacheFS = cached.CacheFS
				}
				totalDownloadBps += m.DownloadBps
				totalUploadBps += m.UploadBps
				metrics = append(metrics, m)
			}
			for key := range transferByMount {
				if !activeKeys[key] {
					delete(transferByMount, key)
				}
			}
			for key := range cacheByMount {
				if !activeKeys[key] {
					delete(cacheByMount, key)
				}
			}

			fyne.Do(func() {
				activeProfileLabel.SetText("Active Profile: " + p.Name + " (" + itoa(len(mountsSnapshot)) + " mount(s))")
				mountRootUsageLabel.SetText("Mount Root FS (" + p.MountRoot + "): " + formatFSUsage(rootUsage))
				mountRootUsageBar.SetValue(fsUsageFraction(rootUsage))
				if p.CachePath == "" {
					cacheRootUsageLabel.SetText("Cache Root FS: disabled")
					cacheRootUsageBar.SetValue(0)
				} else {
					cacheRootUsageLabel.SetText("Cache Root FS (" + p.CachePath + "): " + formatFSUsage(cacheRootUsage))
					cacheRootUsageBar.SetValue(fsUsageFraction(cacheRootUsage))
				}
				transferSummaryLabel.SetText("Current Transfer: ↓ " + formatBytesRate(totalDownloadBps) + "  ↑ " + formatBytesRate(totalUploadBps))

				mountList.RemoveAll()
				if len(metrics) == 0 {
					mountList.Add(widget.NewLabel("No active mounts"))
				} else {
					for _, metric := range metrics {
						m := metric
						cacheLine := "Cache: disabled"
						if m.Info.CachePath != "" {
							if m.CacheErr != nil {
								cacheLine = "Cache: " + m.Info.CachePath + " (read error)"
							} else {
								cacheLine = "Cache: " + formatBytes(m.CacheUsed) + " at " + m.Info.CachePath
							}
						}

						cacheFSLine := "Cache FS: N/A"
						if m.Info.CachePath != "" {
							cacheFSLine = "Cache FS: " + formatFSUsage(m.CacheFS)
						}

						titleText := m.Info.ProfileName + " / " + m.Info.Bucket
						bucketLine := ""
						unmountLabel := "Unmount"
						if m.Info.Mode == mountModeUnified {
							titleText = m.Info.ProfileName + " / Unified Namespace"
							unmountLabel = "Unmount Namespace"
							if len(m.Info.Buckets) > 0 {
								bucketLine = "Buckets: " + strings.Join(m.Info.Buckets, ", ")
							}
						}

						title := widget.NewLabelWithStyle(
							titleText,
							fyne.TextAlignLeading,
							fyne.TextStyle{Bold: true},
						)
						detailsItems := []fyne.CanvasObject{
							title,
							widget.NewLabel("Mount: " + m.Info.MountPoint),
							widget.NewLabel("Endpoint: " + m.Info.Endpoint),
							widget.NewLabel("Transfer: ↓ " + formatBytesRate(m.DownloadBps) + "  ↑ " + formatBytesRate(m.UploadBps) + "  • Total ↓ " + formatBytes(m.TotalDownload) + "  ↑ " + formatBytes(m.TotalUpload)),
							widget.NewLabel(cacheLine),
							widget.NewLabel(cacheFSLine),
							widget.NewLabel("Status: " + m.Info.Status + " • Uptime: " + time.Since(m.Info.MountedAt).Round(time.Second).String()),
						}
						if bucketLine != "" {
							detailsItems = append(detailsItems, widget.NewLabel(bucketLine))
						}
						details := container.NewVBox(detailsItems...)

						unmountBtn := widget.NewButton(unmountLabel, func() {
							statusLabel.SetText("Unmounting " + m.Info.Bucket + "...")
							go func(profileName, bucket string) {
								err := mgr.Unmount(profileName, bucket)
								fyne.Do(func() {
									if err != nil {
										statusLabel.SetText("Error: " + err.Error())
									} else {
										statusLabel.SetText("Unmounted " + bucket)
										if onMountsChanged != nil {
											onMountsChanged()
										}
									}
									refresh()
								})
							}(m.Info.ProfileName, m.Info.Bucket)
						})

						row := container.NewBorder(nil, nil, nil, unmountBtn, details)
						mountList.Add(row)
						mountList.Add(widget.NewSeparator())
					}
				}

				mountList.Refresh()
				if !quiet {
					statusLabel.SetText("Refreshed at " + time.Now().Format("15:04:05"))
				}
				endRefresh()
			})
		}(profile, mounts)
	}
	refresh = func() {
		refreshWithMode(false)
	}

	unmountAllBtn := widget.NewButton("Unmount All", func() {
		statusLabel.SetText("Unmounting all mounts...")
		go func() {
			mgr.UnmountAll()
			fyne.Do(func() {
				statusLabel.SetText("All mounts unmounted")
				if onMountsChanged != nil {
					onMountsChanged()
				}
				refresh()
			})
		}()
	})

	refreshBtn := widget.NewButton("Refresh", func() {
		refresh()
	})

	pinBtn := widget.NewButton("Pin Path", func() {
		path := pinPathEntry.Text
		recursive := pinRecursiveCheck.Checked
		statusLabel.SetText("Pinning path...")
		go func() {
			result, err := mgr.PinAbsolutePath(path, recursive)
			fyne.Do(func() {
				if err != nil {
					statusLabel.SetText("Pin error: " + err.Error())
					return
				}
				statusLabel.SetText("Pinned: " + itoa(result.Files) + " file(s), " + itoa(result.Dirs) + " dir(s), " + formatBytes(result.BytesRead) + " loaded")
				refresh()
			})
		}()
	})

	unpinBtn := widget.NewButton("Unpin Path", func() {
		path := pinPathEntry.Text
		recursive := pinRecursiveCheck.Checked
		statusLabel.SetText("Unpinning path...")
		go func() {
			result, err := mgr.UnpinAbsolutePath(path, recursive)
			fyne.Do(func() {
				if err != nil {
					statusLabel.SetText("Unpin error: " + err.Error())
					return
				}
				statusLabel.SetText("Unpinned: " + itoa(result.Files) + " file(s), " + itoa(result.Dirs) + " dir(s), " + itoa(result.UnpinnedBuffer) + " buffer(s)")
				refresh()
			})
		}()
	})

	pinControls := container.NewBorder(
		nil,
		nil,
		pinRecursiveCheck,
		container.NewHBox(pinBtn, unpinBtn),
		pinPathEntry,
	)

	content := container.NewVBox(
		container.NewHBox(refreshBtn, unmountAllBtn, layout.NewSpacer(), statusLabel),
		activeProfileLabel,
		mountRootUsageLabel,
		mountRootUsageBar,
		cacheRootUsageLabel,
		cacheRootUsageBar,
		transferSummaryLabel,
		pinControls,
		widget.NewSeparator(),
		scrollMounts,
	)

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			refreshWithMode(true)
		}
	}()

	refresh()
	return content, refresh
}
