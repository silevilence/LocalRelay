package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"localrelay/internal/githubproxy"
	"localrelay/internal/store"
)

type GitHubProxyState struct {
	Settings  store.GitHubProxySettings `json:"settings"`
	Running   bool                      `json:"running"`
	Error     string                    `json:"error"`
	Addresses []LocalAddress            `json:"addresses"`
}

func (a *App) GitHubProxyStatus() (GitHubProxyState, error) {
	a.githubMu.Lock()
	defer a.githubMu.Unlock()
	settings, err := a.store.GitHubProxySettings()
	if err != nil {
		return GitHubProxyState{}, err
	}
	addresses, err := localAccessAddresses(settings.Config.Port)
	if err != nil {
		return GitHubProxyState{}, err
	}
	for i := range addresses {
		addresses[i].URL += "/github"
	}
	return GitHubProxyState{settings, a.githubServer != nil, a.githubError, addresses}, nil
}

// Save configuration independently of the service switch. A stale form cannot
// overwrite a more recent enable/disable operation.
func (a *App) SaveGitHubProxyConfig(config githubproxy.Config) error {
	a.githubMu.Lock()
	defer a.githubMu.Unlock()
	settings, err := a.store.GitHubProxySettings()
	if err != nil {
		return err
	}
	settings.Config = config
	return a.applyGitHubLocked(settings)
}

func (a *App) SetGitHubProxyEnabled(enabled bool) error {
	a.githubMu.Lock()
	defer a.githubMu.Unlock()
	settings, err := a.store.GitHubProxySettings()
	if err != nil {
		return err
	}
	settings.Enabled = enabled
	return a.applyGitHubLocked(settings)
}

func (a *App) startGitHubProxy() {
	a.githubMu.Lock()
	defer a.githubMu.Unlock()
	settings, err := a.store.GitHubProxySettings()
	if err == nil && settings.Enabled {
		err = a.applyGitHubLocked(settings)
	}
	if err != nil {
		a.githubError = err.Error()
		log.Printf("GitHub proxy: %v", err)
	}
}

func (a *App) applyGitHubLocked(settings store.GitHubProxySettings) error {
	if err := settings.Config.Validate(); err != nil {
		return err
	}
	previous, err := a.store.GitHubProxySettings()
	if err != nil {
		return err
	}
	var handler *githubproxy.Server
	var listener net.Listener
	if settings.Enabled {
		handler, err = githubproxy.New(settings.Config)
		if err != nil {
			return err
		}
		if a.githubServer == nil || previous.Config.Port != settings.Config.Port {
			listener, err = net.Listen("tcp", relayListenAddress(settings.Config.Port))
			if err != nil {
				handler.Close()
				return fmt.Errorf("GitHub 代理端口 %d 不可用: %w", settings.Config.Port, err)
			}
		}
	}
	if err = a.store.SetGitHubProxySettings(settings); err != nil {
		if listener != nil {
			listener.Close()
		}
		if handler != nil {
			handler.Close()
		}
		return err
	}
	if !settings.Enabled || listener != nil {
		a.stopGitHubLocked()
	}
	if old := a.githubHandler.Swap(handler); old != nil {
		old.Close()
	}
	a.githubError = ""
	if listener != nil {
		server := &http.Server{ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if h := a.githubHandler.Load(); h != nil {
					h.ServeHTTP(w, r)
				} else {
					http.Error(w, "GitHub proxy stopped", http.StatusServiceUnavailable)
				}
			})}
		a.githubServer = server
		go func() {
			if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
				a.githubMu.Lock()
				defer a.githubMu.Unlock()
				if a.githubServer == server {
					a.githubServer = nil
					a.githubError = err.Error()
				}
				log.Printf("GitHub proxy stopped: %v", err)
			}
		}()
	}
	return nil
}

func (a *App) stopGitHubLocked() {
	if a.githubServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := a.githubServer.Shutdown(ctx); err != nil {
			if closeErr := a.githubServer.Close(); closeErr != nil {
				log.Printf("close GitHub proxy: %v", closeErr)
			}
		}
		cancel()
		a.githubServer = nil
	}
	if old := a.githubHandler.Swap(nil); old != nil {
		old.Close()
	}
}
