package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// AggregationProviderType is a virtual provider which only owns aggregation
// models. It never has an upstream URL, key, or protocol configuration.
const AggregationProviderType = "aggregation"

const (
	AggregationPrimaryBackup = "primary_backup"
	AggregationRoundRobin    = "round_robin"
	AggregationTokenBalance  = "token_balance"
	AggregationTimeSchedule  = "time_schedule"
)

type AggregationMember struct {
	ProviderID string `json:"providerId"`
	ModelID    string `json:"modelId"`
}

func (m AggregationMember) PublicID() string { return m.ProviderID + "/" + m.ModelID }

type AggregationScheduleEntry struct {
	Hour   int               `json:"hour"`
	Member AggregationMember `json:"member"`
}

type AggregationStrategy struct {
	Type                  string                     `json:"type"`
	CooldownSeconds       int                        `json:"cooldownSeconds,omitempty"`
	AttemptTimeoutSeconds int                        `json:"attemptTimeoutSeconds,omitempty"`
	Schedule              []AggregationScheduleEntry `json:"schedule,omitempty"`
}

type AggregationConfig struct {
	Members  []AggregationMember `json:"members"`
	Strategy AggregationStrategy `json:"strategy"`
}

func (c AggregationConfig) Normalized() AggregationConfig {
	if c.Strategy.Type == "" {
		c.Strategy.Type = AggregationPrimaryBackup
	}
	if c.Strategy.Type == AggregationPrimaryBackup && c.Strategy.CooldownSeconds <= 0 {
		c.Strategy.CooldownSeconds = 60
	}
	if c.Strategy.Type == AggregationPrimaryBackup && c.Strategy.AttemptTimeoutSeconds <= 0 {
		c.Strategy.AttemptTimeoutSeconds = 10
	}
	return c
}

func aggregationConfigKey(providerID, modelID string) string {
	return strings.TrimSpace(providerID) + "/" + strings.TrimSpace(modelID)
}

func (s *Store) GetAggregationConfig(providerID, modelID string) (AggregationConfig, error) {
	var raw string
	if err := s.db.QueryRow(`SELECT config_json FROM aggregation_configs WHERE model_id = ?`, aggregationConfigKey(providerID, modelID)).Scan(&raw); err != nil {
		return AggregationConfig{}, err
	}
	var cfg AggregationConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return AggregationConfig{}, fmt.Errorf("decode aggregation config: %w", err)
	}
	return cfg.Normalized(), nil
}

func (s *Store) saveAggregationConfig(tx *sql.Tx, providerID, modelID string, cfg AggregationConfig) error {
	cfg = cfg.Normalized()
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO aggregation_configs(model_id, config_json, updated_at) VALUES (?, ?, ?)
ON CONFLICT(model_id) DO UPDATE SET config_json = excluded.config_json, updated_at = excluded.updated_at`, aggregationConfigKey(providerID, modelID), string(raw), timestamp())
	return err
}

func (s *Store) validateAggregationConfig(cfg AggregationConfig) error {
	cfg = cfg.Normalized()
	if len(cfg.Members) == 0 {
		return errors.New("aggregation requires at least one member")
	}
	switch cfg.Strategy.Type {
	case AggregationPrimaryBackup, AggregationRoundRobin, AggregationTokenBalance, AggregationTimeSchedule:
	default:
		return fmt.Errorf("unknown aggregation strategy %q", cfg.Strategy.Type)
	}
	seen := make(map[string]struct{}, len(cfg.Members))
	for _, member := range cfg.Members {
		if strings.TrimSpace(member.ProviderID) == "" || strings.TrimSpace(member.ModelID) == "" {
			return errors.New("aggregation member providerId and modelId are required")
		}
		if _, ok := seen[member.PublicID()]; ok {
			return fmt.Errorf("aggregation member %q is duplicated", member.PublicID())
		}
		seen[member.PublicID()] = struct{}{}
		var providerType string
		err := s.db.QueryRow(`SELECT p.type FROM models m JOIN providers p ON p.id = m.provider_id WHERE m.provider_id = ? AND m.id = ?`, member.ProviderID, member.ModelID).Scan(&providerType)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("aggregation member %q does not exist", member.PublicID())
		}
		if err != nil {
			return err
		}
		if providerType == AggregationProviderType {
			return fmt.Errorf("aggregation member %q cannot be another aggregation model", member.PublicID())
		}
	}
	if cfg.Strategy.Type == AggregationTimeSchedule {
		hours := map[int]bool{}
		for _, entry := range cfg.Strategy.Schedule {
			if entry.Hour < 0 || entry.Hour > 23 || hours[entry.Hour] {
				return errors.New("schedule hours must be unique values from 0 through 23")
			}
			hours[entry.Hour] = true
			if _, ok := seen[entry.Member.PublicID()]; !ok {
				return fmt.Errorf("scheduled member %q is not in members", entry.Member.PublicID())
			}
		}
	}
	return nil
}
