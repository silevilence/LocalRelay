package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"localrelay/internal/capabilities"
	"localrelay/internal/ir"
	"localrelay/internal/store"
)

// aggregationRuntime owns only ephemeral strategy state. Configuration stays
// in SQLite and is read for every request, so edits take effect immediately.
type aggregationRuntime struct {
	locks   sync.Map
	stateMu sync.Mutex
	states  map[string]*aggregationState
	now     func() time.Time
}

type aggregationState struct {
	next      int
	cooldowns map[string]time.Time
	tokens    map[string][]tokenSample
}
type tokenSample struct {
	at    time.Time
	value int
}
type aggregationAttempt struct {
	Member string `json:"member"`
	Error  string `json:"error"`
}
type aggregationFailure struct{ Attempts []aggregationAttempt }

func (e aggregationFailure) Error() string {
	if len(e.Attempts) == 0 {
		return "aggregation has no available members"
	}
	b, _ := json.Marshal(e.Attempts)
	return "aggregation members failed: " + string(b)
}

func newAggregationRuntime() *aggregationRuntime {
	return &aggregationRuntime{states: map[string]*aggregationState{}, now: time.Now}
}
func (r *aggregationRuntime) lock(id string) *sync.Mutex {
	value, _ := r.locks.LoadOrStore(id, &sync.Mutex{})
	return value.(*sync.Mutex)
}
func (r *aggregationRuntime) state(id string) *aggregationState {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	state := r.states[id]
	if state == nil {
		state = &aggregationState{cooldowns: map[string]time.Time{}, tokens: map[string][]tokenSample{}}
		r.states[id] = state
	}
	return state
}

func (r *aggregationRuntime) candidates(id string, cfg store.AggregationConfig, valid map[string]store.RoutedModel) []store.RoutedModel {
	// Configurations are validated when saved, but a manually corrupted database
	// must not turn a request into a modulo-by-zero or index-out-of-range panic.
	if len(cfg.Members) == 0 {
		return nil
	}
	lock := r.lock(id)
	lock.Lock()
	defer lock.Unlock()
	state, now := r.state(id), r.now()
	member := func(item store.AggregationMember) (store.RoutedModel, bool) {
		value, ok := valid[item.PublicID()]
		return value, ok
	}
	switch cfg.Strategy.Type {
	case store.AggregationRoundRobin:
		for offset := 0; offset < len(cfg.Members); offset++ {
			index := (state.next + offset) % len(cfg.Members)
			if value, ok := member(cfg.Members[index]); ok {
				state.next = (index + 1) % len(cfg.Members)
				return []store.RoutedModel{value}
			}
		}
	case store.AggregationTokenBalance:
		var selected store.RoutedModel
		best, found := int(^uint(0)>>1), false
		for _, item := range cfg.Members {
			value, ok := member(item)
			if !ok {
				continue
			}
			key := item.PublicID()
			samples := state.tokens[key]
			kept := samples[:0]
			total := 0
			for _, sample := range samples {
				if now.Sub(sample.at) < time.Hour {
					kept = append(kept, sample)
					total += sample.value
				}
			}
			state.tokens[key] = kept
			if !found || total < best {
				selected, best, found = value, total, true
			}
		}
		if found {
			return []store.RoutedModel{selected}
		}
	case store.AggregationTimeSchedule:
		for _, entry := range cfg.Strategy.Schedule {
			if entry.Hour == now.Hour() {
				if value, ok := member(entry.Member); ok {
					return []store.RoutedModel{value}
				}
				break
			}
		}
		if value, ok := member(cfg.Members[0]); ok {
			return []store.RoutedModel{value}
		}
	default:
		var result []store.RoutedModel
		for _, item := range cfg.Members {
			value, ok := member(item)
			if !ok {
				continue
			}
			if until := state.cooldowns[item.PublicID()]; until.After(now) {
				continue
			}
			result = append(result, value)
		}
		return result
	}
	return nil
}

func (r *aggregationRuntime) failure(id, member string, cooldownSeconds int) {
	lock := r.lock(id)
	lock.Lock()
	defer lock.Unlock()
	if cooldownSeconds <= 0 {
		cooldownSeconds = 60
	}
	r.state(id).cooldowns[member] = r.now().Add(time.Duration(cooldownSeconds) * time.Second)
}
func (r *aggregationRuntime) success(id, member string, tokens int) {
	lock := r.lock(id)
	lock.Lock()
	defer lock.Unlock()
	state := r.state(id)
	delete(state.cooldowns, member)
	state.tokens[member] = append(state.tokens[member], tokenSample{at: r.now(), value: tokens})
}
func aggregationAttemptsJSON(attempts []aggregationAttempt) string {
	raw, _ := json.Marshal(attempts)
	return string(raw)
}
func aggregationMemberID(routed store.RoutedModel) string {
	return routed.Provider.ID + "/" + routed.Model.ID
}
func aggregationUnavailable(id string) error {
	return fmt.Errorf("aggregation %q has no enabled members", id)
}

type aggregationResult struct {
	status int
	wrote  bool
	err    error
}

// forwardAggregation snapshots its configuration and routes a single request.
// Only primary-backup retries; the other strategies deliberately make exactly
// one attempt so their allocation policy remains predictable.
func (s *Server) forwardAggregation(ctx context.Context, w http.ResponseWriter, aggregate store.RoutedModel, incoming inboundRequest, responseWriter clientResponseWriter, streamWriter clientStreamWriter, log *store.CallLog) aggregationResult {
	result := aggregationResult{status: http.StatusBadGateway}
	aggregateID := aggregate.Provider.ID + "/" + aggregate.Model.ID
	cfg, err := s.store.GetAggregationConfig(aggregate.Provider.ID, aggregate.Model.ID)
	if err != nil {
		result.err = fmt.Errorf("load aggregation config: %w", err)
		return result
	}
	valid := map[string]store.RoutedModel{}
	attempts := []aggregationAttempt{}
	for _, member := range cfg.Members {
		routed, err := s.store.GetRoutedModel(member.PublicID())
		if err != nil {
			attempts = append(attempts, aggregationAttempt{Member: member.PublicID(), Error: err.Error()})
			continue
		}
		valid[member.PublicID()] = routed
	}
	candidates := s.aggregation.candidates(aggregateID, cfg, valid)
	if len(candidates) == 0 {
		result.err = aggregationUnavailable(aggregateID)
		log.AggregationSource, log.AggAttempts = aggregateID, aggregationAttemptsJSON(attempts)
		return result
	}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			result.status = statusClientClosedRequest
			result.err = err
			return result
		}
		memberID := aggregationMemberID(candidate)
		providerCfg, err := capabilities.Parse(candidate.Provider.CapabilityConfig)
		if err == nil {
			var request ir.Request
			request, err = incoming.toIR(providerCfg)
			if err == nil {
				request.Model = candidate.Model.ID
				var providerReq any
				providerReq, err = toProviderRequest(request, providerCfg)
				if err == nil {
					var body []byte
					body, err = json.Marshal(providerReq)
					if err == nil {
						attemptCtx := ctx
						var cancel context.CancelFunc
						if cfg.Strategy.Type == store.AggregationPrimaryBackup {
							attemptCtx, cancel = context.WithTimeout(ctx, time.Duration(cfg.Strategy.AttemptTimeoutSeconds)*time.Second)
						}
						if cancel != nil {
							defer cancel()
						}
						resp, postErr := s.postProvider(attemptCtx, candidate.Provider, providerCfg, candidate.Model.ID, request.Stream, body)
						if postErr != nil {
							err = postErr
						} else if request.Stream && resp.StatusCode >= 200 && resp.StatusCode < 300 {
							started, streamErr := s.streamNativeGatedProviderResponse(ctx, w, resp.Body, providerCfg, memberID, request, log, streamWriter)
							resp.Body.Close()
							if started {
								result.status, result.wrote, result.err = http.StatusOK, true, streamErr
								if streamErr == nil {
									s.aggregation.success(aggregateID, memberID, log.InputTokens+log.OutputTokens+log.CacheCreationInputTokens)
								}
								log.ProviderID, log.ModelID, log.Protocol, log.AggregationSource, log.AggAttempts = candidate.Provider.ID, candidate.Model.ID, providerCfg.Protocol, aggregateID, aggregationAttemptsJSON(attempts)
								return result
							}
							err = streamErr
						} else {
							respBody, readErr := io.ReadAll(resp.Body)
							resp.Body.Close()
							if readErr != nil {
								err = readErr
							} else if resp.StatusCode < 200 || resp.StatusCode >= 300 {
								err = fmt.Errorf("upstream returned %d: %s", resp.StatusCode, truncateError(respBody))
							} else {
								var irResp ir.Response
								irResp, err = parseProviderResponse(respBody, providerCfg)
								if err == nil {
									irResp.Model = memberID
									if irResp.Usage == (ir.Usage{}) {
										irResp.Usage = estimateResponseUsage(request, irResp)
										log.TokenEstimated = true
									}
									log.InputTokens, log.OutputTokens, log.CacheCreationInputTokens, log.CacheReadInputTokens = irResp.Usage.InputTokens, irResp.Usage.OutputTokens, irResp.Usage.CacheCreationInputTokens, irResp.Usage.CacheReadInputTokens
									var clientResp any
									clientResp, err = responseWriter(irResp, providerCfg)
									if err != nil {
										// Client-protocol conversion is deterministic for this
										// response and is unrelated to upstream member health.
										// Do not send an already-successful request to a backup.
										log.ProviderID, log.ModelID, log.Protocol, log.AggregationSource, log.AggAttempts = candidate.Provider.ID, candidate.Model.ID, providerCfg.Protocol, aggregateID, aggregationAttemptsJSON(attempts)
										return aggregationResult{status: http.StatusBadGateway, err: fmt.Errorf("convert aggregation response: %w", err)}
									}
									log.ProviderID, log.ModelID, log.Protocol, log.AggregationSource, log.AggAttempts = candidate.Provider.ID, candidate.Model.ID, providerCfg.Protocol, aggregateID, aggregationAttemptsJSON(attempts)
									s.aggregation.success(aggregateID, memberID, log.InputTokens+log.OutputTokens+log.CacheCreationInputTokens)
									writeJSON(w, http.StatusOK, clientResp)
									return aggregationResult{status: http.StatusOK, wrote: true}
								}
							}
						}
					}
				}
			}
		}
		if err == nil {
			err = errors.New("unknown aggregation attempt failure")
		}
		attempts = append(attempts, aggregationAttempt{Member: memberID, Error: err.Error()})
		if cfg.Strategy.Type != store.AggregationPrimaryBackup {
			break
		}
		s.aggregation.failure(aggregateID, memberID, cfg.Strategy.CooldownSeconds)
	}
	log.AggregationSource, log.AggAttempts = aggregateID, aggregationAttemptsJSON(attempts)
	result.err = aggregationFailure{Attempts: attempts}
	return result
}
