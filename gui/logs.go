package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/rs/zerolog"
	"github.com/tigrisdata/tigrisfs/log"
)

const (
	maxStoredLogLines   = 6000
	maxRenderedLogLines = 1500
)

func normalizeLevelName(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "trace":
		return "trace"
	case "debug":
		return "debug"
	case "info":
		return "info"
	case "warn":
		return "warn"
	case "error":
		return "error"
	default:
		return "all"
	}
}

func lineMatchesLevel(line, selected string) bool {
	selected = normalizeLevelName(selected)
	if selected == "all" {
		return true
	}
	lower := strings.ToLower(line)

	switch selected {
	case "trace":
		return strings.Contains(lower, " trc ") || strings.Contains(lower, "\"level\":\"trace\"")
	case "debug":
		return strings.Contains(lower, " dbg ") || strings.Contains(lower, "\"level\":\"debug\"")
	case "info":
		return strings.Contains(lower, " inf ") || strings.Contains(lower, "\"level\":\"info\"")
	case "warn":
		return strings.Contains(lower, " wrn ") || strings.Contains(lower, "\"level\":\"warn\"") || strings.Contains(lower, "\"level\":\"warning\"")
	case "error":
		return strings.Contains(lower, " err ") || strings.Contains(lower, "\"level\":\"error\"") || strings.Contains(lower, " panic:")
	default:
		return true
	}
}

func lineMatchesModule(line, selected string) bool {
	selected = strings.TrimSpace(selected)
	if selected == "" || selected == "all" {
		return true
	}
	lower := strings.ToLower(line)
	module := strings.ToLower(selected)
	return strings.Contains(lower, "module="+module) || strings.Contains(lower, "\"module\":\""+module+"\"")
}

func logsTab(win fyne.Window, mgr *MountManager) (*fyne.Container, func()) {
	levelFilter := widget.NewSelect([]string{"all", "trace", "debug", "info", "warn", "error"}, nil)
	levelFilter.SetSelected("all")

	moduleFilter := widget.NewSelect([]string{"all"}, nil)
	moduleFilter.SetSelected("all")

	runtimeLevel := widget.NewSelect([]string{"trace", "debug", "info", "warn", "error"}, nil)
	runtimeLevel.SetSelected("info")

	searchEntry := widget.NewEntry()
	searchEntry.SetPlaceHolder("Filter text")

	pausedCheck := widget.NewCheck("Pause stream", nil)
	statusLabel := widget.NewLabel("")
	healthLabel := widget.NewLabel("")

	logOutput := widget.NewMultiLineEntry()
	logOutput.Disable()
	logOutput.Wrapping = fyne.TextWrapOff
	logScroll := container.NewVScroll(logOutput)
	logScroll.SetMinSize(fyne.NewSize(0, 320))

	var mu sync.Mutex
	logLines := make([]string, 0, 512)
	refreshQueued := false
	paused := false

	refreshModules := func() {
		modules := log.ListLoggers()
		options := make([]string, 0, len(modules)+1)
		options = append(options, "all")
		options = append(options, modules...)
		current := moduleFilter.Selected
		moduleFilter.SetOptions(options)
		if current == "" {
			current = "all"
		}
		found := false
		for _, m := range options {
			if m == current {
				found = true
				break
			}
		}
		if !found {
			current = "all"
		}
		moduleFilter.SetSelected(current)
	}

	render := func() {
		level := levelFilter.Selected
		module := moduleFilter.Selected
		textFilter := strings.ToLower(strings.TrimSpace(searchEntry.Text))

		mounts := mgr.ActiveMounts()
		healthLabel.SetText(fmt.Sprintf("Active mounts: %d", len(mounts)))

		mu.Lock()
		source := append([]string(nil), logLines...)
		refreshQueued = false
		mu.Unlock()

		filtered := make([]string, 0, len(source))
		for _, line := range source {
			if !lineMatchesLevel(line, level) {
				continue
			}
			if !lineMatchesModule(line, module) {
				continue
			}
			if textFilter != "" && !strings.Contains(strings.ToLower(line), textFilter) {
				continue
			}
			filtered = append(filtered, line)
		}

		if len(filtered) > maxRenderedLogLines {
			filtered = filtered[len(filtered)-maxRenderedLogLines:]
		}
		logOutput.SetText(strings.Join(filtered, "\n"))
		statusLabel.SetText(fmt.Sprintf("%d shown / %d buffered", len(filtered), len(source)))
	}

	queueRefresh := func() {
		mu.Lock()
		if refreshQueued {
			mu.Unlock()
			return
		}
		refreshQueued = true
		mu.Unlock()
		fyne.Do(render)
	}

	levelFilter.OnChanged = func(string) { queueRefresh() }
	moduleFilter.OnChanged = func(string) { queueRefresh() }
	searchEntry.OnChanged = func(string) { queueRefresh() }
	pausedCheck.OnChanged = func(v bool) {
		mu.Lock()
		paused = v
		mu.Unlock()
		if !v {
			queueRefresh()
		}
	}

	runtimeLevel.OnChanged = func(level string) {
		lvl, err := zerolog.ParseLevel(level)
		if err != nil {
			statusLabel.SetText("Invalid level: " + level)
			return
		}
		log.SetCloudLogLevel(lvl)
		statusLabel.SetText("Runtime log level set to " + level)
		refreshModules()
		queueRefresh()
	}

	clearBtn := widget.NewButton("Clear", func() {
		mu.Lock()
		logLines = logLines[:0]
		mu.Unlock()
		queueRefresh()
	})

	refreshModulesBtn := widget.NewButton("Refresh Modules", func() {
		refreshModules()
		queueRefresh()
	})

	exportBtn := widget.NewButton("Export", func() {
		dialog.ShowFileSave(func(writer fyne.URIWriteCloser, err error) {
			if err != nil {
				dialog.ShowError(err, win)
				return
			}
			if writer == nil {
				return
			}
			defer writer.Close()

			level := levelFilter.Selected
			module := moduleFilter.Selected
			textFilter := strings.ToLower(strings.TrimSpace(searchEntry.Text))

			mu.Lock()
			source := append([]string(nil), logLines...)
			mu.Unlock()

			filtered := make([]string, 0, len(source))
			for _, line := range source {
				if !lineMatchesLevel(line, level) || !lineMatchesModule(line, module) {
					continue
				}
				if textFilter != "" && !strings.Contains(strings.ToLower(line), textFilter) {
					continue
				}
				filtered = append(filtered, line)
			}
			if _, err := writer.Write([]byte(strings.Join(filtered, "\n"))); err != nil {
				dialog.ShowError(err, win)
				return
			}
			statusLabel.SetText("Exported logs")
		}, win)
	})

	subID, ch := log.SubscribeLines(2048)

	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		defer log.UnsubscribeLines(subID)

		dirty := false
		for {
			select {
			case line, ok := <-ch:
				if !ok {
					return
				}
				mu.Lock()
				logLines = append(logLines, line)
				if len(logLines) > maxStoredLogLines {
					logLines = logLines[len(logLines)-maxStoredLogLines:]
				}
				mu.Unlock()
				dirty = true
			case <-ticker.C:
				mu.Lock()
				isPaused := paused
				mu.Unlock()
				if dirty && !isPaused {
					queueRefresh()
					dirty = false
				}
			}
		}
	}()

	refreshModules()
	queueRefresh()

	content := container.NewVBox(
		container.NewHBox(
			widget.NewLabel("Filter Level"),
			levelFilter,
			widget.NewLabel("Filter Module"),
			moduleFilter,
			refreshModulesBtn,
			layout.NewSpacer(),
			widget.NewLabel("Runtime Level"),
			runtimeLevel,
		),
		container.NewHBox(
			searchEntry,
			pausedCheck,
			clearBtn,
			exportBtn,
		),
		container.NewHBox(healthLabel, layout.NewSpacer(), statusLabel),
		widget.NewSeparator(),
		logScroll,
	)

	return content, queueRefresh
}
