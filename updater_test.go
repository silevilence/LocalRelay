package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"localrelay/internal/store"
)

func TestLaunchUpdateInstallerQuit(t *testing.T) {
	for _, hideOnClose := range []bool{true, false} {
		for _, startFails := range []bool{false, true} {
			name := "close-exits"
			if hideOnClose {
				name = "close-hides"
			}
			if startFails {
				name += "/launch-fails"
			} else {
				name += "/launch-succeeds"
			}
			t.Run(name, func(t *testing.T) {
				t.Setenv("APPDATA", t.TempDir())
				t.Setenv("XDG_CONFIG_HOME", t.TempDir())
				s, err := store.Open(filepath.Join(t.TempDir(), "localrelay.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				settings, err := s.DesktopSettings()
				if err != nil {
					t.Fatal(err)
				}
				settings.HideOnClose = hideOnClose
				if err := s.SetDesktopSettings(settings); err != nil {
					t.Fatal(err)
				}
				app := &App{store: s}
				startErr := errors.New("installer could not start")
				called := false
				err = app.launchUpdateInstaller("verified-installer.exe", func(path string) error {
					called = true
					if path != "verified-installer.exe" {
						t.Fatalf("installer path = %q", path)
					}
					if app.quitting.Load() {
						t.Fatal("app requested exit before installer launch succeeded")
					}
					if startFails {
						return startErr
					}
					return nil
				})
				if !called {
					t.Fatal("installer was not launched")
				}
				if startFails && !errors.Is(err, startErr) || !startFails && err != nil {
					t.Fatalf("launch error = %v", err)
				}
				if got := app.beforeClose(context.Background()); got != (startFails && hideOnClose) {
					t.Fatalf("close intercepted = %v: successful update must exit instead of hiding to tray", got)
				}
				if app.quitting.Load() != !startFails {
					t.Fatalf("quitting = %v, launch failed = %v", app.quitting.Load(), startFails)
				}
				persisted, err := s.DesktopSettings()
				if err != nil || persisted != settings {
					t.Fatalf("desktop preferences changed: settings=%+v, err=%v", persisted, err)
				}
			})
		}
	}
}

func TestCompareSemver(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"0.1.1", "0.1.0", 1},
		{"v0.1.0", "0.1.0", 0},
		{"0.2.0", "0.10.0", -1},
		{"1.0.0", "0.9.9", 1},
	} {
		if got := compareSemver(tc.a, tc.b); got != tc.want {
			t.Fatalf("compareSemver(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestDetectInstallScope(t *testing.T) {
	if got := detectInstallScope(`C:\Users\me\AppData\Local\Programs\LocalRelay\LocalRelay.exe`, `C:\Users\me\AppData\Local`, `C:\Program Files`, `C:\Program Files (x86)`); got != "user" {
		t.Fatalf("user install scope = %q", got)
	}
	if got := detectInstallScope(`C:\Program Files\LocalRelay\LocalRelay\LocalRelay.exe`, `C:\Users\me\AppData\Local`, `C:\Program Files`, `C:\Program Files (x86)`); got != "machine" {
		t.Fatalf("machine install scope = %q", got)
	}
	if got := detectInstallScope(`C:\tmp\LocalRelay.exe`, `C:\Users\me\AppData\Local`, `C:\Program Files`, `C:\Program Files (x86)`); got != "user" {
		t.Fatalf("portable install scope = %q", got)
	}
}

func TestFindInstallerAsset(t *testing.T) {
	assets := []githubAsset{
		{Name: "LocalRelay-0.1.1-user-amd64-installer.exe"},
		{Name: "LocalRelay-0.1.1-machine-amd64-installer.exe"},
		{Name: "checksums.txt"},
	}
	asset, err := findInstallerAsset(assets, "0.1.1", "machine")
	if err != nil {
		t.Fatal(err)
	}
	if asset.Name != "LocalRelay-0.1.1-machine-amd64-installer.exe" {
		t.Fatalf("asset = %q", asset.Name)
	}
}

func TestChecksumForAsset(t *testing.T) {
	const sum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	text := sum + "  LocalRelay-0.1.1-user-amd64-installer.exe\n"
	got, ok := checksumForAsset(text, "LocalRelay-0.1.1-user-amd64-installer.exe")
	if !ok || got != sum {
		t.Fatalf("checksum = %q, %v", got, ok)
	}
}
