package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/miraassist/rewe-cli/internal/rewe"
	"github.com/miraassist/rewe-cli/internal/suggest"
)

// basket_fill: a shopping list into the basket in one call. Which product an
// entry becomes is deterministic, so korb decides it instead of the model
// spending a tool round per lookup: the favorites first, then what was
// bought most often, then the catalogue. What is not clear is answered with
// options for the model to ask about.

type fillEntry struct {
	Entry    string `json:"entry"`
	Query    string `json:"query"`
	Quantity int    `json:"quantity"`
}

type fillAdded struct {
	Entry    string `json:"entry"`
	Title    string `json:"title"`
	Quantity int    `json:"quantity"`
	Price    string `json:"price"`
	// Source: favorite, history or search.
	Source string `json:"source"`
}

type fillUnclear struct {
	Entry   string        `json:"entry"`
	Options []productView `json:"options"`
}

type fillResult struct {
	Added    []fillAdded   `json:"added"`
	Unclear  []fillUnclear `json:"unclear,omitempty"`
	NotFound []string      `json:"notFound,omitempty"`
	Basket   basketView    `json:"basket"`
}

// fillChoice is what an entry became before it goes into the basket.
type fillChoice struct {
	product rewe.Product
	source  string
	options []rewe.Product
}

func (ap *app) mcpBasketFill(raw json.RawMessage) (any, error) {
	var a struct{ Entries []fillEntry }
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if len(a.Entries) == 0 {
		return nil, errors.New("entries is required: one per item of the shopping list")
	}
	api, mod, err := ap.basketAPI()
	if err != nil {
		return nil, err
	}
	lists, err := api.FavoriteLists(ap.ctx)
	if err != nil {
		return nil, err
	}
	var favorites []rewe.Product
	for _, l := range lists {
		favorites = append(favorites, l.Items...)
	}
	// The order history is read only when an entry is no favorite.
	var history []suggest.Suggestion
	historyRead := false
	readHistory := func() error {
		if historyRead {
			return nil
		}
		historyRead = true
		ids, err := orderedProductIDs(ap.ctx, api)
		if err != nil {
			return err
		}
		purchased, err := api.PurchasedProducts(ap.ctx)
		if err != nil {
			return err
		}
		history = suggest.Engine(ids, purchased, nil, len(purchased))
		return nil
	}

	res := fillResult{Added: []fillAdded{}}
	type pick struct {
		entry    fillEntry
		product  rewe.Product
		source   string
		quantity int
	}
	var picks []pick
	for _, e := range a.Entries {
		if e.Query = strings.TrimSpace(e.Query); e.Query == "" {
			e.Query = e.Entry
		}
		if e.Entry == "" {
			e.Entry = e.Query
		}
		if e.Quantity <= 0 {
			e.Quantity = 1
		}
		c, err := ap.fillChoose(api, e, favorites, readHistory, &history)
		if err != nil {
			return nil, err
		}
		switch {
		case c.source != "":
			picks = append(picks, pick{e, c.product, c.source, e.Quantity})
		case len(c.options) > 0:
			u := fillUnclear{Entry: e.Entry}
			for _, p := range c.options {
				u.Options = append(u.Options, viewProduct(p))
			}
			res.Unclear = append(res.Unclear, u)
		default:
			res.NotFound = append(res.NotFound, e.Entry)
		}
	}
	// Two entries of the same product add up.
	qty := map[string]int{}
	for _, p := range picks {
		qty[p.product.Listing.ListingID] += p.quantity
	}
	var b *rewe.Basket
	set := map[string]bool{}
	for _, p := range picks {
		id := p.product.Listing.ListingID
		if !set[id] {
			set[id] = true
			if b, err = ap.setQuantity(api, mod, id, qty[id]); err != nil {
				return nil, fmt.Errorf("%s: %w", p.entry.Entry, err)
			}
		}
		if b != nil && !slices.ContainsFunc(b.LineItems, func(li rewe.LineItem) bool { return li.Product.Listing.ListingID == id }) {
			res.NotFound = append(res.NotFound, p.entry.Entry+" (not available at this service)")
			continue
		}
		res.Added = append(res.Added, fillAdded{Entry: p.entry.Entry, Title: p.product.Title, Quantity: p.quantity,
			Price: euro(p.product.Listing.CurrentRetailPrice), Source: p.source})
	}
	if b == nil {
		if b, err = ap.currentBasket(api, mod); err != nil {
			return nil, err
		}
	}
	res.Basket = viewBasket(b, mod)
	return res, nil
}

// fillChoose decides an entry: the favorites, the order history, the
// catalogue, in this order; the first that has the query decides.
func (ap *app) fillChoose(api *rewe.API, e fillEntry, favorites []rewe.Product, readHistory func() error, history *[]suggest.Suggestion) (fillChoice, error) {
	// Favorites: one that has the query is taken; of several the one whose
	// title has the most words of the entry, else the one bought most often.
	var favs []rewe.Product
	for _, p := range favorites {
		if hasWord(p.Title, e.Query) {
			favs = append(favs, p)
		}
	}
	if len(favs) > 0 {
		if p, ok := bestMatch(favs, e, func(p rewe.Product) int { return timesBought(p, readHistory, history) }); ok {
			return fillChoice{product: p, source: "favorite"}, nil
		}
		return fillChoice{options: favs[:min(len(favs), 3)]}, nil
	}
	// Order history: the one bought most often.
	if err := readHistory(); err != nil {
		return fillChoice{}, err
	}
	var bought []rewe.Product
	for _, s := range *history {
		if hasWord(s.Product.Title, e.Query) {
			bought = append(bought, s.Product)
		}
	}
	if len(bought) > 0 {
		if p, ok := bestMatch(bought, e, func(p rewe.Product) int { return timesBought(p, readHistory, history) }); ok {
			return fillChoice{product: p, source: "history"}, nil
		}
		return fillChoice{options: bought[:min(len(bought), 3)]}, nil
	}
	// The catalogue: the first hit that has the query; hits without it are
	// only options.
	hits, err := api.Search(ap.ctx, e.Query, nil, nil, 1)
	if err != nil {
		return fillChoice{}, err
	}
	for _, p := range hits {
		if hasWord(p.Title, e.Query) {
			return fillChoice{product: p, source: "search"}, nil
		}
	}
	return fillChoice{options: hits[:min(len(hits), 3)]}, nil
}

// bestMatch picks of candidates the one whose title has the most words of
// the entry, then the one bought most often; a tie is no match.
func bestMatch(cands []rewe.Product, e fillEntry, bought func(rewe.Product) int) (rewe.Product, bool) {
	if len(cands) == 1 {
		return cands[0], true
	}
	words := entryWords(e.Entry)
	score := func(p rewe.Product) [2]int {
		n := 0
		for _, w := range words {
			if hasWord(p.Title, w) {
				n++
			}
		}
		return [2]int{n, bought(p)}
	}
	best, tie := cands[0], false
	bs := score(best)
	for _, p := range cands[1:] {
		s := score(p)
		switch {
		case s[0] > bs[0] || (s[0] == bs[0] && s[1] > bs[1]):
			best, bs, tie = p, s, false
		case s == bs:
			tie = true
		}
	}
	return best, !tie
}

func timesBought(p rewe.Product, readHistory func() error, history *[]suggest.Suggestion) int {
	if readHistory() != nil {
		return 0
	}
	for _, s := range *history {
		if s.Product.ProductID == p.ProductID {
			return s.Freq
		}
	}
	return 0
}

// hasWord: the title contains the word, ignoring case ("milch" is in
// "Schwarzwaldmilch").
func hasWord(title, word string) bool {
	return word != "" && strings.Contains(strings.ToLower(title), strings.ToLower(strings.TrimSpace(word)))
}

// entryWords are the words of an entry worth comparing: no numbers, no
// short words, no units ("7 Packungen Kaffee" → Kaffee).
func entryWords(entry string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(entry, func(r rune) bool { return !unicode.IsLetter(r) && r != '!' }) {
		lw := strings.ToLower(w)
		if len([]rune(lw)) < 3 || slices.Contains(fillUnits, lw) {
			continue
		}
		out = append(out, w)
	}
	return out
}

var fillUnits = []string{"packung", "packungen", "pck", "stück", "stk", "flasche", "flaschen", "dose", "dosen", "glas", "gläser",
	"liter", "kilo", "gramm", "beutel", "becher", "tüte", "tüten", "kiste", "kisten", "bund", "netz", "und", "oder", "bitte"}
