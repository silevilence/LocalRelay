package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

var appVersion = "0.1.2"
var releaseRepo = "silevilence/LocalRelay"

const githubAPI = "https://api.github.com"

type AppInfo struct {
	Version      string `json:"version"`
	ReleaseRepo  string `json:"releaseRepo"`
	InstallScope string `json:"installScope"`
}

type UpdateInfo struct {
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	TagName         string `json:"tagName"`
	Name            string `json:"name"`
	PublishedAt     string `json:"publishedAt"`
	Body            string `json:"body"`
	HTMLURL         string `json:"htmlUrl"`
	AssetName       string `json:"assetName"`
	AssetURL        string `json:"assetUrl"`
	Checksum        string `json:"checksum"`
	InstallScope    string `json:"installScope"`
	UpdateAvailable bool   `json:"updateAvailable"`
}

type UpdateProgress struct {
	Phase      string `json:"phase"`
	Downloaded int64  `json:"downloaded"`
	Total      int64  `json:"total"`
	Percent    int    `json:"percent"`
	Message    string `json:"message"`
}

type githubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	Body        string        `json:"body"`
	HTMLURL     string        `json:"html_url"`
	Prerelease  bool          `json:"prerelease"`
	Draft       bool          `json:"draft"`
	PublishedAt time.Time     `json:"published_at"`
	Assets      []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

func (a *App) AppInfo() AppInfo {
	return AppInfo{
		Version:      appVersion,
		ReleaseRepo:  releaseRepo,
		InstallScope: currentInstallScope(),
	}
}

func (a *App) CheckForUpdate() (UpdateInfo, error) {
	return checkForUpdate(context.Background(), releaseRepo, appVersion, currentInstallScope())
}

func (a *App) InstallUpdate(tagName string) error {
	info, err := checkForUpdate(context.Background(), releaseRepo, appVersion, currentInstallScope())
	if err != nil {
		return err
	}
	if tagName != "" && !strings.EqualFold(info.TagName, tagName) {
		return fmt.Errorf("requested update %s is no longer latest stable release %s", tagName, info.TagName)
	}
	if !info.UpdateAvailable {
		return errors.New("current version is already up to date")
	}

	a.emitUpdateProgress(UpdateProgress{Phase: "downloading", Message: "正在下载安装包…"})
	installer, err := downloadAndVerify(context.Background(), info, a.emitUpdateProgress)
	if err != nil {
		a.logUpdate("update failed: " + err.Error())
		return err
	}

	a.emitUpdateProgress(UpdateProgress{Phase: "installing", Percent: 100, Message: "校验通过，正在启动静默安装…"})
	if err := startInstallerAfterExit(installer); err != nil {
		a.logUpdate("failed to start installer: " + err.Error())
		return err
	}
	a.logUpdate("started update installer: " + installer)
	wailsruntime.Quit(a.ctx)
	return nil
}

func checkForUpdate(ctx context.Context, repo, currentVersion, scope string) (UpdateInfo, error) {
	release, err := fetchLatestRelease(ctx, repo)
	if err != nil {
		return UpdateInfo{}, err
	}
	latestVersion := strings.TrimPrefix(strings.TrimPrefix(release.TagName, "v"), "V")
	asset, err := findInstallerAsset(release.Assets, latestVersion, scope)
	if err != nil {
		return UpdateInfo{}, err
	}
	checksum, err := fetchChecksum(ctx, release.Assets, asset.Name)
	if err != nil {
		return UpdateInfo{}, err
	}
	return UpdateInfo{
		CurrentVersion:  currentVersion,
		LatestVersion:   latestVersion,
		TagName:         release.TagName,
		Name:            release.Name,
		PublishedAt:     release.PublishedAt.Format(time.RFC3339),
		Body:            release.Body,
		HTMLURL:         release.HTMLURL,
		AssetName:       asset.Name,
		AssetURL:        asset.BrowserDownloadURL,
		Checksum:        checksum,
		InstallScope:    scope,
		UpdateAvailable: compareSemver(latestVersion, currentVersion) > 0,
	}, nil
}

func fetchLatestRelease(ctx context.Context, repo string) (githubRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPI+"/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "LocalRelay/"+appVersion)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return githubRelease{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return githubRelease{}, fmt.Errorf("GitHub release check failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return githubRelease{}, err
	}
	if release.Draft || release.Prerelease {
		return githubRelease{}, errors.New("latest release is not a stable release")
	}
	return release, nil
}

func findInstallerAsset(assets []githubAsset, version, scope string) (githubAsset, error) {
	want := fmt.Sprintf("LocalRelay-%s-%s-amd64-installer.exe", version, scope)
	for _, asset := range assets {
		if strings.EqualFold(asset.Name, want) {
			return asset, nil
		}
	}
	return githubAsset{}, fmt.Errorf("release asset not found: %s", want)
}

func fetchChecksum(ctx context.Context, assets []githubAsset, assetName string) (string, error) {
	checksums, err := findAssetByName(assets, "checksums.txt")
	if err != nil {
		return "", err
	}
	body, err := downloadBytes(ctx, checksums.BrowserDownloadURL, 64*1024)
	if err != nil {
		return "", err
	}
	checksum, ok := checksumForAsset(string(body), assetName)
	if !ok {
		return "", fmt.Errorf("checksums.txt has no entry for %s", assetName)
	}
	return checksum, nil
}

func findAssetByName(assets []githubAsset, name string) (githubAsset, error) {
	for _, asset := range assets {
		if strings.EqualFold(asset.Name, name) {
			return asset, nil
		}
	}
	return githubAsset{}, fmt.Errorf("release asset not found: %s", name)
}

func checksumForAsset(text, assetName string) (string, bool) {
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && strings.EqualFold(fields[len(fields)-1], assetName) {
			hash := strings.ToLower(fields[0])
			if len(hash) == 64 {
				return hash, true
			}
		}
	}
	return "", false
}

func downloadAndVerify(ctx context.Context, info UpdateInfo, progress func(UpdateProgress)) (string, error) {
	dir := filepath.Join(os.TempDir(), "LocalRelay", "updates", info.LatestVersion)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, info.AssetName)
	if err := downloadFile(ctx, info.AssetURL, dst, progress); err != nil {
		return "", err
	}
	hash, err := fileSHA256(dst)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(hash, info.Checksum) {
		_ = os.Remove(dst)
		return "", fmt.Errorf("SHA-256 mismatch: got %s, want %s", hash, info.Checksum)
	}
	progress(UpdateProgress{Phase: "verified", Percent: 100, Message: "安装包完整性校验通过。"})
	return dst, nil
}

func downloadFile(ctx context.Context, url, dst string, progress func(UpdateProgress)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "LocalRelay/"+appVersion)
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("installer download failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}

	tmp := dst + ".download"
	file, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer file.Close()

	var downloaded int64
	buf := make([]byte, 128*1024)
	lastEmit := time.Now()
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			written, writeErr := file.Write(buf[:n])
			if writeErr != nil {
				return writeErr
			}
			downloaded += int64(written)
			if time.Since(lastEmit) > 200*time.Millisecond {
				progress(downloadProgress(downloaded, resp.ContentLength))
				lastEmit = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	progress(downloadProgress(downloaded, resp.ContentLength))
	if err := file.Close(); err != nil {
		return err
	}
	_ = os.Remove(dst)
	return os.Rename(tmp, dst)
}

func downloadProgress(downloaded, total int64) UpdateProgress {
	percent := 0
	if total > 0 {
		percent = int(downloaded * 100 / total)
	}
	return UpdateProgress{Phase: "downloading", Downloaded: downloaded, Total: total, Percent: percent, Message: "正在下载安装包…"}
}

func downloadBytes(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "LocalRelay/"+appVersion)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download failed: %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func startInstallerAfterExit(installer string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	pid := os.Getpid()
	logPath := filepath.Join(filepath.Dir(installer), "install.log")
	installDir := filepath.Dir(exe)
	script := fmt.Sprintf(
		`Wait-Process -Id %d; Start-Process -FilePath %s -ArgumentList @('/S', %s) -Wait; Add-Content -LiteralPath %s -Value "installer exited at $(Get-Date -Format o)"; Start-Process -FilePath %s`,
		pid,
		psQuote(installer),
		psQuote("/D="+installDir),
		psQuote(logPath),
		psQuote(exe),
	)
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden", "-Command", script)
	return cmd.Start()
}

func currentInstallScope() string {
	exe, err := os.Executable()
	if err != nil {
		return "user"
	}
	return detectInstallScope(exe, os.Getenv("LOCALAPPDATA"), os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"))
}

func detectInstallScope(exe, localAppData, programFiles, programFilesX86 string) string {
	exe = strings.ToLower(filepath.Clean(exe))
	for _, root := range []string{programFiles, programFilesX86} {
		if root == "" {
			continue
		}
		if isUnder(exe, strings.ToLower(filepath.Clean(root))) {
			return "machine"
		}
	}
	if localAppData != "" && isUnder(exe, strings.ToLower(filepath.Clean(filepath.Join(localAppData, "Programs")))) {
		return "user"
	}
	return "user"
}

func isUnder(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func compareSemver(a, b string) int {
	av := semverParts(a)
	bv := semverParts(b)
	for i := 0; i < 3; i++ {
		if av[i] > bv[i] {
			return 1
		}
		if av[i] < bv[i] {
			return -1
		}
	}
	return 0
}

func semverParts(version string) [3]int {
	version = strings.TrimPrefix(strings.TrimPrefix(version, "v"), "V")
	var out [3]int
	parts := strings.Split(version, ".")
	for i := 0; i < len(parts) && i < 3; i++ {
		for _, ch := range parts[i] {
			if ch < '0' || ch > '9' {
				break
			}
			out[i] = out[i]*10 + int(ch-'0')
		}
	}
	return out
}

func (a *App) emitUpdateProgress(progress UpdateProgress) {
	if a.ctx != nil {
		wailsruntime.EventsEmit(a.ctx, "update-progress", progress)
	}
}

func (a *App) logUpdate(line string) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return
	}
	path := filepath.Join(dir, "LocalRelay", "update.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = fmt.Fprintf(file, "%s %s\n", time.Now().Format(time.RFC3339), line)
}

func psQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
