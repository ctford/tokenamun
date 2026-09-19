// Package costtest holds the one session that every pricing surface has to
// get right.
//
// It exists because the same mistake was made four separate times: pick the
// weights once, from the first model in the session, and apply them to every
// call in it. Each copy was found and fixed on its own, and a test written
// beside each fix would have covered that copy and let the fifth in. So the
// fixture is shared, and anything in this repository that turns tokens into
// cost points a test at it.
//
// Nothing shipped imports this. It is a package rather than a helper inside
// internal/cost so that the binary does not carry a fixture, and it imports
// only internal/model so that the pricing code can import nothing of it.
package costtest

import (
	"time"

	"github.com/ctford/tokenamun/internal/model"
)

// The two models the fixture mixes. Every weight is identical between them
// except the cache read -- 0.1x against 0.025x -- which is the only field a
// session-wide choice of weights can get wrong, and the one that matters,
// because cache reads are the great majority of prompt volume.
const (
	Expensive = "claude-opus-5"
	Cheap     = "claude-fable-5-1"
)

// MixedPricingSession is a session whose first call is on the expensive
// pricing and whose remaining calls are on the cheap one, with cache reads
// dominating.
//
// Synthetic and hand-written, with counts chosen to make the error visible
// rather than copied from anything recorded. Priced at the first model's
// weights the reads cost four times what they did, and since they are ~97%
// of the prompt volume the whole session comes out far too expensive; priced
// per call it is right. Any surface that reports a cost for this session and
// disagrees with cost.SessionCost is choosing its weights once.
func MixedPricingSession() *model.Session {
	base := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	invs := []model.ModelInvocation{{
		Seq: 0, Model: Expensive, Version: "2.1.246", Effort: "high",
		Timestamp: base, Entries: 1,
		Usage: model.TokenUsage{
			Input: 2_000, CacheCreation: 40_000, CacheCreation5m: 40_000,
			Output: 800, Thinking: 200,
		},
	}}
	for seq := 1; seq <= 5; seq++ {
		invs = append(invs, model.ModelInvocation{
			Seq: seq, Model: Cheap, Version: "2.1.246", Effort: "high",
			Timestamp: base.Add(time.Duration(seq) * time.Minute), Entries: 1,
			Usage: model.TokenUsage{
				Input: 200, CacheRead: 120_000,
				CacheCreation: 3_000, CacheCreation5m: 3_000,
				Output: 600, Thinking: 100,
			},
		})
	}

	s := &model.Session{
		Ref: model.SessionRef{ID: "mixed-pricing-fixture", Origin: model.FromEntire,
			Modified: base.Add(10 * time.Minute)},
		Invocations: invs,
		Prompts:     1,
		ProseBytes:  4_000,
		Estimator:   model.TokenEstimator{BytesPerToken: 3.6, Method: "calibrated"},
	}
	s.PromptEntries = []model.PromptEntry{{InvocationSeq: 0, Bytes: 3_600}}
	s.ToolCalls = []model.ToolCall{{
		ID: "call-1", Name: "Bash", Command: "grep -r thing .",
		InvocationSeq: 1, InputBytes: 400,
	}}
	s.Retrievals = []model.RetrievedContent{{
		Seq: 0, Tool: "Bash", ToolID: "call-1", CommandBinary: "grep",
		InvocationSeq: 1, Bytes: 36_000, Tokens: 10_000,
	}}
	return s
}
