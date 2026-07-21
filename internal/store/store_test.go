package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestProviderCRUDEncryptsAPIKey(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()

	provider, err := s.CreateProvider(ProviderInput{
		ID:      "openai",
		Name:    "OpenAI",
		Type:    "openai",
		BaseURL: "https://api.openai.com/v1",
		APIKey:  "sk-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider.ID != "openai" {
		t.Fatalf("provider id = %q", provider.ID)
	}

	var raw string
	if err := s.db.QueryRow(`SELECT api_key_encrypted FROM providers WHERE id = ?`, "openai").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw == "sk-test" || raw == "" {
		t.Fatalf("api key was not encrypted: %q", raw)
	}

	providers, err := s.ListProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 1 || providers[0].APIKey != "sk-test" {
		t.Fatalf("providers = %#v", providers)
	}

	if _, err := s.UpdateProvider(ProviderInput{
		ID:      "openai",
		Name:    "OpenAI Updated",
		Type:    "openai",
		BaseURL: "https://example.test/v1",
		APIKey:  "sk-new",
	}); err != nil {
		t.Fatal(err)
	}
	providers, err = s.ListProviders()
	if err != nil {
		t.Fatal(err)
	}
	if providers[0].Name != "OpenAI Updated" || providers[0].APIKey != "sk-new" {
		t.Fatalf("updated provider = %#v", providers[0])
	}

	if err := s.DeleteProvider("openai"); err != nil {
		t.Fatal(err)
	}
	providers, err = s.ListProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 0 {
		t.Fatalf("providers after delete = %#v", providers)
	}
}

func TestModelCRUDAndProviderCascade(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()

	if _, err := s.CreateProvider(ProviderInput{ID: "p1", Name: "P1", Type: "openai", BaseURL: "https://example.test", APIKey: "key"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(ModelInput{
		ID:            "gpt-test",
		ProviderID:    "p1",
		Name:          "GPT Test",
		Capabilities:  `{"stream":true}`,
		ContextLength: 128000,
		MaxTokens:     4096,
	}); err != nil {
		t.Fatal(err)
	}

	models, err := s.ListModels("p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "gpt-test" {
		t.Fatalf("models = %#v", models)
	}

	if _, err := s.UpdateModel(ModelInput{ID: "gpt-test", ProviderID: "p1", Name: "GPT Test 2", ContextLength: 256000, MaxTokens: 8192}); err != nil {
		t.Fatal(err)
	}
	models, err = s.ListModels("")
	if err != nil {
		t.Fatal(err)
	}
	if models[0].Name != "GPT Test 2" || models[0].MaxTokens != 8192 {
		t.Fatalf("updated model = %#v", models[0])
	}

	if err := s.DeleteModel("p1", "gpt-test"); err != nil {
		t.Fatal(err)
	}
	models, err = s.ListModels("p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 0 {
		t.Fatalf("models after delete = %#v", models)
	}

	if _, err := s.CreateModel(ModelInput{ID: "cascade", ProviderID: "p1", Name: "Cascade"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProvider("p1"); err != nil {
		t.Fatal(err)
	}
	models, err = s.ListModels("")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 0 {
		t.Fatalf("models after provider delete = %#v", models)
	}
}

func TestUpdateMissingRows(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()

	_, err := s.UpdateProvider(ProviderInput{ID: "missing", Name: "Missing", Type: "openai", BaseURL: "https://example.test"})
	if err != sql.ErrNoRows {
		t.Fatalf("provider update err = %v", err)
	}

	_, err = s.UpdateModel(ModelInput{ID: "missing", ProviderID: "missing", Name: "Missing"})
	if err != sql.ErrNoRows {
		t.Fatalf("model update err = %v", err)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "localrelay.db"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
