package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProviderEnabledDefaultsAndEdits(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	for _, initial := range []string{"default", "enabled", "disabled"} {
		t.Run(initial, func(t *testing.T) {
			in := ProviderInput{ID: initial, Name: initial, Type: "openai", BaseURL: "https://example.test/v1", APIKey: "secret"}
			if initial != "default" {
				enabled := initial == "enabled"
				in.Enabled = &enabled
			}
			created, err := s.CreateProvider(in)
			if err != nil {
				t.Fatal(err)
			}
			if created.Enabled != (initial != "disabled") {
				t.Fatalf("created enabled = %v", created.Enabled)
			}
			model, err := s.CreateModel(ModelInput{ID: "m", ProviderID: in.ID, Name: "Model", Enabled: in.Enabled})
			if err != nil || model.Enabled != created.Enabled {
				t.Fatalf("shared creation default: model=%+v err=%v", model, err)
			}
			// Model edits retain their existing nil => enabled contract; provider
			// edits below deliberately have nil => preserve semantics instead.
			model, err = s.UpdateModel(ModelInput{ID: "m", ProviderID: in.ID, Name: "Edited model"})
			if err != nil || !model.Enabled {
				t.Fatalf("omitted model state: model=%+v err=%v", model, err)
			}
			in.Enabled = nil
			in.Name = "edited"
			updated, err := s.UpdateProvider(in)
			if err != nil || updated.Enabled != created.Enabled || updated.CreatedAt != created.CreatedAt {
				t.Fatalf("omitted state: updated=%+v err=%v", updated, err)
			}
			for _, enabled := range []bool{false, true} {
				in.Enabled = &enabled
				updated, err = s.UpdateProvider(in)
				if err != nil || updated.Enabled != enabled {
					t.Fatalf("explicit state %v: updated=%+v err=%v", enabled, updated, err)
				}
				providers, err := s.ListProviders()
				if err != nil {
					t.Fatal(err)
				}
				for _, provider := range providers {
					if provider.ID == in.ID && provider.Enabled != enabled {
						t.Fatalf("persisted enabled = %v, want %v", provider.Enabled, enabled)
					}
				}
			}
		})
	}
}

func TestSetProviderEnabledPreservesConfigurationAndModelState(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	before, err := s.CreateProvider(ProviderInput{ID: "p", Name: "Provider", Type: "openai", BaseURL: "https://example.test/v1", APIKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(ModelInput{ID: "on", ProviderID: "p", Name: "On"}); err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, err := s.CreateModel(ModelInput{ID: "off", ProviderID: "p", Name: "Off", Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	var encryptedBefore string
	if err := s.db.QueryRow(`SELECT api_key_encrypted FROM providers WHERE id = 'p'`).Scan(&encryptedBefore); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, false, true} {
		if err := s.SetProviderEnabled(" p ", enabled); err != nil {
			t.Fatal(err)
		}
		providers, err := s.ListProviders()
		if err != nil || len(providers) != 1 {
			t.Fatalf("providers=%+v err=%v", providers, err)
		}
		want := before
		want.Enabled, want.UpdatedAt = enabled, providers[0].UpdatedAt
		if !reflect.DeepEqual(providers[0], want) {
			t.Fatalf("toggle changed configuration: got=%+v want=%+v", providers[0], want)
		}
		var encryptedAfter string
		if err := s.db.QueryRow(`SELECT api_key_encrypted FROM providers WHERE id = 'p'`).Scan(&encryptedAfter); err != nil || encryptedAfter != encryptedBefore {
			t.Fatalf("encrypted key changed: err=%v", err)
		}
		models, err := s.ListModels("p")
		if err != nil || len(models) != 2 || models[0].Enabled || !models[1].Enabled {
			t.Fatalf("model flags changed: models=%+v err=%v", models, err)
		}
		visible, err := s.ListEnabledModels()
		if err != nil || len(visible) != boolCount(enabled) {
			t.Fatalf("visible=%+v err=%v", visible, err)
		}
		for _, modelID := range []string{"on", "off"} {
			routed, err := s.GetRoutedModel("p/" + modelID)
			switch {
			case !enabled:
				if !errors.Is(err, ErrProviderDisabled) {
					t.Fatalf("disabled provider route: %v", err)
				}
			case modelID == "off":
				if !errors.Is(err, ErrModelDisabled) {
					t.Fatalf("disabled model route: %v", err)
				}
			default:
				if err != nil || !routed.Provider.Enabled || routed.Provider.APIKey != "secret" {
					t.Fatalf("enabled route=%+v err=%v", routed, err)
				}
			}
		}
	}
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestProviderEnabledMigrationAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE providers (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, type TEXT NOT NULL,
		base_url TEXT NOT NULL, api_key_encrypted TEXT NOT NULL DEFAULT '',
		capability_config TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL
	);
	INSERT INTO providers(id, name, type, base_url, created_at, updated_at)
	VALUES ('legacy', 'Legacy', 'openai', 'https://example.test/v1', 'then', 'then');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	providers, err := s.ListProviders()
	if err != nil || len(providers) != 1 || !providers[0].Enabled {
		t.Fatalf("migrated providers=%+v err=%v", providers, err)
	}
	if err := s.SetProviderEnabled("legacy", false); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ensureProviderEnabledColumn(); err != nil {
		t.Fatal(err)
	}
	providers, err = s.ListProviders()
	if err != nil || len(providers) != 1 || providers[0].Enabled {
		t.Fatalf("reopened providers=%+v err=%v", providers, err)
	}
	var migrations int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 11`).Scan(&migrations); err != nil || migrations != 1 {
		t.Fatalf("migration count=%d err=%v", migrations, err)
	}
}

func TestSetProviderEnabledErrors(t *testing.T) {
	s := openTestStore(t)
	if err := s.SetProviderEnabled("  ", true); err == nil {
		t.Fatal("expected empty ID error")
	}
	if err := s.SetProviderEnabled("missing", true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing provider: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProviderEnabled("p", false); err == nil {
		t.Fatal("expected closed DB error")
	}
	if err := s.ensureProviderEnabledColumn(); err == nil {
		t.Fatal("expected migration error")
	}
}
