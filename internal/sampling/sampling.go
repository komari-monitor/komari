// Package sampling provides deterministic proportional downsampling used by
// history responses. It is independent of transport and storage.
package sampling

import (
	"math"
	"sort"
)

// Group is one named set of candidate points.
type Group[K comparable] struct {
	Key    K
	Length int
}

type remainder struct {
	idx  int
	frac float64
}

// AllocateTargets splits maxTotal across groups proportionally.
func AllocateTargets[K comparable](groups []Group[K], maxTotal int) map[K]int {
	result := make(map[K]int, len(groups))
	if maxTotal < 0 {
		for _, g := range groups {
			result[g.Key] = g.Length
		}
		return result
	}
	total := 0
	for _, g := range groups {
		total += g.Length
	}
	if total <= maxTotal {
		for _, g := range groups {
			result[g.Key] = g.Length
		}
		return result
	}
	remainders := make([]remainder, 0, len(groups))
	sumTargets := 0
	for i, g := range groups {
		if g.Length <= 0 {
			result[g.Key] = 0
			continue
		}
		raw := float64(g.Length) * float64(maxTotal) / float64(total)
		t := int(math.Floor(raw))
		if t > g.Length {
			t = g.Length
		}
		result[groups[i].Key] = t
		sumTargets += t
		remainders = append(remainders, remainder{i, raw - float64(t)})
	}
	if sumTargets < maxTotal {
		need := maxTotal - sumTargets
		sortRemainders(remainders, false)
		for _, r := range remainders {
			if need == 0 {
				break
			}
			g := groups[r.idx]
			if result[g.Key] < g.Length {
				result[g.Key]++
				need--
			}
		}
		for need > 0 {
			for _, g := range groups {
				if need == 0 {
					break
				}
				if result[g.Key] < g.Length {
					result[g.Key]++
					need--
				}
			}
			if need > 0 {
				break
			}
		}
	} else if sumTargets > maxTotal {
		over := sumTargets - maxTotal
		sortRemainders(remainders, true)
		for _, r := range remainders {
			if over == 0 {
				break
			}
			g := groups[r.idx]
			if result[g.Key] > 0 {
				result[g.Key]--
				over--
			}
		}
		for over > 0 {
			for _, g := range groups {
				if over == 0 {
					break
				}
				if result[g.Key] > 0 {
					result[g.Key]--
					over--
				}
			}
			if over > 0 {
				break
			}
		}
	}
	return result
}

func sortRemainders(r []remainder, asc bool) {
	sort.Slice(r, func(i, j int) bool {
		if asc {
			return r[i].frac < r[j].frac
		}
		return r[i].frac > r[j].frac
	})
}

// SampleEvenly selects k points including both endpoints.
func SampleEvenly[T any](in []T, k int) []T {
	n := len(in)
	if k <= 0 || n == 0 {
		return []T{}
	}
	if k >= n {
		return in
	}
	out := make([]T, 0, k)
	if k == 1 {
		out = append(out, in[n-1])
		return out
	}
	for i := 0; i < k; i++ {
		idx := int(math.Round(float64(i) * float64(n-1) / float64(k-1)))
		if idx < 0 {
			idx = 0
		} else if idx >= n {
			idx = n - 1
		}
		out = append(out, in[idx])
	}
	return out
}
