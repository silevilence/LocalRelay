package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"localrelay/internal/store"
)

// App struct
type App struct {
	ctx   context.Context
	store *store.Store
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
}

func (a *App) shutdown(ctx context.Context) {
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

func defaultDBPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "LocalRelay", "localrelay.db")
}
