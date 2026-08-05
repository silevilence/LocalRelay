package store

import "testing"

func TestAggregationModelPersistsValidatedConfig(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	for _, p := range []ProviderInput{{ID: "real", Name: "Real", Type: "openai", BaseURL: "https://example.test/v1"}, {ID: "agg", Name: "Aggregate", Type: AggregationProviderType}} {
		if _, err := s.CreateProvider(p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateModel(ModelInput{ID: "m", ProviderID: "real", Name: "Real"}); err != nil {
		t.Fatal(err)
	}
	cfg := &AggregationConfig{Members: []AggregationMember{{ProviderID: "real", ModelID: "m"}}, Strategy: AggregationStrategy{Type: AggregationPrimaryBackup}}
	model, err := s.CreateModel(ModelInput{ID: "route", ProviderID: "agg", Name: "Route", ContextLength: 100, MaxTokens: 100, Capabilities: "{\"tools\":true}", Aggregation: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if model.ContextLength != 0 || model.MaxTokens != 0 || model.Capabilities != "" {
		t.Fatalf("aggregate model = %#v", model)
	}
	got, err := s.GetAggregationConfig("agg", "route")
	if err != nil {
		t.Fatal(err)
	}
	if got.Strategy.CooldownSeconds != 60 || got.Members[0].PublicID() != "real/m" {
		t.Fatalf("config = %#v", got)
	}
	if _, err := s.CreateModel(ModelInput{ID: "bad", ProviderID: "agg", Name: "Bad", Aggregation: &AggregationConfig{Members: []AggregationMember{{ProviderID: "agg", ModelID: "route"}}}}); err == nil {
		t.Fatal("expected nested aggregation rejection")
	}
}

func TestAggregationConfigKeyDoesNotCollideWithRealModelDeletion(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	for _, p := range []ProviderInput{{ID: "real", Name: "Real", Type: "openai", BaseURL: "https://example.test/v1"}, {ID: "agg", Name: "Aggregate", Type: AggregationProviderType}} {
		if _, err := s.CreateProvider(p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateModel(ModelInput{ID: "same", ProviderID: "real", Name: "Real"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(ModelInput{ID: "same", ProviderID: "agg", Name: "Aggregate", Aggregation: &AggregationConfig{Members: []AggregationMember{{ProviderID: "real", ModelID: "same"}}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteModel("real", "same"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAggregationConfig("agg", "same"); err != nil {
		t.Fatalf("aggregation configuration was deleted: %v", err)
	}
	models, err := s.ListModels("agg")
	if err != nil || len(models) != 1 {
		t.Fatalf("aggregate list = %#v, %v", models, err)
	}
}

func TestValidateAggregationConfigRejectsInvalidMembersAndSchedule(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	if _, err := s.CreateProvider(ProviderInput{ID: "p", Name: "P", Type: "openai", BaseURL: "https://example.test/v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(ModelInput{ID: "m", ProviderID: "p", Name: "M"}); err != nil {
		t.Fatal(err)
	}
	member := AggregationMember{ProviderID: "p", ModelID: "m"}
	for _, cfg := range []AggregationConfig{
		{Members: []AggregationMember{{ProviderID: "missing", ModelID: "m"}}},
		{Members: []AggregationMember{member, member}},
		{Members: []AggregationMember{member}, Strategy: AggregationStrategy{Type: "invalid"}},
		{Members: []AggregationMember{member}, Strategy: AggregationStrategy{Type: AggregationTimeSchedule, Schedule: []AggregationScheduleEntry{{Hour: 24, Member: member}}}},
		{Members: []AggregationMember{member}, Strategy: AggregationStrategy{Type: AggregationTimeSchedule, Schedule: []AggregationScheduleEntry{{Hour: 1, Member: member}, {Hour: 1, Member: member}}}},
		{Members: []AggregationMember{member}, Strategy: AggregationStrategy{Type: AggregationTimeSchedule, Schedule: []AggregationScheduleEntry{{Hour: 1, Member: AggregationMember{ProviderID: "p", ModelID: "other"}}}}},
	} {
		if err := s.validateAggregationConfig(cfg); err == nil {
			t.Fatalf("expected validation error for %#v", cfg)
		}
	}
}

func TestAggregationSourceFiltersAndGroupsCallLogs(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	if _, err := s.CreateProvider(ProviderInput{ID: "p", Name: "P", Type: "openai", BaseURL: "https://example.test/v1"}); err != nil {
		t.Fatal(err)
	}
	for _, log := range []CallLog{{ProviderID: "p", ModelID: "m", Protocol: "openai_chat", StartedAt: "2026-08-05T10:00:00Z", InputTokens: 3, AggregationSource: "agg/route"}, {ProviderID: "p", ModelID: "m", Protocol: "openai_chat", StartedAt: "2026-08-05T10:01:00Z", InputTokens: 2}} {
		if err := s.CreateCallLog(log); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := s.TokenStats(TokenStatsFilter{AggregationSource: "agg/route"})
	if err != nil || stats.Calls != 1 || stats.InputTokens != 3 {
		t.Fatalf("stats = %#v, %v", stats, err)
	}
	rows, err := s.TokenStatRows(TokenStatsFilter{}, "aggregation")
	if err != nil || len(rows) != 2 || rows[0].Name != "agg/route" {
		t.Fatalf("rows = %#v, %v", rows, err)
	}
	page, err := s.CallLogs(TokenStatsFilter{AggregationSource: "agg/route"}, 1, 10)
	if err != nil || page.Total != 1 || page.Items[0].AggregationSource != "agg/route" {
		t.Fatalf("page = %#v, %v", page, err)
	}
}

func TestMigrateAggregationConfigKeysAndRejectMalformedConfig(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	for _, p := range []ProviderInput{{ID: "real", Name: "Real", Type: "openai", BaseURL: "https://example.test/v1"}, {ID: "agg", Name: "Aggregate", Type: AggregationProviderType}} {
		if _, err := s.CreateProvider(p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateModel(ModelInput{ID: "member", ProviderID: "real", Name: "Member"}); err != nil {
		t.Fatal(err)
	}
	now := timestamp()
	if _, err := s.db.Exec(`INSERT INTO models(id, provider_id, name, capabilities, context_length, max_tokens, enabled, created_at, updated_at) VALUES ('route', 'agg', 'Route', '', 0, 0, 1, ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO aggregation_configs(model_id, config_json, updated_at) VALUES ('route', '{"members":[{"providerId":"real","modelId":"member"}]}', ?)`, now); err != nil {
		t.Fatal(err)
	}
	if err := s.migrateAggregationConfigKeys(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAggregationConfig("agg", "route"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAggregationConfig("missing", "route"); err == nil {
		t.Fatal("expected missing config error")
	}
	if _, err := s.db.Exec(`UPDATE aggregation_configs SET config_json = '{bad' WHERE model_id = 'agg/route'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListModels("agg"); err == nil {
		t.Fatal("expected malformed config error")
	}
}

func TestListAggregationMemberModelsReturnsOnlyRealModels(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	for _, p := range []ProviderInput{{ID: "real", Name: "Real", Type: "openai", BaseURL: "https://example.test/v1"}, {ID: "agg", Name: "Aggregate", Type: AggregationProviderType}} {
		if _, err := s.CreateProvider(p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateModel(ModelInput{ID: "member", ProviderID: "real", Name: "Member"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO models(id, provider_id, name, capabilities, context_length, max_tokens, enabled, created_at, updated_at) VALUES ('broken', 'agg', 'Broken', '', 0, 0, 1, ?, ?)`, timestamp(), timestamp()); err != nil {
		t.Fatal(err)
	}
	models, err := s.ListAggregationMemberModels()
	if err != nil || len(models) != 1 || models[0].ProviderID != "real" || models[0].ID != "member" {
		t.Fatalf("models = %#v, %v", models, err)
	}
}
