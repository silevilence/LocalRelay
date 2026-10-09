package main

import (
	"net/http"
	"net/url"
	"strings"

	"localrelay/internal/githubproxy"
	"localrelay/internal/websearch"
)

type SearchSettingsState struct {
	Provider  string                   `json:"provider"`
	HasAPIKey bool                     `json:"hasApiKey"`
	Providers []websearch.ProviderInfo `json:"providers"`
}

type SearchSettingsInput struct {
	Provider string `json:"provider"`
	// nil retains the current key; an explicit empty string removes it.
	APIKey *string `json:"apiKey"`
}

func (a *App) SearchSettings() (SearchSettingsState, error) {
	cfg, err := a.store.SearchSettings()
	if err != nil {
		return SearchSettingsState{}, err
	}
	return SearchSettingsState{cfg.Provider, cfg.APIKey != "", websearch.Providers()}, nil
}

func (a *App) SaveSearchSettings(input SearchSettingsInput) error {
	a.githubMu.Lock()
	defer a.githubMu.Unlock()
	cfg, err := a.store.SearchSettings()
	if err != nil {
		return err
	}
	// A provider switch must not silently reuse another provider's credential.
	if cfg.Provider != input.Provider {
		cfg.APIKey = ""
	}
	cfg.Provider = input.Provider
	if input.APIKey != nil {
		cfg.APIKey = strings.TrimSpace(*input.APIKey)
	}
	return a.store.SetSearchSettings(cfg)
}

// Only the desktop resource listener composes these independent modules.
// GitHub/npm allowlists and their retry rules never handle search requests.
type resourceProxyHandler struct {
	resources *githubproxy.Server
	search    *websearch.Server
}

func (a *App) newResourceProxyHandler(config githubproxy.Config) (*resourceProxyHandler, error) {
	resources, err := githubproxy.New(config)
	if err != nil {
		return nil, err
	}
	var proxyURL *url.URL
	if config.ProxyAddress != "" {
		proxyURL, err = url.Parse(config.ProxyType + "://" + config.ProxyAddress)
		if err != nil {
			resources.Close()
			return nil, err
		}
	}
	return &resourceProxyHandler{resources, websearch.New(a.store.SearchSettings, config.Mode, proxyURL)}, nil
}

func (h *resourceProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/search" || r.URL.Path == "/docs/search" {
		h.search.ServeHTTP(w, r)
		return
	}
	h.resources.ServeHTTP(w, r)
}

func (h *resourceProxyHandler) Close() {
	h.resources.Close()
	h.search.Close()
}
