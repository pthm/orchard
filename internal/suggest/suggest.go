// Package suggest finds the closest name to a misspelt one, for
// "did you mean" hints in errors.
package suggest

import (
	"fmt"
	"strings"
)

// Hint returns ` (did you mean "x"?)` for the closest candidate within
// a small edit distance, or "".
func Hint(name string, candidates []string) string {
	best, bestDist := "", 0
	for _, c := range candidates {
		d := editDistance(strings.ToLower(name), strings.ToLower(c))
		if best == "" || d < bestDist {
			best, bestDist = c, d
		}
	}
	if best == "" || bestDist > max(2, len(name)/3) {
		return ""
	}
	return fmt.Sprintf(" (did you mean %q?)", best)
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
