package streaming

import "math/rand/v2"

// newCycleRNG builds a deterministic generator for one cycle, so a recorded
// order can be reproduced during recovery instead of reshuffled (FR-04).
//
// PCG is seeded from the session id, the cycle number and the stored item
// identity: same inputs, same order.
func newCycleRNG(sessionID string, cycle int, items []PlaylistItem) *rand.Rand {
	var sum uint64 = 1469598103934665603
	mix := func(s string) {
		for i := 0; i < len(s); i++ {
			sum ^= uint64(s[i])
			sum *= 1099511628211
		}
	}
	mix(sessionID)
	mix(itoa(cycle))
	for _, item := range items {
		mix(item.MediaID)
	}
	// Two words feed PCG, so derive a second from the first.
	seed2 := sum*6364136223846793005 + 1442695040888963407
	return rand.New(rand.NewPCG(sum, seed2))
}

// OrderForCycle produces the item order for one pass through the playlist.
//
// The three modes differ in whether repeats are allowed within a cycle (FR-04):
//   - sequential: the stored order, unchanged
//   - random: N independent draws, so a video may repeat
//   - random_no_repeat: every video exactly once, order shuffled
func OrderForCycle(items []PlaylistItem, mode string, cycle int, rng *rand.Rand) []PlaylistItem {
	n := len(items)
	if n == 0 {
		return nil
	}
	switch mode {
	case ModeSequential:
		out := make([]PlaylistItem, n)
		copy(out, items)
		return out

	case ModeRandom:
		// N draws with replacement: exactly N items per cycle (AC-24).
		out := make([]PlaylistItem, 0, n)
		for range n {
			out = append(out, items[rng.IntN(n)])
		}
		return out

	default: // ModeRandomNoRepeat
		out := make([]PlaylistItem, n)
		copy(out, items)
		rng.Shuffle(n, func(i, j int) {
			out[i], out[j] = out[j], out[i]
		})
		return out
	}
}

// avoidRepeatBoundary reorders a freshly shuffled cycle so its first item is not
// the previous cycle's last item. With N > 1, not rolling a full shuffle avoids
// a visible repeat of the same file at the seam (AC-24).
//
// A single swap suffices: for n >= 2 the first and last positions are distinct,
// so swapping keeps the cycle a true permutation with every video exactly once.
func avoidRepeatBoundary(cycle []PlaylistItem, previousLast string) []PlaylistItem {
	n := len(cycle)
	if n < 2 || previousLast == "" {
		return cycle
	}
	if cycle[0].MediaID != previousLast {
		return cycle
	}
	cycle[0], cycle[n-1] = cycle[n-1], cycle[0]
	return cycle
}

// itoa is a tiny helper so the FNV hash above avoids pulling in strconv.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
