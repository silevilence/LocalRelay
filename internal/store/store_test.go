package store

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"localrelay/internal/capabilities"

	_ "modernc.org/sqlite"
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
	if provider.CapabilityConfig == "" {
		t.Fatal("default capability config was not set")
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

func TestRelayPortDefaultsPersistsAndValidates(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()

	port, err := s.RelayPort()
	if err != nil {
		t.Fatal(err)
	}
	if port != DefaultRelayPort {
		t.Fatalf("default relay port = %d, want %d", port, DefaultRelayPort)
	}
	if err := s.SetRelayPort(9123); err != nil {
		t.Fatal(err)
	}
	port, err = s.RelayPort()
	if err != nil {
		t.Fatal(err)
	}
	if port != 9123 {
		t.Fatalf("saved relay port = %d, want 9123", port)
	}
	for _, invalid := range []int{0, -1, 65536} {
		if err := s.SetRelayPort(invalid); err == nil {
			t.Fatalf("SetRelayPort(%d) succeeded", invalid)
		}
	}
}

func TestDesktopSettingsDefaultsAndPersist(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "localrelay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	settings, err := s.DesktopSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !settings.GatewayEnabled || !settings.HideOnMinimize || !settings.HideOnClose || settings.LaunchAtLogin || !settings.StartMinimized {
		t.Fatalf("default desktop settings = %#v", settings)
	}
	settings.GatewayEnabled = false
	settings.HideOnMinimize = false
	settings.HideOnClose = false
	settings.LaunchAtLogin = true
	settings.StartMinimized = false
	settings.TrayHintShown = true
	if err := s.SetDesktopSettings(settings); err != nil {
		t.Fatal(err)
	}
	got, err := s.DesktopSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got != settings {
		t.Fatalf("desktop settings = %#v, want %#v", got, settings)
	}
}

func TestProviderCapabilityConfigIsStoredAndValidated(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()

	custom := `{"protocol":"openai_chat","thinking":{"requestFields":["enable_thinking"],"responseContentField":"reasoning_content"}}`
	if _, err := s.CreateProvider(ProviderInput{ID: "sf", Name: "SiliconFlow", Type: "siliconflow", BaseURL: "https://api.siliconflow.com/v1", CapabilityConfig: custom}); err != nil {
		t.Fatal(err)
	}
	providers, err := s.ListProviders()
	if err != nil {
		t.Fatal(err)
	}
	if providers[0].CapabilityConfig != custom {
		t.Fatalf("capability config = %q", providers[0].CapabilityConfig)
	}
	if _, err := s.CreateProvider(ProviderInput{ID: "bad", Name: "Bad", Type: "openai", BaseURL: "https://example.test", CapabilityConfig: `{`}); err == nil {
		t.Fatal("expected invalid capability config error")
	}
	if _, err := s.CreateProvider(ProviderInput{ID: "bad2", Name: "Bad2", Type: "openai", BaseURL: "https://example.test", CapabilityConfig: `{"thinking":{}}`}); err == nil {
		t.Fatal("expected missing protocol error")
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
	if !models[0].Enabled {
		t.Fatal("new models should be enabled by default")
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

func TestRoutingHonorsEnabledModels(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()

	if _, err := s.CreateProvider(ProviderInput{ID: "p1", Name: "P1", Type: "openai", BaseURL: "https://example.test/v1", APIKey: "key"}); err != nil {
		t.Fatal(err)
	}
	enabled := false
	if _, err := s.CreateModel(ModelInput{ID: "off", ProviderID: "p1", Name: "Off", Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetRoutedModel("p1/off"); err == nil || err.Error() != "model is disabled" {
		t.Fatalf("disabled route err = %v", err)
	}
	enabled = true
	if _, err := s.UpdateModel(ModelInput{ID: "off", ProviderID: "p1", Name: "On", Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	routed, err := s.GetRoutedModel("p1/off")
	if err != nil {
		t.Fatal(err)
	}
	if routed.Provider.APIKey != "key" || routed.Model.ID != "off" {
		t.Fatalf("route = %#v", routed)
	}
	if _, err := s.GetRoutedModel("off"); err == nil {
		t.Fatal("expected provider/model validation error")
	}
	if routed.Provider.CapabilityConfig == "" {
		t.Fatal("routed provider should include capability config")
	}
}

func TestListEnabledModelsFiltersDisabled(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()

	enabled := false
	if _, err := s.CreateProvider(ProviderInput{ID: "p1", Name: "P1", Type: "openai", BaseURL: "https://example.test/v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProvider(ProviderInput{ID: "p2", Name: "P2", Type: "openai", BaseURL: "https://example.test/v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(ModelInput{ID: "on", ProviderID: "p1", Name: "On"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(ModelInput{ID: "off", ProviderID: "p1", Name: "Off", Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(ModelInput{ID: "also-on", ProviderID: "p2", Name: "Also On"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO models(id, provider_id, name, created_at, updated_at) VALUES ('orphan', 'deleted', 'Orphan', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}

	models, err := s.ListEnabledModels()
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "on" || models[1].ID != "also-on" {
		t.Fatalf("enabled models = %#v", models)
	}
}

func TestAPIKeyCRUDAndAuthorizationResolution(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()

	if _, err := s.CreateAPIKey(APIKeyInput{Name: NoAppName}); err == nil {
		t.Fatal("expected reserved name error")
	}
	if _, err := s.CreateAPIKey(APIKeyInput{}); err == nil {
		t.Fatal("expected missing name error")
	}
	key, err := s.CreateAPIKey(APIKeyInput{Name: "Raycast", Description: "desktop launcher"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key.Key, "sk-") || len(key.Key) != 35 {
		t.Fatalf("generated key = %q", key.Key)
	}
	if key.Key[15] != '4' {
		t.Fatalf("generated key is not uuid-v4 shaped: %q", key.Key)
	}
	if got := s.AppNameForAuthorization("Bearer " + key.Key); got != "Raycast" {
		t.Fatalf("resolved app = %q", got)
	}
	for _, header := range []string{"", "Basic " + key.Key, "Bearer", "Bearer   "} {
		if got := s.AppNameForAuthorization(header); got != NoAppName {
			t.Fatalf("header %q resolved app = %q", header, got)
		}
	}
	updated, err := s.UpdateAPIKey(APIKeyInput{ID: key.ID, Name: "Raycast Pro", Description: "updated"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Key != key.Key || updated.Name != "Raycast Pro" {
		t.Fatalf("updated key = %#v", updated)
	}
	if err := s.DeleteAPIKey(key.ID); err != nil {
		t.Fatal(err)
	}
	keys, err := s.ListAPIKeys()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 || s.AppNameForAuthorization("Bearer "+key.Key) != NoAppName {
		t.Fatalf("deleted keys=%#v resolved=%q", keys, s.AppNameForAuthorization("Bearer "+key.Key))
	}
	if _, err := s.UpdateAPIKey(APIKeyInput{ID: key.ID, Name: "Missing"}); err != sql.ErrNoRows {
		t.Fatalf("missing update err = %v", err)
	}
	if err := s.DeleteAPIKey(key.ID); err != sql.ErrNoRows {
		t.Fatalf("missing delete err = %v", err)
	}
}

func TestCreateAPIKeyRetriesUniqueCollisions(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()

	old := newAPIKey
	defer func() { newAPIKey = old }()
	newAPIKey = func() (string, error) { return "sk-00000000000040008000000000000000", nil }
	if _, err := s.CreateAPIKey(APIKeyInput{Name: "One"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAPIKey(APIKeyInput{Name: "Two"}); err == nil || !strings.Contains(err.Error(), "collided") {
		t.Fatalf("collision err = %v", err)
	}
	newAPIKey = func() (string, error) { return "", errors.New("random failed") }
	if _, err := s.CreateAPIKey(APIKeyInput{Name: "Three"}); err == nil || !strings.Contains(err.Error(), "random failed") {
		t.Fatalf("generator err = %v", err)
	}
}

func TestListAPIKeysScanError(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()

	if _, err := s.db.Exec(`
DROP TABLE api_keys;
CREATE TABLE api_keys (
	id TEXT,
	name TEXT,
	description TEXT,
	key TEXT,
	deleted_at TEXT,
	created_at TEXT,
	updated_at TEXT
);
INSERT INTO api_keys(id, name, description, key, created_at, updated_at)
VALUES ('bad-id', 'Bad', '', 'sk-bad', 'now', 'now');`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListAPIKeys(); err == nil {
		t.Fatal("expected ListAPIKeys scan error")
	}
}

func TestCallLogTokenStats(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()

	if _, err := s.CreateProvider(ProviderInput{ID: "p1", Name: "P1", Type: "openai", BaseURL: "https://example.test/v1"}); err != nil {
		t.Fatal(err)
	}
	for _, log := range []CallLog{
		{ProviderID: "p1", ModelID: "m1", AppName: "Raycast", Protocol: "openai_chat", StartedAt: "2026-07-20T01:00:00Z", EndedAt: "2026-07-20T01:00:01Z", StatusCode: 200, InputTokens: 10, OutputTokens: 3, CacheReadInputTokens: 2, Stream: true},
		{ProviderID: "p1", ModelID: "m2", AppName: "Cursor", Protocol: "openai_chat", StartedAt: "2026-07-21T01:00:00Z", StatusCode: 200, InputTokens: 5, OutputTokens: 7, CacheCreationInputTokens: 1},
		{Protocol: "openai_chat", StartedAt: "2026-07-19T01:00:00Z", InputTokens: 1},
	} {
		if err := s.CreateCallLog(log); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := s.TokenStats(TokenStatsFilter{ProviderID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Calls != 2 || stats.InputTokens != 15 || stats.OutputTokens != 10 || len(stats.Points) != 2 {
		t.Fatalf("stats = %#v", stats)
	}
	stats, err = s.TokenStats(TokenStatsFilter{ModelID: "m2"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Calls != 1 || stats.CacheCreationInputTokens != 1 {
		t.Fatalf("filtered stats = %#v", stats)
	}

	stats, err = s.TokenStats(TokenStatsFilter{From: "2026-07-21", To: "2026-07-21"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Calls != 1 || stats.InputTokens != 5 {
		t.Fatalf("date stats = %#v", stats)
	}

	models, err := s.TokenStatModels(TokenStatsFilter{ProviderID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(models, ",") != "m1,m2" {
		t.Fatalf("logged models = %#v", models)
	}
	models, err = s.TokenStatModels(TokenStatsFilter{From: "2026-07-21", To: "2026-07-21"})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0] != "m2" {
		t.Fatalf("date logged models = %#v", models)
	}

	stats, err = s.TokenStats(TokenStatsFilter{AppName: "Raycast"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Calls != 1 || stats.InputTokens != 10 {
		t.Fatalf("app stats = %#v", stats)
	}
	apps, err := s.TokenStatApps(TokenStatsFilter{ProviderID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(apps, ",") != "Cursor,Raycast" {
		t.Fatalf("apps = %#v", apps)
	}
	rows, err := s.TokenStatRows(TokenStatsFilter{ProviderID: "p1"}, "app")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Name != "Raycast" || rows[0].TotalTokens != 13 {
		t.Fatalf("rows = %#v", rows)
	}
	trend, err := s.TokenTrend(TokenStatsFilter{ProviderID: "p1"}, "day", "app")
	if err != nil {
		t.Fatal(err)
	}
	if len(trend) != 2 || trend[0].Bucket != "2026-07-20" || trend[0].Name != "Raycast" {
		t.Fatalf("trend = %#v", trend)
	}
	page, err := s.CallLogs(TokenStatsFilter{ProviderID: "p1"}, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || page.Items[1].DurationMs != 1000 || !page.Items[1].Stream {
		t.Fatalf("log page = %#v", page)
	}
	for i := 0; i < 58; i++ {
		if err := s.CreateCallLog(CallLog{ProviderID: "p1", ModelID: "m1", Protocol: "openai_chat", StartedAt: "2026-07-22T01:00:00Z"}); err != nil {
			t.Fatal(err)
		}
	}
	page, err = s.CallLogs(TokenStatsFilter{ProviderID: "p1"}, -1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 60 || len(page.Items) != 50 {
		t.Fatalf("default page = %#v", page)
	}
	page, err = s.CallLogs(TokenStatsFilter{ProviderID: "p1"}, 1, 10001)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 50 {
		t.Fatalf("clamped page size items = %d", len(page.Items))
	}
	if _, err := s.TokenStatRows(TokenStatsFilter{}, "bad"); err == nil {
		t.Fatal("expected unsupported group error")
	}
	if _, err := s.TokenTrend(TokenStatsFilter{}, "bad", "app"); err == nil {
		t.Fatal("expected unsupported grain error")
	}
	if _, err := s.TokenTrend(TokenStatsFilter{}, "day", "bad"); err == nil {
		t.Fatal("expected unsupported trend group error")
	}
	if _, err := s.TokenTrend(TokenStatsFilter{From: "2026-07-22", To: "2026-07-22"}, "day", "app"); err == nil {
		t.Fatal("expected day grain range error")
	}
	if _, err := s.TokenTrend(TokenStatsFilter{From: "2026-07-01", To: "2026-07-06"}, "week", "app"); err == nil {
		t.Fatal("expected week grain range error")
	}
}

func TestStatHelperBranches(t *testing.T) {
	for _, group := range []string{"", "provider", "model", "app"} {
		if column, fallback, err := statGroupColumn(group); err != nil || column == "" || fallback == "" {
			t.Fatalf("statGroupColumn(%q) = %q/%q/%v", group, column, fallback, err)
		}
	}
	for _, grain := range []string{"", "day", "hour", "week"} {
		if expr, err := statBucketExpr(grain); err != nil || expr == "" {
			t.Fatalf("statBucketExpr(%q) = %q/%v", grain, expr, err)
		}
	}
	for _, filter := range []TokenStatsFilter{
		{From: "bad", To: "2026-07-02"},
		{From: "2026-07-01", To: "bad"},
	} {
		if err := validateStatsGrain(filter, "day"); err == nil {
			t.Fatalf("expected date parse error for %#v", filter)
		}
	}
}

func TestProviderAndModelDeleteEffectsOnCallLogs(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()

	if _, err := s.CreateProvider(ProviderInput{ID: "p1", Name: "P1", Type: "openai", BaseURL: "https://example.test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(ModelInput{ID: "m1", ProviderID: "p1", Name: "M1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateCallLog(CallLog{ProviderID: "p1", ModelID: "m1", Protocol: "openai_chat", StartedAt: "2026-07-21T01:00:00Z", InputTokens: 10}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteModel("p1", "m1"); err != nil {
		t.Fatal(err)
	}
	stats, err := s.TokenStats(TokenStatsFilter{ProviderID: "p1", ModelID: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Calls != 1 {
		t.Fatalf("model delete should keep logs addressable, stats = %#v", stats)
	}

	if err := s.DeleteProvider("p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProvider(ProviderInput{ID: "p1", Name: "P1 New", Type: "openai", BaseURL: "https://new.example.test"}); err != nil {
		t.Fatal(err)
	}
	stats, err = s.TokenStats(TokenStatsFilter{ProviderID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Calls != 0 {
		t.Fatalf("recreated provider should not inherit old logs, stats = %#v", stats)
	}
	stats, err = s.TokenStats(TokenStatsFilter{ModelID: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Calls != 1 {
		t.Fatalf("global model stats should keep old logs, stats = %#v", stats)
	}
}

func TestCreateCallLogDefaultsStartedAt(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()

	if err := s.CreateCallLog(CallLog{Protocol: "openai_chat", InputTokens: 1}); err != nil {
		t.Fatal(err)
	}
	var startedAt string
	if err := s.db.QueryRow(`SELECT started_at FROM call_logs`).Scan(&startedAt); err != nil {
		t.Fatal(err)
	}
	if startedAt == "" {
		t.Fatal("started_at was not defaulted")
	}
}

func TestCreateCallLogsBatchInsert(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()

	if err := s.CreateCallLogs(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProvider(ProviderInput{ID: "p1", Name: "P1", Type: "openai", BaseURL: "https://example.test"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateCallLogs([]CallLog{
		{ProviderID: "p1", ModelID: "m1", Protocol: "openai_chat", StartedAt: "2026-07-20T01:00:00Z", InputTokens: 2},
		{ProviderID: "p1", ModelID: "m1", Protocol: "openai_chat", StartedAt: "2026-07-20T02:00:00Z", OutputTokens: 3},
	}); err != nil {
		t.Fatal(err)
	}
	stats, err := s.TokenStats(TokenStatsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Calls != 2 || stats.InputTokens != 2 || stats.OutputTokens != 3 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestOpenAndClosedStoreErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(file, "db")); err == nil {
		t.Fatal("expected mkdir error")
	}

	s := openTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListProviders(); err == nil {
		t.Fatal("expected ListProviders error")
	}
	if _, err := s.CreateProvider(ProviderInput{ID: "p", Name: "P", Type: "openai", BaseURL: "https://example.test"}); err == nil {
		t.Fatal("expected CreateProvider error")
	}
	if _, err := s.UpdateProvider(ProviderInput{ID: "p", Name: "P", Type: "openai", BaseURL: "https://example.test"}); err == nil {
		t.Fatal("expected UpdateProvider error")
	}
	if err := s.DeleteProvider("p"); err == nil {
		t.Fatal("expected DeleteProvider error")
	}
	if _, err := s.ListModels(""); err == nil {
		t.Fatal("expected ListModels error")
	}
	if _, err := s.CreateModel(ModelInput{ID: "m", ProviderID: "p", Name: "M"}); err == nil {
		t.Fatal("expected CreateModel error")
	}
	if _, err := s.UpdateModel(ModelInput{ID: "m", ProviderID: "p", Name: "M"}); err == nil {
		t.Fatal("expected UpdateModel error")
	}
	if err := s.DeleteModel("p", "m"); err == nil {
		t.Fatal("expected DeleteModel error")
	}
	if _, err := s.GetRoutedModel("p/m"); err == nil {
		t.Fatal("expected GetRoutedModel error")
	}
	if _, err := s.ListEnabledModels(); err == nil {
		t.Fatal("expected ListEnabledModels error")
	}
	if _, err := s.ListAPIKeys(); err == nil {
		t.Fatal("expected ListAPIKeys error")
	}
	if _, err := s.CreateAPIKey(APIKeyInput{Name: "App"}); err == nil {
		t.Fatal("expected CreateAPIKey error")
	}
	if _, err := s.UpdateAPIKey(APIKeyInput{ID: 1, Name: "App"}); err == nil {
		t.Fatal("expected UpdateAPIKey error")
	}
	if err := s.DeleteAPIKey(1); err == nil {
		t.Fatal("expected DeleteAPIKey error")
	}
	if err := s.CreateCallLog(CallLog{Protocol: "openai_chat"}); err == nil {
		t.Fatal("expected CreateCallLog error")
	}
	if _, err := s.TokenStats(TokenStatsFilter{}); err == nil {
		t.Fatal("expected TokenStats error")
	}
	if _, err := s.TokenStatApps(TokenStatsFilter{}); err == nil {
		t.Fatal("expected TokenStatApps error")
	}
	if _, err := s.TokenStatRows(TokenStatsFilter{}, "app"); err == nil {
		t.Fatal("expected TokenStatRows error")
	}
	if _, err := s.TokenTrend(TokenStatsFilter{}, "day", "app"); err == nil {
		t.Fatal("expected TokenTrend error")
	}
	if _, err := s.CallLogs(TokenStatsFilter{}, 1, 50); err == nil {
		t.Fatal("expected CallLogs error")
	}
}

func TestValidationRejectsMissingFields(t *testing.T) {
	providers := []ProviderInput{
		{Name: "P", Type: "openai", BaseURL: "https://example.test"},
		{ID: "p", Type: "openai", BaseURL: "https://example.test"},
		{ID: "p", Name: "P", BaseURL: "https://example.test"},
		{ID: "p", Name: "P", Type: "openai"},
	}
	for _, input := range providers {
		if err := validateProvider(input); err == nil {
			t.Fatalf("expected provider validation error for %#v", input)
		}
	}

	models := []ModelInput{
		{ProviderID: "p", Name: "M"},
		{ID: "m", Name: "M"},
		{ID: "m", ProviderID: "p"},
		{ID: "m", ProviderID: "p", Name: "M", ContextLength: -1},
		{ID: "m", ProviderID: "p", Name: "M", MaxTokens: -1},
	}
	for _, input := range models {
		if err := validateModel(input); err == nil {
			t.Fatalf("expected model validation error for %#v", input)
		}
	}
}

func TestDecryptRejectsCorruptStoredKeys(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()

	if _, err := s.CreateProvider(ProviderInput{ID: "p1", Name: "P1", Type: "openai", BaseURL: "https://example.test", APIKey: "key"}); err != nil {
		t.Fatal(err)
	}
	for _, encrypted := range []string{
		"not-base64",
		base64.StdEncoding.EncodeToString([]byte("short")),
		base64.StdEncoding.EncodeToString(make([]byte, 32)),
	} {
		if _, err := s.db.Exec(`UPDATE providers SET api_key_encrypted = ? WHERE id = 'p1'`, encrypted); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ListProviders(); err == nil {
			t.Fatalf("expected decrypt error for %q", encrypted)
		}
	}
}

func TestEmptySecretHelpers(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	encrypted, err := s.encrypt("")
	if err != nil {
		t.Fatal(err)
	}
	if encrypted != "" {
		t.Fatalf("encrypted empty = %q", encrypted)
	}
	plain, err := s.decrypt("")
	if err != nil {
		t.Fatal(err)
	}
	if plain != "" {
		t.Fatalf("plain empty = %q", plain)
	}
}

func TestDeleteValidation(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()

	if err := s.DeleteProvider(" "); err == nil {
		t.Fatal("expected provider delete validation error")
	}
	if err := s.DeleteModel("", "m"); err == nil {
		t.Fatal("expected model delete validation error")
	}
	if err := s.DeleteModel("p", " "); err == nil {
		t.Fatal("expected model id validation error")
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

func TestDeleteProviderRemovesModelsWithoutForeignKeyCascade(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()

	if _, err := s.db.Exec(`DROP TABLE models`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TABLE providers`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`
CREATE TABLE providers (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	type TEXT NOT NULL,
	base_url TEXT NOT NULL,
	api_key_encrypted TEXT NOT NULL DEFAULT '',
	capability_config TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE models (
	id TEXT NOT NULL,
	provider_id TEXT NOT NULL,
	name TEXT NOT NULL,
	capabilities TEXT NOT NULL DEFAULT '',
	context_length INTEGER NOT NULL DEFAULT 0,
	max_tokens INTEGER NOT NULL DEFAULT 0,
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (provider_id, id)
);`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO providers(id, name, type, base_url, created_at, updated_at)
VALUES ('deepseek', 'DeepSeek', 'deepseek', 'https://api.deepseek.com', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO models(id, provider_id, name, created_at, updated_at)
VALUES ('deepseek-v4-pro', 'deepseek', 'DeepSeek V4 Pro', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteProvider("deepseek"); err != nil {
		t.Fatal(err)
	}
	models, err := s.ListModels("")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 0 {
		t.Fatalf("models after provider delete = %#v", models)
	}
}

func TestMigrationAddsEnabledColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localrelay.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
CREATE TABLE providers (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	type TEXT NOT NULL,
	base_url TEXT NOT NULL,
	api_key_encrypted TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE models (
	id TEXT NOT NULL,
	provider_id TEXT NOT NULL,
	name TEXT NOT NULL,
	capabilities TEXT NOT NULL DEFAULT '',
	context_length INTEGER NOT NULL DEFAULT 0,
	max_tokens INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (provider_id, id)
);`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var found bool
	rows, err := s.db.Query(`PRAGMA table_info(models)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		found = found || name == "enabled"
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("enabled column was not added")
	}

	var capabilityConfig string
	if err := s.db.QueryRow(`SELECT capability_config FROM providers WHERE id = 'p'`).Scan(&capabilityConfig); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
}

func TestMigrationAddsProviderCapabilityConfigColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localrelay.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
CREATE TABLE providers (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	type TEXT NOT NULL,
	base_url TEXT NOT NULL,
	api_key_encrypted TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
INSERT INTO providers(id, name, type, base_url, created_at, updated_at)
VALUES ('deepseek', 'DeepSeek', 'deepseek', 'https://api.deepseek.com', 'now', 'now');`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var capabilityConfig string
	if err := s.db.QueryRow(`SELECT capability_config FROM providers WHERE id = 'deepseek'`).Scan(&capabilityConfig); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(capabilityConfig, "reasoning_content") {
		t.Fatalf("backfilled capability config = %q", capabilityConfig)
	}
}

func TestMigrationAddsAPIKeyStatsColumnsBeforeIndexes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localrelay.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
CREATE TABLE schema_migrations (
	version INTEGER PRIMARY KEY,
	applied_at TEXT NOT NULL
);
INSERT INTO schema_migrations(version, applied_at) VALUES (1, 'now');
CREATE TABLE call_logs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	provider_id TEXT,
	model_id TEXT,
	protocol TEXT NOT NULL,
	started_at TEXT NOT NULL,
	ended_at TEXT,
	status_code INTEGER,
	error TEXT,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0,
	cache_read_input_tokens INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO call_logs(model_id, protocol, started_at, input_tokens)
VALUES ('m1', 'openai_chat', '2026-07-22T01:00:00Z', 3);`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	page, err := s.CallLogs(TokenStatsFilter{}, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Items[0].AppName != NoAppName {
		t.Fatalf("migrated logs = %#v", page)
	}
}

func TestBuiltinProviderPresets(t *testing.T) {
	presets := BuiltinProviderPresets()
	if len(presets) < 4 {
		t.Fatalf("presets = %#v", presets)
	}
	for _, preset := range presets {
		if preset.ID == "" || preset.Name == "" || preset.Type == "" || preset.BaseURL == "" || preset.CapabilityConfig == "" {
			t.Fatalf("bad preset = %#v", preset)
		}
		if err := validateProvider(ProviderInput{ID: preset.ID, Name: preset.Name, Type: preset.Type, BaseURL: preset.BaseURL, CapabilityConfig: preset.CapabilityConfig}); err != nil {
			t.Fatalf("preset %s invalid: %v", preset.ID, err)
		}
	}
}

func TestEnableVolcengineCodingStreamUsage(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	if _, err := s.CreateProvider(ProviderInput{
		ID:               "volc",
		Name:             "火山",
		Type:             "openai-compatible",
		BaseURL:          "https://ark.cn-beijing.volces.com/api/coding/v3",
		CapabilityConfig: `{"protocol":"openai_chat"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProvider(ProviderInput{
		ID:               "other",
		Name:             "Other",
		Type:             "openai-compatible",
		BaseURL:          "https://example.test/v1",
		CapabilityConfig: `{"protocol":"openai_chat"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.enableVolcengineCodingStreamUsage(); err != nil {
		t.Fatal(err)
	}
	providers, err := s.ListProviders()
	if err != nil {
		t.Fatal(err)
	}
	configs := make(map[string]capabilities.Provider, len(providers))
	for _, provider := range providers {
		cfg, err := capabilities.Parse(provider.CapabilityConfig)
		if err != nil {
			t.Fatal(err)
		}
		configs[provider.ID] = cfg
	}
	if !configs["volc"].Streaming.IncludeUsage || configs["other"].Streaming.IncludeUsage {
		t.Fatalf("migrated streaming configs = %#v", configs)
	}
}

func TestMigrationHelperErrors(t *testing.T) {
	s := openTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err == nil {
		t.Fatal("expected migrate error")
	}
	if err := s.ensureProviderCapabilityConfigColumn(); err == nil {
		t.Fatal("expected provider capability column error")
	}
	if err := s.ensureModelEnabledColumn(); err == nil {
		t.Fatal("expected model enabled column error")
	}
	if err := s.backfillProviderCapabilityConfig(); err == nil {
		t.Fatal("expected backfill error")
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
