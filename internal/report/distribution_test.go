package report

import "testing"

func TestPercentileIsNearestRankSoEveryFigureIsARetrievalThatHappened(t *testing.T) {
	// 1..10. An interpolated p95 would be 9.5, which no retrieval ever cost.
	s := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	for _, c := range []struct {
		p    float64
		want float64
	}{{0.5, 5}, {0.95, 10}, {0, 1}, {1, 10}} {
		if got := percentile(s, c.p); got != c.want {
			t.Errorf("p%v = %v, want %v", c.p*100, got, c.want)
		}
	}
	if got := percentile(nil, 0.5); got != 0 {
		t.Errorf("percentile of nothing = %v, want 0", got)
	}
}

func TestDistributionDeclinesPercentilesOnTooFewRetrievals(t *testing.T) {
	// Fabricated precision is the failure mode this threshold exists for: a
	// p95 over nine samples is the ninth sample, dressed up.
	var few []float64
	for i := 0; i < PercentilesNeedAtLeast-1; i++ {
		few = append(few, float64(i))
	}
	n := &Node{samples: few}
	setDistribution(n)
	if n.MaxPerRetrieval != float64(PercentilesNeedAtLeast-2) {
		t.Errorf("max = %v, want the largest sample", n.MaxPerRetrieval)
	}
	if n.P50PerRetrieval != 0 || n.P95PerRetrieval != 0 {
		t.Errorf("percentiles over %d samples must be declined, got %v and %v",
			len(few), n.P50PerRetrieval, n.P95PerRetrieval)
	}

	enough := append(few, 100)
	n = &Node{samples: enough}
	setDistribution(n)
	if n.P50PerRetrieval == 0 || n.P95PerRetrieval == 0 {
		t.Errorf("percentiles over %d samples should be reported", len(enough))
	}
	if n.MaxPerRetrieval != 100 {
		t.Errorf("max = %v, want 100", n.MaxPerRetrieval)
	}
}

// The distinction the column exists to draw, on two leaves with the same
// total cost and the same retrieval count.
func TestDistributionSeparatesHabituallyExpensiveFromOneBadEvent(t *testing.T) {
	var uniform, spiky []float64
	for i := 0; i < 20; i++ {
		uniform = append(uniform, 100)
		spiky = append(spiky, 5)
	}
	spiky[19] = 1905 // same total, one event

	u, s := &Node{samples: uniform}, &Node{samples: spiky}
	setDistribution(u)
	setDistribution(s)

	if u.MaxPerRetrieval != u.P95PerRetrieval {
		t.Errorf("a uniform leaf should have its max at its p95, got %v against %v",
			u.MaxPerRetrieval, u.P95PerRetrieval)
	}
	if s.MaxPerRetrieval <= 10*s.P95PerRetrieval {
		t.Errorf("a leaf with one bad event should have its max far above its p95, "+
			"got %v against %v", s.MaxPerRetrieval, s.P95PerRetrieval)
	}
	// The mean cannot tell them apart, which is the reason for the column.
	if sum(uniform)/20 != sum(spiky)/20 {
		t.Fatal("the fixture is wrong: the two leaves must have the same mean")
	}
}

func sum(xs []float64) float64 {
	var t float64
	for _, x := range xs {
		t += x
	}
	return t
}

func TestDistributionRollsUpFromTheLeavesAndSurvivesAMerge(t *testing.T) {
	leaf := func(samples ...float64) *Node {
		n := &Node{Name: "f.go", Kind: "item", Tokens: 10, Items: len(samples)}
		for _, s := range samples {
			n.Carry += s
			n.samples = append(n.samples, s)
		}
		return n
	}
	tree := func(samples ...float64) *Node {
		return &Node{Name: "session", Kind: "root",
			Children: []*Node{{Name: "file content", Kind: "mechanism",
				Children: []*Node{leaf(samples...)}}}}
	}

	a := tree(1, 2, 3, 4, 5)
	rollUp(a)
	if a.MaxPerRetrieval != 5 {
		t.Errorf("the root's max is %v, want the worst leaf's 5", a.MaxPerRetrieval)
	}
	if a.P95PerRetrieval != 0 {
		t.Error("five samples is not a distribution")
	}

	// Merged across sessions, the worst retrieval of the set is still the
	// worst retrieval of the set.
	merged := MergeTrees([]*Node{tree(1, 2, 3, 4, 5), tree(6, 7, 8, 9, 400)})
	if merged.MaxPerRetrieval != 400 {
		t.Errorf("the merged max is %v, want 400", merged.MaxPerRetrieval)
	}
	if merged.P50PerRetrieval == 0 {
		t.Error("ten samples across two sessions is a distribution")
	}
}
