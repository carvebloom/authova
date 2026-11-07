package main

import "testing"

func TestHistogramBoundaries(t *testing.T) {
	var histogram histogram
	for _, value := range []int64{0, 100, 999900, 1000000, 1000001, 119999999, 120000000, 120000001} {
		histogram.observe(value)
	}
	if histogram.count != 8 || histogram.overflow != 1 {
		t.Fatal("counts lost at bin boundaries")
	}
	if median := histogram.percentile(.5); median == nil || *median != 1000 {
		t.Fatal("unexpected median")
	}
	if histogram.percentile(.99) != nil {
		t.Fatal("overflow was silently clipped into a valid quantile")
	}
}
