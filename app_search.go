package main

import (
	"net/http"
	"net/url"
	"strings"

	"localrelay/internal/githubproxy"
	"localrelay/internal/websearch"
)

type SearchSettingsState struct {
	SearchProvider string                   `json:"searchProvider"`
	FetchProvider  string                   `json:"fetchProvider"`
	HasAPIKeys     map[string]bool          `json:"hasApiKeys"`
	Providers      []websearch.ProviderInfo `json:"providers"`
}

type SearchSettingsInput struct {
	// Omitted selections retain current values; omitted keys retain secrets.
	SearchProvider *string           `json:"searchProvider"`
	FetchProvider  *string           `json:"fetchProvider"`
	APIKeys        map[string]string `json:"apiKeys"`
}

func (a *App) SearchSettings() (SearchSettingsState, error) {
	cfg, err := a.store.SearchSettings()
	if err != nil {
		return SearchSettingsState{}, err
	}
	state := SearchSettingsState{cfg.SearchProvider, cfg.FetchProvider, map[string]bool{}, websearch.Providers()}
	for _, p := range state.Providers {
		state.HasAPIKeys[p.ID] = cfg.APIKeys[p.ID] != ""
	}
	return state, nil
}

func (a *App) SaveSearchSettings(input SearchSettingsInput) error {
	a.githubMu.Lock()
	defer a.githubMu.Unlock()
	cfg, err := a.store.SearchSettings()
	if err != nil {
		return err
	}
	if input.SearchProvider != nil {
		cfg.SearchProvider = *input.SearchProvider
	}
	if input.FetchProvider != nil {
		cfg.FetchProvider = *input.FetchProvider
	}
	for id, key := range input.APIKeys {
		cfg.APIKeys[id] = strings.TrimSpace(key)
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
	if r.URL.Path == "/search" || r.URL.Path == "/docs/search" || r.URL.Path == "/web" {
		h.search.ServeHTTP(w, r)
		return
	}
	h.resources.ServeHTTP(w, r)
}

func (h *resourceProxyHandler) Close() {
	h.resources.Close()
	h.search.Close()
}
