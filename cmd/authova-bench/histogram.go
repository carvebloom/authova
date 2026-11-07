package main

import "math"

// Latencies use bounded storage: 0.1ms bins through 1s, then 1ms through 120s.
// Overflow is explicit; quantiles in overflow are unknown rather than clipped.
type histogram struct {
	bins            [129001]uint64
	count, overflow uint64
	maxMicros       int64
}

func (h *histogram) observe(micros int64) {
	if micros < 0 {
		micros = 0
	}
	h.count++
	if micros > h.maxMicros {
		h.maxMicros = micros
	}
	if micros > 120000000 {
		h.overflow++
		return
	}
	index := (micros + 99) / 100
	if micros > 1000000 {
		index = 10000 + (micros-1000000+999)/1000
	}
	h.bins[index]++
}

func (h *histogram) percentile(percent float64) *float64 {
	if h.count == 0 {
		return nil
	}
	rank := uint64(math.Ceil(percent * float64(h.count)))
	seen := uint64(0)
	for index, count := range h.bins {
		seen += count
		if seen >= rank {
			micros := int64(index * 100)
			if index > 10000 {
				micros = 1000000 + int64(index-10000)*1000
			}
			value := float64(min(micros, h.maxMicros)) / 1000
			return &value
		}
	}
	return nil
}

func (h *histogram) summary() map[string]any {
	return map[string]any{"p50": h.percentile(.5), "p95": h.percentile(.95), "p99": h.percentile(.99), "max": float64(h.maxMicros) / 1000, "overflow_above_120s": h.overflow}
}
