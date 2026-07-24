package main

import "testing"

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
