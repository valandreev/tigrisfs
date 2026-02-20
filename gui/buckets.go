package main

import (
	"path/filepath"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func removeString(items []string, target string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item != target {
			out = append(out, item)
		}
	}
	return out
}

func filterBucketList(buckets []string, query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return buckets
	}
	filtered := make([]string, 0, len(buckets))
	for _, b := range buckets {
		if strings.Contains(strings.ToLower(b), query) {
			filtered = append(filtered, b)
		}
	}
	return filtered
}

// bucketsTab builds the Buckets tab with per-profile mount/unmount controls.
func bucketsTab(
	win fyne.Window,
	conf *AppConfig,
	mgr *MountManager,
	onMountsChanged func(),
) (*fyne.Container, func([]string), func()) {
	_ = win

	profileLabel := widget.NewLabel("")
	mountRootLabel := widget.NewLabel("")
	statusLabel := widget.NewLabel("")

	filterEntry := widget.NewEntry()
	filterEntry.SetPlaceHolder("Filter buckets...")
	unifiedModeCheck := widget.NewCheck("Unified namespace mount (one mountpoint, buckets as subfolders)", nil)
	var updatingUnifiedToggle bool

	bucketsBox := container.NewVBox()
	scrollBuckets := container.NewVScroll(bucketsBox)
	scrollBuckets.SetMinSize(fyne.NewSize(0, 300))

	bucketCache := make(map[string][]string)

	visibleBuckets := func(profileName string) []string {
		buckets := append([]string(nil), bucketCache[profileName]...)
		sort.Strings(buckets)
		return filterBucketList(buckets, filterEntry.Text)
	}

	var refresh func()
	refresh = func() {
		profile := conf.ActiveProfileCopy()
		profileLabel.SetText("Profile: " + profile.Name)
		mountRootLabel.SetText("Mount Root: " + profile.MountRoot)

		if unifiedModeCheck.Checked != profile.UnifiedMount {
			updatingUnifiedToggle = true
			unifiedModeCheck.SetChecked(profile.UnifiedMount)
			updatingUnifiedToggle = false
		}

		unifiedInfo, unifiedMounted := mgr.UnifiedMountByProfile(profile.Name)
		unifiedBuckets := make(map[string]bool)
		if unifiedMounted {
			for _, b := range unifiedInfo.Buckets {
				unifiedBuckets[b] = true
			}
		}

		bucketsBox.RemoveAll()
		buckets := visibleBuckets(profile.Name)
		if len(buckets) == 0 {
			if strings.TrimSpace(filterEntry.Text) != "" {
				bucketsBox.Add(widget.NewLabel("No buckets match this filter."))
			} else {
				bucketsBox.Add(widget.NewLabel("No buckets loaded for this profile. Click Probe/Refresh."))
			}
			bucketsBox.Refresh()
			return
		}

		for _, name := range buckets {
			bucket := name
			mountInfo, mounted := mgr.MountByProfileBucket(profile.Name, bucket)
			managedByUnified := unifiedMounted
			isAutoMount := containsString(profile.AutoMount, bucket)

			stateText := "Not mounted"
			btnText := "Mount"
			if managedByUnified {
				if unifiedBuckets[bucket] {
					stateText = "Mounted in unified namespace at " + filepath.Join(unifiedInfo.MountPoint, bucket)
				} else {
					stateText = "Unified namespace is active for this profile (bucket not included in current namespace mount)"
				}
				btnText = "Managed by Unified"
			} else if mounted {
				stateText = "Mounted at " + mountInfo.MountPoint
				btnText = "Unmount"
			}

			stateLabel := widget.NewLabel(stateText)
			actionBtn := widget.NewButton(btnText, nil)
			autoMountCheck := widget.NewCheck("Auto-mount", func(checked bool) {
				active := conf.ActiveProfileRef()
				if active == nil || active.Name != profile.Name {
					return
				}
				if checked {
					if !containsString(active.AutoMount, bucket) {
						active.AutoMount = append(active.AutoMount, bucket)
					}
				} else {
					active.AutoMount = removeString(active.AutoMount, bucket)
				}
				active.AutoMount = normalizeAutoMount(active.AutoMount)
				if err := saveConfig(conf); err != nil {
					statusLabel.SetText("Error saving auto-mount: " + err.Error())
				}
			})
			autoMountCheck.SetChecked(isAutoMount)

			if managedByUnified {
				actionBtn.Disable()
			} else {
				actionBtn.OnTapped = func() {
					actionBtn.Disable()
					if mounted {
						statusLabel.SetText("Unmounting " + bucket + "...")
						go func(profileName, b string) {
							err := mgr.Unmount(profileName, b)
							fyne.Do(func() {
								if err != nil {
									statusLabel.SetText("Error: " + err.Error())
								} else {
									statusLabel.SetText("Unmounted " + b)
									if onMountsChanged != nil {
										onMountsChanged()
									}
								}
								refresh()
							})
						}(profile.Name, bucket)
						return
					}

					statusLabel.SetText("Mounting " + bucket + "...")
					go func(p ConnectionProfile, b string) {
						err := mgr.Mount(p, b)
						fyne.Do(func() {
							if err != nil {
								statusLabel.SetText("Error: " + err.Error())
							} else {
								statusLabel.SetText("Mounted " + b)
								if onMountsChanged != nil {
									onMountsChanged()
								}
							}
							refresh()
						})
					}(profile, bucket)
				}
			}

			row := widget.NewCard(
				bucket,
				stateText,
				container.NewBorder(
					nil,
					nil,
					autoMountCheck,
					actionBtn,
					stateLabel,
				),
			)
			bucketsBox.Add(row)
		}
		bucketsBox.Refresh()
	}

	setBuckets := func(buckets []string) {
		profile := conf.ActiveProfileCopy()
		cp := append([]string(nil), buckets...)
		sort.Strings(cp)
		bucketCache[profile.Name] = cp
		refresh()
	}

	refreshBucketsBtn := widget.NewButton("Probe/Refresh Buckets", func() {
		profile := conf.ActiveProfileCopy()
		statusLabel.SetText("Listing buckets for " + profile.Name + "...")
		go func(p ConnectionProfile) {
			buckets, err := listBuckets(p.Endpoint, p.AccessKey, p.SecretKey, p.SkipSSL)
			fyne.Do(func() {
				if err != nil {
					statusLabel.SetText("Error: " + err.Error())
					return
				}
				setBuckets(buckets)
				statusLabel.SetText("Loaded " + itoa(len(buckets)) + " bucket(s)")
			})
		}(profile)
	})

	mountVisibleBtn := widget.NewButton("Mount Visible", func() {
		profile := conf.ActiveProfileCopy()
		targets := visibleBuckets(profile.Name)
		if len(targets) == 0 {
			statusLabel.SetText("No buckets to mount")
			return
		}
		if unifiedModeCheck.Checked {
			statusLabel.SetText("Mounting unified namespace...")
			go func(p ConnectionProfile, names []string) {
				err := mgr.MountUnified(p, names)
				fyne.Do(func() {
					if err != nil {
						statusLabel.SetText("Error: " + err.Error())
						return
					}
					statusLabel.SetText("Unified namespace mounted with " + itoa(len(names)) + " bucket(s)")
					if onMountsChanged != nil {
						onMountsChanged()
					}
					refresh()
				})
			}(profile, targets)
			return
		}

		statusLabel.SetText("Mounting visible buckets...")
		go func(p ConnectionProfile, names []string) {
			mountedCount := 0
			failedCount := 0
			for _, b := range names {
				if mgr.IsMounted(p.Name, b) {
					continue
				}
				if err := mgr.Mount(p, b); err != nil {
					failedCount++
					continue
				}
				mountedCount++
			}
			fyne.Do(func() {
				statusLabel.SetText("Mount complete: " + itoa(mountedCount) + " mounted, " + itoa(failedCount) + " failed")
				if onMountsChanged != nil {
					onMountsChanged()
				}
				refresh()
			})
		}(profile, targets)
	})

	unmountProfileBtn := widget.NewButton("Unmount Profile", func() {
		profile := conf.ActiveProfileCopy()
		statusLabel.SetText("Unmounting active profile buckets...")
		go func(profileName string) {
			count := 0
			for _, mount := range mgr.ActiveMounts() {
				if mount.ProfileName != profileName {
					continue
				}
				if err := mgr.Unmount(mount.ProfileName, mount.Bucket); err == nil {
					count++
				}
			}
			fyne.Do(func() {
				statusLabel.SetText("Unmounted " + itoa(count) + " mount(s) for " + profileName)
				if onMountsChanged != nil {
					onMountsChanged()
				}
				refresh()
			})
		}(profile.Name)
	})

	unifiedModeCheck.OnChanged = func(checked bool) {
		if updatingUnifiedToggle {
			return
		}
		p := conf.ActiveProfileRef()
		if p == nil {
			return
		}
		p.UnifiedMount = checked
		if err := saveConfig(conf); err != nil {
			statusLabel.SetText("Error saving unified preference: " + err.Error())
			return
		}
		if checked {
			statusLabel.SetText("Unified namespace mode enabled for profile")
		} else {
			statusLabel.SetText("Per-bucket mount mode enabled for profile")
		}
	}

	filterEntry.OnChanged = func(string) {
		refresh()
	}

	content := container.NewVBox(
		container.NewHBox(profileLabel, layout.NewSpacer(), refreshBucketsBtn),
		mountRootLabel,
		unifiedModeCheck,
		container.NewHBox(filterEntry, mountVisibleBtn, unmountProfileBtn),
		scrollBuckets,
		container.NewHBox(statusLabel),
	)

	refresh()
	return content, setBuckets, refresh
}
