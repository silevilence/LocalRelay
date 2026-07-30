//go:build windows

package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"os"
	goruntime "runtime"
	"time"

	"git.sr.ht/~jackmordaunt/go-toast/v2"
	"github.com/energye/systray"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows/registry"

	"localrelay/internal/store"
)

const windowsRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const windowsRunValue = "LocalRelay"

//go:embed build/windows/icon.ico
var trayIcon []byte

func (a *App) startSystemTray() {
	a.trayOnce.Do(func() {
		a.trayMu.Lock()
		a.trayStop = systray.Quit
		a.trayMu.Unlock()
		go func() {
			// Windows routes the tray window, click messages, and popup menu
			// through one native thread. Keep the complete systray loop pinned
			// there instead of RunWithExternalLoop's unpinned worker goroutine.
			goruntime.LockOSThread()
			defer goruntime.UnlockOSThread()
			systray.Run(func() {
				systray.SetIcon(trayIcon)
				systray.SetTooltip("LocalRelay 本地模型网关")
				systray.SetOnClick(func(_ systray.IMenu) { a.ShowMainWindow() })
				systray.SetOnDClick(func(_ systray.IMenu) { a.ShowMainWindow() })
				systray.SetOnRClick(showTrayMenu)
				showItem := systray.AddMenuItem("显示主窗口", "显示 LocalRelay 主窗口")
				showItem.Click(a.ShowMainWindow)
				enabled, err := a.RelayServiceEnabled()
				if err != nil {
					enabled = true
				}
				gatewayItem := systray.AddMenuItem(gatewayMenuTitle(enabled), "暂停或恢复网关服务")
				gatewayItem.Click(func() {
					current, err := a.RelayServiceEnabled()
					if err != nil {
						a.emitGatewayError(fmt.Errorf("read gateway service state: %w", err))
						return
					}
					if _, err := a.SetRelayServiceEnabled(!current); err != nil {
						a.emitGatewayError(fmt.Errorf("toggle gateway service from tray: %w", err))
					}
				})
				a.trayMu.Lock()
				a.trayGatewayItem = gatewayItem
				a.trayMu.Unlock()
				systray.AddSeparator()
				quitItem := systray.AddMenuItem("退出", "退出 LocalRelay")
				quitItem.Click(a.RequestQuit)
			}, nil)
		}()
	})
}

func showTrayMenu(menu systray.IMenu) {
	if err := menu.ShowMenu(); err != nil {
		log.Printf("show tray context menu: %v", err)
	}
}

func (a *App) stopSystemTray() {
	a.trayMu.Lock()
	stop := a.trayStop
	a.trayMu.Unlock()
	if stop != nil {
		stop()
	}
}

func (a *App) updateTrayGatewayMenu(enabled bool) {
	a.trayMu.Lock()
	item := a.trayGatewayItem
	a.trayMu.Unlock()
	if item != nil {
		item.SetTitle(gatewayMenuTitle(enabled))
	}
}

func gatewayMenuTitle(enabled bool) string {
	if enabled {
		return "暂停网关服务"
	}
	return "恢复网关服务"
}

func (a *App) showTrayHiddenHint() {
	go func() {
		notification := toast.Notification{
			AppID: "LocalRelay",
			Title: "LocalRelay",
			Body:  "已隐藏到系统托盘，可从托盘图标右键退出",
		}
		_ = notification.Push()
	}()
}

func (a *App) startWindowStateWatcher() {
	if a.ctx == nil {
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.watchCancel = cancel
	go func() {
		ticker := time.NewTicker(350 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				settings, err := a.store.DesktopSettings()
				if err == nil && settings.HideOnMinimize && runtime.WindowIsMinimised(a.ctx) {
					a.hideToTray(true)
				}
			}
		}
	}()
}

func syncLaunchAtLogin(settings store.DesktopSettings) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, windowsRunKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if !settings.LaunchAtLogin {
		if err := key.DeleteValue(windowsRunValue); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	return key.SetStringValue(windowsRunValue, autostartCommand(executable))
}
