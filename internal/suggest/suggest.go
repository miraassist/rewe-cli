// Package suggest ranks previously bought products to fill up a basket.
// It is a port of the Haskell suggestionEngine (whose properties are proven in leanKorb/).
package suggest

import (
	"sort"

	"github.com/miraassist/rewe-cli/internal/rewe"
)

// Suggestion is a product with the number of past orders it appeared in.
type Suggestion struct {
	Product rewe.Product `json:"product"`
	Freq    int          `json:"freq"`
}

// Engine returns at most n purchasable products that were ordered before and are
// not in the basket, sorted by order frequency (descending, stable).
//
//   - orderedProductIDs: product IDs from past orders (one entry per order line)
//   - purchasable: products currently purchasable that the customer bought before
//   - basketProductIDs: products already in the basket
func Engine(orderedProductIDs []string, purchasable []rewe.Product, basketProductIDs []string, n int) []Suggestion {
	freq := make(map[string]int, len(orderedProductIDs))
	for _, id := range orderedProductIDs {
		freq[id]++
	}
	inBasket := make(map[string]bool, len(basketProductIDs))
	for _, id := range basketProductIDs {
		inBasket[id] = true
	}
	out := []Suggestion{}
	for _, p := range purchasable {
		f, ok := freq[p.ProductID]
		if !ok || inBasket[p.ProductID] {
			continue
		}
		out = append(out, Suggestion{Product: p, Freq: f})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Freq > out[j].Freq })
	if n < 0 {
		n = 0
	}
	if len(out) > n {
		out = out[:n]
	}
	return out
}
