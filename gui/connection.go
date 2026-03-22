package main

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

func endpointSecurityText(skipSSL bool) string {
	if skipSSL {
		return "TLS verification: OFF (insecure)"
	}
	return "TLS verification: ON"
}

// connectionTab builds the profile and endpoint configuration UI.
func connectionTab(
	win fyne.Window,
	conf *AppConfig,
	onConnected func(buckets []string),
	onProfileChanged func(),
) fyne.CanvasObject {
	profileSelect := widget.NewSelect(nil, nil)

	endpointEntry := widget.NewEntry()
	endpointEntry.SetPlaceHolder("https://s3.example.com")

	accessKeyEntry := widget.NewEntry()
	accessKeyEntry.SetPlaceHolder("Access Key ID")

	secretKeyEntry := widget.NewPasswordEntry()
	secretKeyEntry.SetPlaceHolder("Secret Access Key")

	skipSSLCheck := widget.NewCheck("Skip SSL verification (insecure)", nil)
	statusLabel := widget.NewLabel("")
	tlsStatusLabel := widget.NewLabel("")
	credentialsStatusLabel := widget.NewLabel("")

	var updatingProfileSelect bool

	updateSecurityStatus := func() {
		tlsStatusLabel.SetText(endpointSecurityText(skipSSLCheck.Checked))
		if strings.TrimSpace(accessKeyEntry.Text) == "" && strings.TrimSpace(secretKeyEntry.Text) == "" {
			credentialsStatusLabel.SetText("Credentials: not configured")
		} else {
			credentialsStatusLabel.SetText("Credentials: stored in OS keychain (not in config.json)")
		}
	}

	loadActiveProfileIntoFields := func() {
		p := conf.ActiveProfileCopy()
		endpointEntry.SetText(p.Endpoint)
		accessKeyEntry.SetText(p.AccessKey)
		secretKeyEntry.SetText(p.SecretKey)
		skipSSLCheck.SetChecked(p.SkipSSL)
		updateSecurityStatus()
	}

	refreshProfileSelect := func(selectName string) {
		options := conf.ProfileNames()
		if len(options) == 0 {
			selectName = defaultProfileName
			conf.UpsertProfile(defaultProfile(defaultProfileName))
			conf.SetActiveProfile(defaultProfileName)
			options = conf.ProfileNames()
		}
		if selectName == "" {
			selectName = conf.ActiveProfile
		}
		updatingProfileSelect = true
		profileSelect.SetOptions(options)
		profileSelect.SetSelected(selectName)
		updatingProfileSelect = false
	}

	profileFromFields := func(name string) ConnectionProfile {
		name = normalizeProfileName(name)
		p := conf.ActiveProfileCopy()
		p.Name = name
		p.Endpoint = strings.TrimSpace(endpointEntry.Text)
		p.AccessKey = strings.TrimSpace(accessKeyEntry.Text)
		p.SecretKey = strings.TrimSpace(secretKeyEntry.Text)
		p.SkipSSL = skipSSLCheck.Checked
		p.applyDefaults(name)
		return p
	}

	saveCurrentProfile := func() {
		name := profileSelect.Selected
		if name == "" {
			name = conf.ActiveProfile
		}
		p := profileFromFields(name)
		if strings.TrimSpace(p.Endpoint) == "" {
			dialog.ShowError(fmt.Errorf("endpoint is required"), win)
			return
		}
		if err := saveProfileCredentials(p.Name, p.AccessKey, p.SecretKey); err != nil {
			dialog.ShowError(fmt.Errorf("save credentials: %w", err), win)
			return
		}
		conf.UpsertProfile(p)
		conf.SetActiveProfile(p.Name)
		if err := saveConfig(conf); err != nil {
			dialog.ShowError(err, win)
			return
		}
		refreshProfileSelect(p.Name)
		loadActiveProfileIntoFields()
		statusLabel.SetText("Profile saved: " + p.Name)
		if onProfileChanged != nil {
			onProfileChanged()
		}
	}

	newProfileBtn := widget.NewButton("New Profile", func() {
		dialog.ShowEntryDialog("Create Profile", "Profile Name", func(name string) {
			if strings.TrimSpace(name) == "" {
				dialog.ShowError(fmt.Errorf("profile name is required"), win)
				return
			}
			name = normalizeProfileName(name)
			if conf.profileIndexByName(name) != -1 {
				dialog.ShowError(fmt.Errorf("profile %q already exists", name), win)
				return
			}
			p := profileFromFields(name)
			if err := saveProfileCredentials(p.Name, p.AccessKey, p.SecretKey); err != nil {
				dialog.ShowError(fmt.Errorf("save credentials: %w", err), win)
				return
			}
			conf.UpsertProfile(p)
			conf.SetActiveProfile(name)
			if err := saveConfig(conf); err != nil {
				dialog.ShowError(err, win)
				return
			}
			refreshProfileSelect(name)
			loadActiveProfileIntoFields()
			statusLabel.SetText("Created profile: " + name)
			if onProfileChanged != nil {
				onProfileChanged()
			}
		}, win)
	})

	deleteProfileBtn := widget.NewButton("Delete Profile", func() {
		name := profileSelect.Selected
		if name == "" {
			name = conf.ActiveProfile
		}
		if len(conf.Profiles) <= 1 {
			dialog.ShowError(fmt.Errorf("at least one profile must remain"), win)
			return
		}
		dialog.ShowConfirm("Delete Profile", "Delete profile "+name+"?", func(ok bool) {
			if !ok {
				return
			}
			if !conf.DeleteProfile(name) {
				dialog.ShowError(fmt.Errorf("failed to delete profile %q", name), win)
				return
			}
			if err := deleteProfileCredentials(name); err != nil {
				dialog.ShowError(fmt.Errorf("delete credentials: %w", err), win)
				return
			}
			if err := saveConfig(conf); err != nil {
				dialog.ShowError(err, win)
				return
			}
			refreshProfileSelect(conf.ActiveProfile)
			loadActiveProfileIntoFields()
			statusLabel.SetText("Deleted profile: " + name)
			if onProfileChanged != nil {
				onProfileChanged()
			}
		}, win)
	})

	profileSelect.OnChanged = func(name string) {
		if updatingProfileSelect || name == "" {
			return
		}
		if !conf.SetActiveProfile(name) {
			return
		}
		_ = saveConfig(conf)
		loadActiveProfileIntoFields()
		statusLabel.SetText("Active profile: " + name)
		if onProfileChanged != nil {
			onProfileChanged()
		}
	}

	skipSSLCheck.OnChanged = func(bool) {
		updateSecurityStatus()
	}
	accessKeyEntry.OnChanged = func(string) {
		updateSecurityStatus()
	}
	secretKeyEntry.OnChanged = func(string) {
		updateSecurityStatus()
	}

	probeBtn := widget.NewButton("Probe S3 Buckets", func() {
		p := profileFromFields(profileSelect.Selected)
		if strings.TrimSpace(p.Endpoint) == "" {
			statusLabel.SetText("Endpoint is required")
			return
		}
		statusLabel.SetText("Probing S3 endpoint...")
		go func(profile ConnectionProfile) {
			buckets, err := listBuckets(profile.Endpoint, profile.AccessKey, profile.SecretKey, profile.SkipSSL)
			fyne.Do(func() {
				if err != nil {
					statusLabel.SetText("Failed: " + err.Error())
					return
				}
				statusLabel.SetText("Connected — found " + itoa(len(buckets)) + " bucket(s)")
				if onConnected != nil {
					onConnected(buckets)
				}
			})
		}(p)
	})

	clearCredsBtn := widget.NewButton("Clear Stored Credentials", func() {
		name := profileSelect.Selected
		if name == "" {
			name = conf.ActiveProfile
		}
		dialog.ShowConfirm("Clear Credentials", "Remove stored credentials for profile "+name+"?", func(ok bool) {
			if !ok {
				return
			}
			if err := deleteProfileCredentials(name); err != nil {
				dialog.ShowError(fmt.Errorf("clear credentials: %w", err), win)
				return
			}
			if p := conf.ActiveProfileRef(); p != nil && p.Name == name {
				p.AccessKey = ""
				p.SecretKey = ""
			}
			if err := saveConfig(conf); err != nil {
				dialog.ShowError(err, win)
				return
			}
			accessKeyEntry.SetText("")
			secretKeyEntry.SetText("")
			updateSecurityStatus()
			statusLabel.SetText("Credentials cleared for profile: " + name)
			if onProfileChanged != nil {
				onProfileChanged()
			}
		}, win)
	})

	saveBtn := widget.NewButton("Save Profile", func() {
		saveCurrentProfile()
	})

	refreshProfileSelect(conf.ActiveProfile)
	loadActiveProfileIntoFields()

	profileCard := widget.NewCard(
		"Profiles",
		"Create and switch connection profiles",
		container.NewVBox(
			container.NewBorder(nil, nil, nil, container.NewHBox(newProfileBtn, deleteProfileBtn), profileSelect),
		),
	)

	connectionCard := widget.NewCard(
		"Connection",
		"S3-compatible endpoint and credentials",
		container.NewVBox(
			widget.NewLabel("Endpoint"),
			endpointEntry,
			widget.NewLabel("Access Key"),
			accessKeyEntry,
			widget.NewLabel("Secret Key"),
			secretKeyEntry,
			skipSSLCheck,
		),
	)

	securityCard := widget.NewCard(
		"Security",
		"Credentials are keychain-backed and not persisted in config.json",
		container.NewVBox(
			tlsStatusLabel,
			credentialsStatusLabel,
		),
	)

	actions := container.NewHBox(
		probeBtn,
		saveBtn,
		clearCredsBtn,
		layout.NewSpacer(),
		statusLabel,
	)

	content := container.NewVBox(
		profileCard,
		connectionCard,
		securityCard,
		actions,
	)

	return container.NewVScroll(content)
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}
