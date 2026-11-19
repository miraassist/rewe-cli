package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/miraassist/rewe-cli/internal/fakerewe"
)

// mcpClient talks to korb's MCP server in-process over pipes.
type mcpClient struct {
	t    *testing.T
	in   *io.PipeWriter
	out  *bufio.Reader
	next int
}

func startMCP(t *testing.T) *mcpClient {
	t.Helper()
	a, err := parseArgs([]string{"mcp"}, allBoolFlags, allValueFlags)
	if err != nil {
		t.Fatal(err)
	}
	ap, err := newApp(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	ap.mcp = true
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- ap.serveMCP(inR, outW); outW.Close() }()
	t.Cleanup(func() {
		inW.Close()
		if err := <-done; err != nil {
			t.Errorf("serveMCP: %v", err)
		}
	})
	c := &mcpClient{t: t, in: inW, out: bufio.NewReader(outR)}
	init := c.rpc("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "test", "version": "0"}})
	if init["protocolVersion"] != "2025-06-18" {
		t.Fatalf("initialize: %v", init)
	}
	c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	return c
}

func (c *mcpClient) send(v any) {
	c.t.Helper()
	b, _ := json.Marshal(v)
	if _, err := c.in.Write(append(b, '\n')); err != nil {
		c.t.Fatal(err)
	}
}

func (c *mcpClient) rpc(method string, params any) map[string]any {
	c.t.Helper()
	c.next++
	c.send(map[string]any{"jsonrpc": "2.0", "id": c.next, "method": method, "params": params})
	line, err := c.out.ReadBytes('\n')
	if err != nil {
		c.t.Fatal(err)
	}
	var resp struct {
		ID     int             `json:"id"`
		Result map[string]any  `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil || resp.ID != c.next || resp.Error != nil {
		c.t.Fatalf("%s: %s (%v)", method, line, err)
	}
	return resp.Result
}

// call runs a tool and returns its decoded answer and whether it is an error.
func (c *mcpClient) call(tool string, args map[string]any) (any, bool) {
	c.t.Helper()
	r := c.rpc("tools/call", map[string]any{"name": tool, "arguments": args})
	text := r["content"].([]any)[0].(map[string]any)["text"].(string)
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		c.t.Fatalf("%s: %s", tool, text)
	}
	return v, r["isError"] == true
}

func (c *mcpClient) ok(tool string, args map[string]any) any {
	c.t.Helper()
	v, isErr := c.call(tool, args)
	if isErr {
		c.t.Fatalf("%s %v: %v", tool, args, v)
	}
	return v
}

func (c *mcpClient) fails(tool string, args map[string]any, contains string) {
	c.t.Helper()
	v, isErr := c.call(tool, args)
	if !isErr || !strings.Contains(v.(map[string]any)["error"].(string), contains) {
		c.t.Fatalf("%s %v: want error with %q, got %v", tool, args, contains, v)
	}
}

func obj(v any) map[string]any { return v.(map[string]any) }
func arr(v any) []any          { return v.([]any) }

// fillToMinimum puts the favorite milk and enough coffee into the basket to
// reach the minimum order value and returns the basket.
func fillToMinimum(c *mcpClient) map[string]any {
	milk := obj(arr(c.ok("favorites_search", map[string]any{"query": "milch"}))[0])
	c.ok("basket_set", map[string]any{"listingId": milk["listingId"], "quantity": 2})
	coffee := obj(arr(c.ok("product_search", map[string]any{"query": "Tchibo"}))[0])
	return obj(c.ok("basket_set", map[string]any{"listingId": coffee["listingId"], "quantity": 7}))
}

func thursdaySlot(c *mcpClient) map[string]any {
	c.t.Helper()
	for _, s := range arr(c.ok("slots", nil)) {
		if s := obj(s); s["free"] == true {
			return s
		}
	}
	c.t.Fatal("no free slot")
	return nil
}

func TestRW01_ToolsAreListed(t *testing.T) {
	setupFake(t)
	c := startMCP(t)
	var names []string
	for _, tool := range arr(c.rpc("tools/list", map[string]any{})["tools"]) {
		names = append(names, obj(tool)["name"].(string))
	}
	want := []string{"status", "favorites_search", "frequent", "product_search", "basket_fill", "basket", "basket_set", "slots", "checkout_prepare",
		"order", "orders", "modification_start", "modification_submit", "modification_cancel", "order_cancel",
		"login_start", "login_complete", "store_search", "store_set"}
	if !slices.Equal(names, want) {
		t.Fatalf("tools: %v", names)
	}
}

func TestRW02_StatusNamesWhatIsMissing(t *testing.T) {
	fake := setupFake(t)
	c := startMCP(t)
	// Neither: both are named, the login first.
	if st := obj(c.ok("status", nil)); st["ready"] != false || len(arr(st["missing"])) != 2 ||
		!strings.Contains(arr(st["missing"])[1].(string), "korb store set") {
		t.Fatalf("no store: %v", st)
	}
	mustKorb(t, "store", "set", fakerewe.DeliveryMarketID, "12345")
	if st := obj(c.ok("status", nil)); st["loggedIn"] != false || !strings.Contains(arr(st["missing"])[0].(string), "korb login") {
		t.Fatalf("not logged in: %v", st)
	}
	login(t)
	st := obj(c.ok("status", nil))
	if st["ready"] != true || st["paymentChecked"] != false {
		t.Fatalf("empty basket: %v", st)
	}
	fillToMinimum(c)
	st = obj(c.ok("status", nil))
	if st["ready"] != true || st["paymentMethod"] != "PAYPAL" || st["deliveryAddress"] == nil {
		t.Fatalf("ready: %v", st)
	}
	fake.SetSavedPayment(false)
	st = obj(c.ok("status", nil))
	if st["ready"] != false || !strings.Contains(arr(st["missing"])[0].(string), "payment method") {
		t.Fatalf("no saved payment: %v", st)
	}
}

func setupShop(t *testing.T) *mcpClient {
	setupFake(t)
	mustKorb(t, "store", "set", fakerewe.DeliveryMarketID, "12345")
	login(t)
	return startMCP(t)
}

func TestRW03_OrderNeedsTheTotalThatWasSaid(t *testing.T) {
	c := setupShop(t)
	fillToMinimum(c)
	co := obj(c.ok("checkout_prepare", map[string]any{"slotId": thursdaySlot(c)["slotId"]}))
	if co["orderable"] != true {
		t.Fatalf("prepare: %v", co)
	}
	c.fails("order", map[string]any{"total": "1,00"}, "the total is "+co["total"].(string))
	c.fails("order", map[string]any{}, "total is required")
	if n := len(arr(c.ok("orders", nil))); n != 0 {
		t.Fatalf("ordered anyway: %d", n)
	}
	r := obj(c.ok("order", map[string]any{"total": co["total"]}))
	if r["ordered"] != true || r["orderId"] == "" {
		t.Fatalf("order: %v", r)
	}
}

func TestRW04_TheLimitHolds(t *testing.T) {
	c := setupShop(t)
	t.Setenv("KORB_MAX_TOTAL_EURO", "50")
	fillToMinimum(c)
	co := obj(c.ok("checkout_prepare", map[string]any{"slotId": thursdaySlot(c)["slotId"]}))
	if co["orderable"] != false || !strings.Contains(co["problem"].(string), "limit of 50,00 €") {
		t.Fatalf("prepare: %v", co)
	}
	c.fails("order", map[string]any{"total": co["total"]}, "limit of 50,00 €")
}

func TestRW05_AnswersAreCompact(t *testing.T) {
	c := setupShop(t)
	water := obj(arr(c.ok("favorites_search", map[string]any{"query": "Gerolsteiner"}))[0])
	b := obj(c.ok("basket_set", map[string]any{"listingId": water["listingId"], "quantity": 1}))
	if b["articleValue"] != "5,49 €" || b["deposit"] != "1,50 €" || b["missingForMinimum"] != "54,51 €" || b["minimumOrderValue"] != "60,00 €" {
		t.Fatalf("basket: %v", b)
	}
	item := obj(arr(b["items"])[0])
	if len(item) != 5 || item["title"] != "Gerolsteiner Naturell 6x1,5l" || item["price"] != "5,49 €" {
		t.Fatalf("item: %v", item)
	}
	raw, _ := json.Marshal(b)
	if strings.Contains(string(raw), "imageURL") || strings.Contains(string(raw), "staggerings") {
		t.Fatalf("raw REWE fields: %s", raw)
	}
}

// The shopping-list flow of the skill: favorites first, an order, a change
// of the order, cancelling it.
func TestMCPShoppingFlow(t *testing.T) {
	c := setupShop(t)
	if fav := arr(c.ok("favorites_search", map[string]any{"query": "milch"})); len(fav) != 1 || !strings.Contains(obj(fav[0])["title"].(string), "Schwarzwaldmilch") {
		t.Fatalf("favorite milk: %v", fav)
	}
	if n := len(arr(c.ok("favorites_search", map[string]any{"query": "tomate"}))); n != 0 {
		t.Fatalf("tomato favorite: %d", n)
	}
	freq := arr(c.ok("frequent", map[string]any{"query": "milch"}))
	if len(freq) == 0 || obj(freq[0])["timesOrdered"] != 2.0 {
		t.Fatalf("frequent: %v", freq)
	}
	if n := len(arr(c.ok("product_search", map[string]any{"query": "milch", "organic": true}))); n != 1 {
		t.Fatalf("organic milk: %d", n)
	}
	c.fails("basket_set", map[string]any{"listingId": "8-NOPE-fake-listing", "quantity": 1}, "")

	fillToMinimum(c)
	slot := thursdaySlot(c)
	co := obj(c.ok("checkout_prepare", map[string]any{"slotId": slot["slotId"]}))
	if co["slot"] != slot["slot"] || co["paymentMethod"] != "PAYPAL" || co["deliveryAddress"] == nil {
		t.Fatalf("prepare: %v", co)
	}
	c.ok("order", map[string]any{"total": co["total"]})
	orders := arr(c.ok("orders", nil))
	if len(orders) != 1 || obj(orders[0])["slot"] != slot["slot"] || obj(orders[0])["canChange"] != true {
		t.Fatalf("orders: %v", orders)
	}
	orderID := obj(orders[0])["orderId"]

	// "Pack noch Butter dazu"
	b := obj(c.ok("modification_start", nil))
	if b["modifyingOrder"] != orderID {
		t.Fatalf("start: %v", b)
	}
	c.fails("checkout_prepare", map[string]any{"slotId": slot["slotId"]}, "is being changed")
	butter := obj(arr(c.ok("favorites_search", map[string]any{"query": "butter"}))[0])
	b = obj(c.ok("basket_set", map[string]any{"listingId": butter["listingId"], "quantity": 1}))
	if b["modifyingOrder"] != orderID || len(arr(b["items"])) != 3 {
		t.Fatalf("modified basket: %v", b)
	}
	c.fails("modification_submit", map[string]any{"total": co["total"]}, "nothing was ordered")
	r := obj(c.ok("modification_submit", map[string]any{"total": b["total"]}))
	if r["orderId"] != orderID {
		t.Fatalf("submit: %v", r)
	}
	if st := obj(c.ok("status", nil)); st["modifyingOrder"] != nil {
		t.Fatalf("modification still running: %v", st)
	}

	// A change discarded, then the order cancelled.
	c.ok("modification_start", map[string]any{"orderId": orderID})
	c.ok("modification_cancel", nil)
	c.fails("modification_cancel", nil, "no order is being changed")
	c.ok("order_cancel", map[string]any{"orderId": orderID})
	if n := len(arr(c.ok("orders", nil))); n != 0 {
		t.Fatalf("order not cancelled: %d", n)
	}
}

func TestParseEuro(t *testing.T) {
	for in, want := range map[string]int{"63,40": 6340, "63.4 €": 6340, "150": 15000, " 0,05 EUR": 5, "7,5": 750} {
		if got, err := parseEuro(in); err != nil || got != want {
			t.Errorf("parseEuro(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "abc", "1,234", "1.2.3", "-5", "+5", "5,"} {
		if _, err := parseEuro(in); err == nil {
			t.Errorf("parseEuro(%q) accepted", in)
		}
	}
}

// basket_fill decides each entry as the skill used to in several rounds: the
// favorite first, then the history, then the catalogue; unclear entries come
// back with options, nothing is guessed.
func TestBasketFillTakesFavoritesHistoryThenSearch(t *testing.T) {
	c := setupShop(t)
	entry := func(e, q string, n int) map[string]any { return map[string]any{"entry": e, "query": q, "quantity": n} }
	r := obj(c.ok("basket_fill", map[string]any{"entries": []any{
		entry("Milch", "Milch", 0),
		entry("7 Packungen Kaffee", "Kaffee", 7),
		entry("Joghurt", "Joghurt", 0),
		entry("Tomaten", "Tomaten", 0),
		entry("Bio", "Bio", 0),
		entry("Drachenfrucht", "Drachenfrucht", 0),
	}}))
	got := map[string]string{}
	for _, a := range arr(r["added"]) {
		a := obj(a)
		got[a["entry"].(string)] = fmt.Sprintf("%s|%s|%v", a["source"], a["title"], a["quantity"])
	}
	for e, want := range map[string]string{
		"Milch":              "favorite|Schwarzwaldmilch Bio H-Milch 3,5% 1l|1", // not the first search hit, not the most bought
		"7 Packungen Kaffee": "favorite|Tchibo Feine Milde Kaffee ganze Bohnen 500g|7",
		"Joghurt":            "history|Elinas Joghurt Griechischer Art 500g|1",
		"Tomaten":            "search|Rispentomaten 500g|1",
	} {
		if got[e] != want {
			t.Errorf("%s: %q, want %q", e, got[e], want)
		}
	}
	if len(got) != 4 {
		t.Errorf("added %v", got)
	}
	unclear := arr(r["unclear"])
	if len(unclear) != 1 || obj(unclear[0])["entry"] != "Bio" || len(arr(obj(unclear[0])["options"])) < 2 {
		t.Errorf("unclear: %v", unclear)
	}
	if nf := arr(r["notFound"]); len(nf) != 1 || nf[0] != "Drachenfrucht" {
		t.Errorf("not found: %v", nf)
	}
	b := obj(r["basket"])
	if len(arr(b["items"])) != 4 || b["missingForMinimum"] != nil {
		t.Errorf("basket: %v", b)
	}
	if _, ok := b["next"]; ok {
		t.Error("the basket still names the next staggering, which the model took for the minimum order value")
	}

	// Filling again sets the quantities, it does not add them up.
	r = obj(c.ok("basket_fill", map[string]any{"entries": []any{entry("Kaffee", "Kaffee", 7)}}))
	for _, it := range arr(obj(r["basket"])["items"]) {
		if it := obj(it); strings.Contains(it["title"].(string), "Tchibo") && it["quantity"] != 7.0 {
			t.Errorf("coffee: %v", it)
		}
	}
	c.fails("basket_fill", map[string]any{"entries": []any{}}, "entries is required")
}

// checkout_prepare without a slot takes the next one on the weekday given.
func TestCheckoutPrepareOnAWeekday(t *testing.T) {
	c := setupShop(t)
	fillToMinimum(c)
	co := obj(c.ok("checkout_prepare", map[string]any{"weekday": "Donnerstag"}))
	if !strings.HasPrefix(co["slot"].(string), "Do ") || co["orderable"] != true {
		t.Fatalf("prepare on Thursday: %v", co)
	}
	co = obj(c.ok("checkout_prepare", nil))
	if co["slot"] != obj(arr(c.ok("slots", nil))[0])["slot"] {
		t.Fatalf("prepare without a weekday: %v", co)
	}
	c.fails("checkout_prepare", map[string]any{"weekday": "Blumentag"}, "is not a weekday")
}

// PG-12 The REWE login without a terminal: login_start gives the address,
// login_complete takes the redirect address the browser could not open.
func TestPG12_TheLoginWithoutATerminal(t *testing.T) {
	setupFake(t)
	c := startMCP(t)
	if st := obj(c.ok("status", nil)); st["loggedIn"] != false {
		t.Fatalf("status before: %v", st)
	}
	r := obj(c.ok("login_start", nil))
	authURL, _ := r["authUrl"].(string)
	if authURL == "" || !strings.Contains(r["hint"].(string), "de.rewe.app://redirect") {
		t.Fatalf("login_start: %v", r)
	}
	// The browser's part: korb-fake redirects at once to the app address.
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := noFollow.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	redirect := res.Header.Get("Location")
	if !strings.HasPrefix(redirect, "de.rewe.app://redirect") {
		t.Fatalf("redirect %q", redirect)
	}
	c.fails("login_complete", map[string]any{"url": "de.rewe.app://redirect?code=x&state=wrong"}, "")
	if r := obj(c.ok("login_complete", map[string]any{"url": redirect})); r["loggedIn"] != true {
		t.Fatalf("login_complete: %v", r)
	}
	if st := obj(c.ok("status", nil)); st["loggedIn"] != true {
		t.Fatalf("status after: %v", st)
	}
}

// PG-13 The delivery service without a terminal.
func TestPG13_TheDeliveryServiceWithoutATerminal(t *testing.T) {
	setupFake(t)
	c := startMCP(t)
	var delivery map[string]any
	for _, m := range arr(c.ok("store_search", map[string]any{"zip": "12345"})) {
		if m := obj(m); m["service"] == "delivery" {
			delivery = m
		} else if m["address"] == nil {
			t.Errorf("a market without its address: %v", m)
		}
	}
	if delivery == nil {
		t.Fatal("no delivery service")
	}
	if r := obj(c.ok("store_set", map[string]any{"id": delivery["id"], "zip": "12345"})); r["service"] != "delivery" {
		t.Fatalf("store_set: %v", r)
	}
	c.fails("store_set", map[string]any{"id": "999", "zip": "12345"}, "neither pickup nor delivery")
	st := obj(c.ok("status", nil))
	if !strings.Contains(fmt.Sprint(st), fmt.Sprint(delivery["id"])) && !strings.Contains(fmt.Sprint(st), "DELIVERY") && !strings.Contains(strings.ToLower(fmt.Sprint(st)), "delivery") {
		t.Fatalf("status does not name the service: %v", st)
	}
}
