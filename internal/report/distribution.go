package report

import "sort"

// PercentilesNeedAtLeast is how many retrievals a node needs before its
// percentiles are printed.
//
// A p95 over four samples is the fourth sample with a decimal point on it.
// Below this the maximum is reported alone: it is an observation about one
// retrieval and needs no distribution behind it, where a percentile claims
// something about a population that is not there.
const PercentilesNeedAtLeast = 10

// setDistribution fills in what one retrieval cost here, as a distribution
// rather than as a mean.
//
// A total and a count give a reader a mean and nothing else, and the mean is
// the one statistic that cannot tell the two interesting cases apart. A leaf
// whose maximum sits near its p95 is uniformly expensive, and the answer is a
// policy change -- this command is verbose every time it runs. A leaf whose
// maximum is far above its p95 had one bad event in it, and the answer is a
// fix. Averaged into a category, the second is invisible, which is why a
// third level of naming would not have helped: it only gives you a smaller
// category to average it into.
//
// Priced as billed, like every other cost here: one sample is one retrieval's
// CarryEIT.
func setDistribution(n *Node) {
	if len(n.samples) == 0 {
		return
	}
	s := append([]float64(nil), n.samples...)
	sort.Float64s(s)
	n.MaxPerRetrieval = s[len(s)-1]
	if len(s) < PercentilesNeedAtLeast {
		n.P50PerRetrieval, n.P95PerRetrieval = 0, 0
		return
	}
	n.P50PerRetrieval = percentile(s, 0.5)
	n.P95PerRetrieval = percentile(s, 0.95)
}

// percentile is nearest-rank over a sorted sample: the smallest value at or
// below which that fraction of the retrievals sit.
//
// Nearest-rank rather than interpolated, because every sample here is a
// retrieval that really cost that much. An interpolated p95 is a number no
// retrieval ever was, which is the wrong kind of precision to print beside
// observations.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(p*float64(len(sorted))+0.999999) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

// perRetrievalStr renders the distribution as one column: p50/p95/max.
//
// One column rather than three, because a level has up to forty rows and the
// three numbers are read together or not at all. A dash where a percentile
// was not computed says that it was declined rather than that it is zero.
func perRetrievalStr(n TreeNode) string {
	if n.MaxPerRetrieval == 0 {
		return ""
	}
	if n.P50PerRetrieval == 0 {
		return "-/-/" + num(int(n.MaxPerRetrieval))
	}
	return num(int(n.P50PerRetrieval)) + "/" + num(int(n.P95PerRetrieval)) +
		"/" + num(int(n.MaxPerRetrieval))
}
