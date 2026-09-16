package model

// TokenUsage holds the token classes the Messages API reports, kept separate
// because they are priced differently. Summing them yields volume, not cost;
// use internal/cost for cost.
type TokenUsage struct {
	Input           int64 `json:"input"`
	CacheRead       int64 `json:"cache_read"`
	CacheCreation   int64 `json:"cache_creation"`
	CacheCreation5m int64 `json:"cache_creation_5m"`
	CacheCreation1h int64 `json:"cache_creation_1h"`
	Output          int64 `json:"output"`
	Thinking        int64 `json:"thinking"`
}

// PromptTokens is what the request's input side was billed for: the full
// prompt, however it was classified. Observed, per API call.
func (t TokenUsage) PromptTokens() int64 {
	return t.Input + t.CacheRead + t.CacheCreation
}

// Add accumulates usage across invocations.
func (t TokenUsage) Add(o TokenUsage) TokenUsage {
	return TokenUsage{
		Input:           t.Input + o.Input,
		CacheRead:       t.CacheRead + o.CacheRead,
		CacheCreation:   t.CacheCreation + o.CacheCreation,
		CacheCreation5m: t.CacheCreation5m + o.CacheCreation5m,
		CacheCreation1h: t.CacheCreation1h + o.CacheCreation1h,
		Output:          t.Output + o.Output,
		Thinking:        t.Thinking + o.Thinking,
	}
}

// TTLSplitConsistent reports whether the 5m/1h breakdown accounts for the
// reported cache-creation total. Entire and the API should agree; when they
// don't we want to know rather than silently prefer one.
func (t TokenUsage) TTLSplitConsistent() bool {
	if t.CacheCreation5m == 0 && t.CacheCreation1h == 0 {
		return t.CacheCreation == 0
	}
	return t.CacheCreation5m+t.CacheCreation1h == t.CacheCreation
}
