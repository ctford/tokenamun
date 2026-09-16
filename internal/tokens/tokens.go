// Package tokens counts tokens in observed content.
//
// There is no public Claude tokenizer, and tiktoken must not be used: it is
// OpenAI's, and undercounts Claude by 15-20% on prose and more on code, which
// is most of what we measure. So the offline default is a ratio estimator
// calibrated against the session's own observed prompt growth, and every
// result it produces is labelled derived-approx.
package tokens

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/ctford/tokenamun/internal/model"
)

// Counter turns observed bytes into a token count.
type Counter interface {
	// Count returns the token count for content of n bytes.
	Count(n int) float64
	// Provenance says how trustworthy the result is.
	Provenance() model.Provenance
	// Describe explains the method, for printing next to the numbers.
	Describe() string
}

// FallbackRatio is used when a session offers too little evidence to
// calibrate. It sits inside the range observed on real transcripts rather
// than being the familiar chars/4, which is an OpenAI-shaped guess.
const FallbackRatio = 3.6

// bounds keep a calibration from running away on noisy data. Outside this
// range the fit is rejected rather than trusted.
const (
	minRatio = 2.0
	maxRatio = 8.0
)

// Ratio is a bytes-per-token estimator.
type Ratio struct {
	BytesPerToken float64
	// PerCallOverhead is the fixed token cost each call carries regardless of
	// content: system reminders, attachments, message envelopes. Fitting it
	// separately keeps it from being absorbed into the ratio.
	PerCallOverhead float64
	// Residual is the share of observed growth the fit could not explain.
	Residual float64
	// Samples is how many call pairs the fit used.
	Samples     int
	Calibrated  bool
	Explanation string
}

// Count implements Counter. It returns content tokens only; per-call overhead
// is a property of the call, not of the content, and is reported separately.
func (r Ratio) Count(n int) float64 {
	if r.BytesPerToken <= 0 {
		return float64(n) / FallbackRatio
	}
	return float64(n) / r.BytesPerToken
}

// Provenance implements Counter. An estimate is never labelled derived.
func (r Ratio) Provenance() model.Provenance { return model.DerivedApprox }

// Describe implements Counter.
func (r Ratio) Describe() string { return r.Explanation }

// Sample is one observation used for calibration: the observed growth in
// prompt size between two consecutive API calls, the output tokens the model
// generated in between (observed, so not estimated), and the bytes of tool
// result content that arrived in between.
type Sample struct {
	PromptGrowth int64
	OutputTokens int64
	ResultBytes  int
}

// Calibrate fits bytes-per-token against a session's own observed growth.
//
// Per-call prompt size is observed, so growth between calls is observed too.
// Of that growth, the output tokens are known exactly; what remains should be
// the tool-result content plus a fixed per-call overhead. Fitting both gives
// an estimator grounded in this session's actual content rather than in a
// generic constant.
//
// Ordinary least squares on
//
//	growth - output = overhead + bytes * tokensPerByte
//
// An intercept matters: without one, a constant per-call overhead is
// indistinguishable from a smaller byte ratio, and the fit quietly absorbs it.
// Separating them also requires the result sizes to vary, so a session whose
// results are all the same size cannot be calibrated and says so.
func Calibrate(samples []Sample) Ratio {
	var xs, ys []float64
	for _, s := range samples {
		// Skip pairs that cannot inform the fit: a context reset (negative
		// growth), or a step with no content to attribute.
		if s.ResultBytes <= 0 || s.PromptGrowth <= 0 {
			continue
		}
		remainder := float64(s.PromptGrowth - s.OutputTokens)
		if remainder <= 0 {
			continue
		}
		xs = append(xs, float64(s.ResultBytes))
		ys = append(ys, remainder)
	}
	used := len(xs)
	if used < 4 {
		return uncalibrated(used, "too few comparable call pairs in this session")
	}

	n := float64(used)
	var sx, sy, sxy, sxx float64
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
		sxy += xs[i] * ys[i]
		sxx += xs[i] * xs[i]
	}
	denom := n*sxx - sx*sx
	if denom <= 0 {
		return uncalibrated(used, "the result sizes in this session do not vary "+
			"enough to separate content cost from per-call overhead")
	}
	slope := (n*sxy - sx*sy) / denom // tokens per byte
	intercept := (sy - slope*sx) / n // fixed tokens per call

	if slope <= 0 {
		return uncalibrated(used, "the fit implied non-positive content cost")
	}
	ratio := 1 / slope
	if ratio < minRatio || ratio > maxRatio {
		return uncalibrated(used, "the fit fell outside the plausible range")
	}
	if intercept < 0 {
		// A negative overhead is not meaningful; report it as zero rather than
		// letting it subtract from content estimates.
		intercept = 0
	}

	// Report how much of the observed growth the fit accounts for. What is
	// left is unattributed, and is reported rather than distributed.
	var totalGrowth, explained float64
	for _, s := range samples {
		if s.PromptGrowth <= 0 {
			continue
		}
		totalGrowth += float64(s.PromptGrowth)
		explained += float64(s.OutputTokens) + intercept + float64(s.ResultBytes)*slope
	}
	residual := 0.0
	if totalGrowth > 0 {
		residual = 1 - explained/totalGrowth
	}

	return Ratio{
		BytesPerToken:   ratio,
		PerCallOverhead: intercept,
		Residual:        residual,
		Samples:         used,
		Calibrated:      true,
		Explanation:     "calibrated against this session's observed prompt growth",
	}
}

func uncalibrated(samples int, why string) Ratio {
	return Ratio{
		BytesPerToken: FallbackRatio,
		Samples:       samples,
		Explanation:   "uncalibrated: " + why + "; using the default byte ratio",
	}
}

// Hash identifies content so that repeated retrieval can be detected. Only a
// prefix is kept: it is long enough to make a collision irrelevant at session
// scale and short enough to print.
func Hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}
