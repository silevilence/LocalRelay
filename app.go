package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"localrelay/internal/relay"
	"localrelay/internal/store"
)

// App struct
type App struct {
	ctx         context.Context
	store       *store.Store
	relay       *relay.Server
	relayServer *http.Server
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	db, err := store.Open(defaultDBPath())
	if err != nil {
		panic(err)
	}
	a.store = db
	a.relay = relay.New(db)
	a.relayServer = &http.Server{Addr: "127.0.0.1:8718", Handler: a.relay}
	go func() {
		if err := a.relayServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			panic(err)
		}
	}()
}

func (a *App) shutdown(ctx context.Context) {
	if a.relayServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = a.relayServer.Shutdown(shutdownCtx)
	}
	if a.relay != nil {
		a.relay.Close()
	}
	if a.store != nil {
		_ = a.store.Close()
	}
}

// Greet returns a greeting for the given name
func (a *App) Greet(name string) string {
	return fmt.Sprintf("你好，%s。Go 后端通信正常。", name)
}

func (a *App) ListProviders() ([]store.Provider, error) {
	return a.store.ListProviders()
}

func (a *App) CreateProvider(input store.ProviderInput) (store.Provider, error) {
	return a.store.CreateProvider(input)
}

func (a *App) UpdateProvider(input store.ProviderInput) (store.Provider, error) {
	return a.store.UpdateProvider(input)
}

func (a *App) DeleteProvider(id string) error {
	return a.store.DeleteProvider(id)
}

func (a *App) ListModels(providerID string) ([]store.Model, error) {
	return a.store.ListModels(providerID)
}

func (a *App) CreateModel(input store.ModelInput) (store.Model, error) {
	return a.store.CreateModel(input)
}

func (a *App) UpdateModel(input store.ModelInput) (store.Model, error) {
	return a.store.UpdateModel(input)
}

func (a *App) DeleteModel(providerID string, id string) error {
	return a.store.DeleteModel(providerID, id)
}

func (a *App) TokenStats(filter store.TokenStatsFilter) (store.TokenStats, error) {
	return a.store.TokenStats(filter)
}

func (a *App) RelayBaseURL() string {
	return "http://127.0.0.1:8718"
}

func defaultDBPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "LocalRelay", "localrelay.db")
}
