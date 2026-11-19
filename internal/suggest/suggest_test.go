package suggest

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/miraassist/rewe-cli/internal/rewe"
)

func prod(id string) rewe.Product { return rewe.Product{ProductID: id, Title: "p" + id} }

func TestEngineExample(t *testing.T) {
	ordered := []string{"a", "b", "a", "c", "a", "b", "x"}
	purchasable := []rewe.Product{prod("c"), prod("b"), prod("a"), prod("d")}
	got := Engine(ordered, purchasable, []string{"b"}, 5)
	want := []Suggestion{{prod("a"), 3}, {prod("c"), 1}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestEngineStableTies(t *testing.T) {
	got := Engine([]string{"a", "b", "c"}, []rewe.Product{prod("c"), prod("a"), prod("b")}, nil, 2)
	if got[0].Product.ProductID != "c" || got[1].Product.ProductID != "a" {
		t.Fatalf("ties must keep input order, got %v", got)
	}
}

func TestEngineNonNilAndNegativeN(t *testing.T) {
	if got := Engine(nil, nil, nil, 3); got == nil || len(got) != 0 {
		t.Fatalf("want empty non-nil slice, got %#v", got)
	}
	if got := Engine([]string{"a"}, []rewe.Product{prod("a")}, nil, -1); len(got) != 0 {
		t.Fatalf("negative n must yield nothing, got %v", got)
	}
}

// The five properties proven in leanKorb/, checked on random inputs.
func TestEngineProperties(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	ids := []string{"a", "b", "c", "d", "e", "f", "g"}
	pick := func(n int) []string {
		s := make([]string, n)
		for i := range s {
			s[i] = ids[r.IntN(len(ids))]
		}
		return s
	}
	for i := 0; i < 2000; i++ {
		ordered, basket := pick(r.IntN(20)), pick(r.IntN(4))
		var purchasable []rewe.Product
		for _, id := range pick(r.IntN(8)) {
			purchasable = append(purchasable, prod(id))
		}
		n := r.IntN(6)
		got := Engine(ordered, purchasable, basket, n)

		if len(got) > n {
			t.Fatalf("count limit violated: %d > %d", len(got), n)
		}
		for j, s := range got {
			if s.Freq <= 0 {
				t.Fatalf("non-positive frequency: %v", s)
			}
			if j > 0 && got[j-1].Freq < s.Freq {
				t.Fatalf("not sorted descending: %v", got)
			}
			if !slices.Contains(ordered, s.Product.ProductID) {
				t.Fatalf("suggested product never ordered: %v", s)
			}
			if !slices.ContainsFunc(purchasable, func(p rewe.Product) bool { return p.ProductID == s.Product.ProductID }) {
				t.Fatalf("suggested product not purchasable: %v", s)
			}
			if slices.Contains(basket, s.Product.ProductID) {
				t.Fatalf("suggested product already in basket: %v", s)
			}
		}
	}
}
