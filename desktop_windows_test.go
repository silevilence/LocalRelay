//go:build windows

package main

import (
	"errors"
	"testing"
)

type testTrayMenu struct {
	called bool
	err    error
}

func (m *testTrayMenu) ShowMenu() error {
	m.called = true
	return m.err
}

func TestShowTrayMenuInvokesNativeMenu(t *testing.T) {
	menu := &testTrayMenu{}
	showTrayMenu(menu)
	if !menu.called {
		t.Fatal("right-click handler did not invoke ShowMenu")
	}
	menu = &testTrayMenu{err: errors.New("not ready")}
	showTrayMenu(menu)
	if !menu.called {
		t.Fatal("right-click handler did not attempt ShowMenu after an error")
	}
}

func TestGatewayMenuTitle(t *testing.T) {
	if got := gatewayMenuTitle(true); got != "暂停网关服务" {
		t.Fatalf("enabled gateway menu title = %q", got)
	}
	if got := gatewayMenuTitle(false); got != "恢复网关服务" {
		t.Fatalf("disabled gateway menu title = %q", got)
	}
}
