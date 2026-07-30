//go:build !windows

package main

import (
	"errors"

	"localrelay/internal/store"
)

var errLaunchAtLoginUnsupported = errors.New("开机启动目前仅支持 Windows")

// Non-Windows builds retain persisted settings but deliberately avoid platform
// integrations until their dedicated compatibility work is scheduled.
func (a *App) startSystemTray()                     {}
func (a *App) stopSystemTray()                      {}
func (a *App) updateTrayGatewayMenu(bool)           {}
func (a *App) showTrayHiddenHint()                  {}
func (a *App) startWindowStateWatcher()             {}
func syncLaunchAtLogin(store.DesktopSettings) error { return errLaunchAtLoginUnsupported }
