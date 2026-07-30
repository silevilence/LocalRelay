package main

import (
	"context"
	"fmt"

	"localrelay/internal/store"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// DesktopSettings exposes the persisted application settings to the frontend.
func (a *App) DesktopSettings() (store.DesktopSettings, error) {
	return a.store.DesktopSettings()
}

func (a *App) SetHideOnMinimize(enabled bool) (store.DesktopSettings, error) {
	return a.updateDesktopSettings(func(settings *store.DesktopSettings) {
		settings.HideOnMinimize = enabled
	}, false)
}

func (a *App) SetHideOnClose(enabled bool) (store.DesktopSettings, error) {
	return a.updateDesktopSettings(func(settings *store.DesktopSettings) {
		settings.HideOnClose = enabled
	}, false)
}

func (a *App) SetLaunchAtLogin(enabled bool) (store.DesktopSettings, error) {
	return a.updateDesktopSettings(func(settings *store.DesktopSettings) {
		settings.LaunchAtLogin = enabled
	}, true)
}

func (a *App) SetStartMinimized(enabled bool) (store.DesktopSettings, error) {
	return a.updateDesktopSettings(func(settings *store.DesktopSettings) {
		settings.StartMinimized = enabled
	}, false)
}

func (a *App) updateDesktopSettings(update func(*store.DesktopSettings), updateAutostart bool) (store.DesktopSettings, error) {
	settings, err := a.store.DesktopSettings()
	if err != nil {
		return store.DesktopSettings{}, err
	}
	previous := settings
	update(&settings)
	if updateAutostart {
		if err := syncLaunchAtLogin(settings); err != nil {
			return store.DesktopSettings{}, err
		}
	}
	if err := a.store.SetDesktopSettings(settings); err != nil {
		if updateAutostart {
			_ = syncLaunchAtLogin(previous)
		}
		return store.DesktopSettings{}, err
	}
	return settings, nil
}

// beforeClose preserves a running application in the notification area when
// configured. The tray's explicit Exit item calls RequestQuit and bypasses it.
func (a *App) beforeClose(ctx context.Context) bool {
	settings, err := a.store.DesktopSettings()
	if err != nil || !shouldInterceptClose(settings, a.quitting.Load()) {
		return false
	}
	a.hideToTray(true)
	return true
}

// HideToTray is intentionally exported for window-state integration. The hint
// is persisted before it is shown so it remains a true one-time notification.
func (a *App) HideToTray(showHint bool) {
	a.hideToTray(showHint)
}

func (a *App) hideToTray(showHint bool) {
	if a.ctx == nil {
		return
	}
	runtime.WindowHide(a.ctx)
	if !showHint {
		return
	}
	settings, err := a.store.DesktopSettings()
	if err != nil || settings.TrayHintShown {
		return
	}
	settings.TrayHintShown = true
	if err := a.store.SetDesktopSettings(settings); err == nil {
		a.showTrayHiddenHint()
	}
}

// ShowMainWindow makes the window visible from the tray menu or icon action.
func (a *App) ShowMainWindow() {
	if a.ctx != nil {
		runtime.WindowShow(a.ctx)
	}
}

// RequestQuit is only used by the explicit tray command; normal window close
// remains subject to the user's hide-on-close preference.
func (a *App) RequestQuit() {
	a.quitting.Store(true)
	if a.ctx != nil {
		runtime.Quit(a.ctx)
	}
}

func shouldInterceptClose(settings store.DesktopSettings, explicitQuit bool) bool {
	return settings.HideOnClose && !explicitQuit
}

// initialStartHidden reads the persisted preference before Wails creates its
// window. That keeps both packaged runs and `wails dev` from briefly showing a
// window when start-minimized is enabled.
func initialStartHidden() bool {
	db, err := store.Open(defaultDBPath())
	if err != nil {
		return false
	}
	defer db.Close()
	settings, err := db.DesktopSettings()
	return err == nil && settings.StartMinimized
}

func shouldStartMinimized(settings store.DesktopSettings) bool {
	return settings.StartMinimized
}

func autostartCommand(executable string) string {
	return fmt.Sprintf("\"%s\"", executable)
}
