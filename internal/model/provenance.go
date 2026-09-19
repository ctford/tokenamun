// Package model is Tokenamun's normalized domain. Adapters translate someone
// else's format into these types; every analysis and renderer consumes them.
package model

import "fmt"

// Provenance records how a number came to exist. Every reported quantity has
// one, and the renderers refuse to print a quantity that doesn't. See
// docs/METHODOLOGY.md section 1.
type Provenance string

const (
	// Observed values are present in the telemetry with no interpretation.
	Observed Provenance = "observed"
	// Derived values are deterministic arithmetic over observed values.
	Derived Provenance = "derived"
	// DerivedApprox values are deterministic but use a stated estimator.
	DerivedApprox Provenance = "derived-approx"
	// Inferred values are a classifier's opinion and could be wrong.
	Inferred Provenance = "inferred"
	// Counterfactual values describe a session that never happened.
	Counterfactual Provenance = "counterfactual"
	// Given values came from the caller. Not a measurement and not arithmetic
	// over one: the figure in `optimise --optimise` is the only quantity this
	// tool reports that it did not produce, and labelling it anything else
	// would lend it the authority of the numbers around it.
	Given Provenance = "given"
)

// Unit names what a Quantity counts. Mixing units is the other way to mislead,
// so they are explicit: raw tokens and cache-weighted cost are not the same
// quantity and must never be added.
type Unit string

const (
	Tokens  Unit = "tokens" // raw token count: volume, not cost
	EIT     Unit = "eit"    // effective input-equivalent tokens: cost
	Bytes   Unit = "bytes"
	Calls   Unit = "calls"
	Ratio   Unit = "ratio"
	Seconds Unit = "seconds"
	// USD is the one unit here that a session does not contain. Tokens are
	// counted; dollars are tokens multiplied by a third-party price list,
	// so a quantity in USD is only as good as the catalog pin printed
	// beside it, and something must print that pin.
	//
	// A USD quantity is nonetheless Derived and not DerivedApprox, which
	// looks like the more modest label and is the wrong one. Nothing in
	// the arithmetic is estimated: the tokens are observed and each call
	// converts at its own model's rate. What can be wrong is the published
	// rate, and that is staleness, not approximation. DerivedApprox
	// promises a different caveat -- an estimator whose error you can
	// reason about, like the byte-ratio token count -- so using it here
	// would invite a reader to reason about an error bar that does not
	// exist. The pin is the honest mitigation, which is why it is
	// mandatory rather than advisory.
	USD Unit = "usd"
)

// Quantity is a number that knows where it came from and what it counts.
type Quantity struct {
	Value float64    `json:"value"`
	Unit  Unit       `json:"unit"`
	Prov  Provenance `json:"provenance"`
}

// Obs returns an observed quantity.
func Obs(v float64, u Unit) Quantity { return Quantity{v, u, Observed} }

// Der returns a derived quantity.
func Der(v float64, u Unit) Quantity { return Quantity{v, u, Derived} }

// Validate reports whether the quantity is fit to render. An unlabelled or
// unitless number is a bug, not a formatting problem.
func (q Quantity) Validate() error {
	if q.Prov == "" {
		return fmt.Errorf("quantity %v has no provenance", q.Value)
	}
	if q.Unit == "" {
		return fmt.Errorf("quantity %v has no unit", q.Value)
	}
	return nil
}
