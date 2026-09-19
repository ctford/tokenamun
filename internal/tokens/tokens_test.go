package tokens

import (
	"math"
	"testing"

	"github.com/ctford/tokenamun/internal/model"
)

func TestCalibrateRecoversAKnownRatio(t *testing.T) {
	const want = 3.2
	var samples []Sample
	for i := 1; i <= 10; i++ {
		bytes := 1000 * i
		out := int64(50 * i)
		growth := out + int64(float64(bytes)/want)
		samples = append(samples, Sample{PromptGrowth: growth, OutputTokens: out, ResultBytes: bytes})
	}
	got := Calibrate(samples)
	if !got.Calibrated {
		t.Fatalf("expected a calibrated fit, got %+v", got)
	}
	if math.Abs(got.BytesPerToken-want) > 0.05 {
		t.Fatalf("recovered %.3f bytes/token, want %.3f", got.BytesPerToken, want)
	}
	if math.Abs(got.Residual) > 0.01 {
		t.Fatalf("clean data should leave almost no residual, got %.3f", got.Residual)
	}
}

func TestCalibrateReportsUnexplainedGrowth(t *testing.T) {
	// Real sessions carry overhead the content cannot explain -- system
	// reminders, attachments, envelopes. It must surface as residual rather
	// than being absorbed into the ratio.
	const ratio = 3.5
	var samples []Sample
	for i := 1; i <= 10; i++ {
		bytes := 500 * i // sizes must vary or overhead is unidentifiable
		out := int64(100)
		clean := out + int64(float64(bytes)/ratio)
		samples = append(samples, Sample{
			PromptGrowth: clean + 200, // fixed per-call overhead
			OutputTokens: out,
			ResultBytes:  bytes,
		})
	}
	got := Calibrate(samples)
	if !got.Calibrated {
		t.Fatalf("expected a calibrated fit, got %+v", got)
	}
	// The overhead belongs in the intercept, not folded into the ratio.
	if math.Abs(got.PerCallOverhead-200) > 20 {
		t.Errorf("per-call overhead = %.1f, want ~200", got.PerCallOverhead)
	}
	if math.Abs(got.BytesPerToken-ratio) > 0.2 {
		t.Errorf("bytes per token = %.3f, want ~%.1f", got.BytesPerToken, ratio)
	}
}

func TestConstantResultSizesCannotBeCalibrated(t *testing.T) {
	// With identical sizes, a fixed overhead and a smaller byte ratio explain
	// the data equally well. Reporting a confident fit here would be wrong.
	var samples []Sample
	for i := 0; i < 10; i++ {
		samples = append(samples, Sample{PromptGrowth: 771, OutputTokens: 100, ResultBytes: 2000})
	}
	got := Calibrate(samples)
	if got.Calibrated {
		t.Fatal("constant result sizes must not yield a calibrated fit")
	}
}

func TestCalibrationRefusesInsufficientOrImplausibleData(t *testing.T) {
	thin := Calibrate([]Sample{{PromptGrowth: 100, OutputTokens: 10, ResultBytes: 300}})
	if thin.Calibrated || thin.BytesPerToken != FallbackRatio {
		t.Fatal("a single sample must not produce a calibrated fit")
	}

	// Growth far larger than the content can explain implies the growth came
	// from somewhere else; accepting it would give an absurd ratio.
	var wild []Sample
	for i := 0; i < 10; i++ {
		wild = append(wild, Sample{PromptGrowth: 1_000_000, OutputTokens: 0, ResultBytes: 100})
	}
	if got := Calibrate(wild); got.Calibrated {
		t.Fatalf("an implausible fit must be rejected, got %.3f bytes/token", got.BytesPerToken)
	}
}

func TestEstimatesAreNeverLabelledAsMeasurements(t *testing.T) {
	if got := (Ratio{BytesPerToken: 3.5}).Provenance(); got != model.DerivedApprox {
		t.Fatalf("an estimated token count must be derived-approx, got %q", got)
	}
}

func TestZeroRatioStillCounts(t *testing.T) {
	// A zero-value Ratio must not divide by zero.
	if got := (Ratio{}).Count(360); math.Abs(got-100) > 0.001 {
		t.Fatalf("expected the fallback ratio to apply, got %v", got)
	}
}

func TestHashIsStableAndDistinguishes(t *testing.T) {
	// Two equal strings built differently, rather than the same literal
	// twice: the property is that equal content hashes equally, and writing
	// it as Hash("abc") != Hash("abc") is an expression a linter is right to
	// call meaningless.
	assembled := "ab" + string(rune('c'))
	if Hash("abc") != Hash(assembled) {
		t.Fatal("hashing must be stable, or repeated-retrieval detection breaks")
	}
	if Hash("abc") == Hash("abd") {
		t.Fatal("different content must hash differently")
	}
}
