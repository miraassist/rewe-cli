package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/miraassist/rewe-cli/internal/fakerewe"
)

// korbCmd runs korb in-process against the fake and returns stdout or the error.
func korbCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	runErr := run(context.Background(), args)
	w.Close()
	os.Stdout = orig
	out, _ := io.ReadAll(r)
	return string(out), runErr
}

func mustKorb(t *testing.T, args ...string) string {
	t.Helper()
	out, err := korbCmd(t, args...)
	if err != nil {
		t.Fatalf("korb %s: %v", strings.Join(args, " "), err)
	}
	return out
}

func decode[T any](t *testing.T, s string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decoding %q: %v", s, err)
	}
	return v
}

func wantErr(t *testing.T, contains string, args ...string) {
	t.Helper()
	_, err := korbCmd(t, args...)
	if err == nil || !strings.Contains(err.Error(), contains) {
		t.Fatalf("korb %s: want error containing %q, got %v", strings.Join(args, " "), contains, err)
	}
}

func setupFake(t *testing.T) *fakerewe.Server {
	fake := fakerewe.New()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	t.Setenv("KORB_API_URL", srv.URL+"/api")
	t.Setenv("KORB_AUTH_URL", srv.URL)
	t.Setenv("KORB_CONFIG_DIR", t.TempDir())
	t.Setenv("KORB_CERT_DIR", "")
	t.Setenv("KORB_MAX_TOTAL", "")
	t.Setenv("KORB_MAX_TOTAL_EURO", "")
	return fake
}

func login(t *testing.T) {
	t.Helper()
	mustKorb(t, "login") // against a plain-HTTP test server korb follows the login itself
	if st := decode[map[string]any](t, mustKorb(t, "login", "status")); st["loggedIn"] != true {
		t.Fatalf("not logged in: %v", st)
	}
}

func timeslots(t *testing.T) []map[string]any {
	t.Helper()
	var r struct {
		Slots []map[string]any `json:"getTimeslotsCheckout"`
	}
	if err := json.Unmarshal([]byte(mustKorb(t, "timeslots")), &r); err != nil {
		t.Fatal(err)
	}
	return r.Slots
}

func listing(t *testing.T, query string) string {
	t.Helper()
	ps := decode[[]map[string]any](t, mustKorb(t, "search", query))
	if len(ps) == 0 {
		t.Fatalf("no product for %q", query)
	}
	return ps[0]["listing"].(map[string]any)["listingId"].(string)
}

func TestE2EDeliveryOrderAndModification(t *testing.T) {
	setupFake(t)
	wantErr(t, "no store selected", "basket")
	mustKorb(t, "store", "set", fakerewe.DeliveryMarketID, "12345") // store lookups need no login
	wantErr(t, "not logged in", "basket")
	login(t)

	markets := decode[[]map[string]any](t, mustKorb(t, "store", "search", "12345"))
	if len(markets) != 3 || markets[2]["serviceType"] != "DELIVERY" {
		t.Fatalf("unexpected markets: %v", markets)
	}
	wantErr(t, "neither pickup nor delivery", "store", "set", "9999999", "12345")
	if s := decode[map[string]string](t, mustKorb(t, "store", "set", fakerewe.DeliveryMarketID, "12345")); s["serviceType"] != "DELIVERY" {
		t.Fatalf("store: %v", s)
	}

	milk, coffee, choc := listing(t, "Vollmilch"), listing(t, "Tchibo"), listing(t, "Ritter Sport")
	if li := decode[map[string]any](t, mustKorb(t, "basket", "add", milk, "--qty", "2")); li["quantity"] != 2.0 {
		t.Fatalf("basket add: %v", li)
	}
	wantErr(t, "es fehlen 57,62 €", "checkout")
	mustKorb(t, "basket", "add", coffee, "--qty", "6") // 2x1,19 + 6x8,99 = 56,32
	wantErr(t, "es fehlen 3,68 €", "checkout")
	mustKorb(t, "basket", "add", coffee, "--qty", "7")

	var thursday string
	for _, s := range timeslots(t) {
		start, _ := time.Parse(time.RFC3339, s["startTime"].(string))
		if start.Weekday() == time.Thursday {
			if s["serviceFee"] != 0.0 {
				t.Fatalf("thursday delivery must be free: %v", s)
			}
			thursday = s["id"].(string)
			break
		}
	}
	if thursday == "" {
		t.Fatal("no thursday slot")
	}
	pay := decode[[]map[string]any](t, mustKorb(t, "checkout", "payment-options"))
	usable := 0
	for _, m := range pay {
		if m["usable"] == true {
			usable++
			if m["key"] != "PAYPAL" {
				t.Fatalf("only PayPal should be usable: %v", m)
			}
		}
	}
	if usable != 1 {
		t.Fatalf("payment options: %v", pay)
	}
	co := decode[map[string]map[string]any](t, mustKorb(t, "checkout", "create", thursday))
	if co["checkout"]["payment"].(map[string]any)["paymentMethod"] != "PAYPAL" {
		t.Fatalf("checkout: %v", co)
	}
	wantErr(t, "exceeds the limit of 10,00 €", "checkout", "order", "--max-total", "1000")
	t.Setenv("KORB_MAX_TOTAL", "2000")
	wantErr(t, "exceeds the limit of 20,00 €", "checkout", "order", "--max-total", "99999")
	t.Setenv("KORB_MAX_TOTAL", "")
	orderID := decode[map[string]map[string]string](t, mustKorb(t, "checkout", "order"))["order"]["orderId"]
	if orderID == "" {
		t.Fatal("no order id")
	}
	if b := decode[map[string]any](t, mustKorb(t, "basket")); len(b["lineItems"].([]any)) != 0 {
		t.Fatalf("basket must be empty after ordering: %v", b["lineItems"])
	}
	open := decode[[]map[string]any](t, mustKorb(t, "orders"))
	if len(open) != 1 || open[0]["orderId"] != orderID {
		t.Fatalf("open orders: %v", open)
	}

	// Modify the order: add chocolate.
	mustKorb(t, "orders", "modify", orderID)
	wantErr(t, "already being modified", "orders", "modify", orderID)
	wantErr(t, "is being modified", "checkout")
	mustKorb(t, "basket", "add", choc, "--qty", "3")
	st := decode[map[string]any](t, mustKorb(t, "orders", "modify", "status"))
	if n := len(st["basket"].(map[string]any)["lineItems"].([]any)); n != 3 {
		t.Fatalf("modification basket should have 3 lines, got %d", n)
	}
	mustKorb(t, "orders", "modify", "submit")
	detail := mustKorb(t, "orders", "get", orderID)
	if !strings.Contains(detail, "Ritter Sport") {
		t.Fatalf("modification not applied: %s", detail)
	}
	if b := decode[map[string]any](t, mustKorb(t, "basket")); len(b["lineItems"].([]any)) != 0 {
		t.Fatal("regular basket must be untouched by the modification")
	}

	// A modification below the minimum order value is refused; cancel keeps the order.
	mustKorb(t, "orders", "modify", orderID)
	mustKorb(t, "basket", "add", coffee, "--qty", "1")
	wantErr(t, "Mindestbestellwert", "orders", "modify", "submit")
	mustKorb(t, "orders", "modify", "cancel")
	if st := decode[map[string]any](t, mustKorb(t, "orders", "modify", "status")); st["modification"] != nil {
		t.Fatalf("modification still active: %v", st)
	}
	if detail := mustKorb(t, "orders", "get", orderID); !strings.Contains(detail, `"quantity":7`) {
		t.Fatalf("cancelled modification changed the order: %s", detail)
	}

	// Second order paid with a method without saved data: korb refuses.
	mustKorb(t, "basket", "add", coffee, "--qty", "7")
	mustKorb(t, "checkout", "create", thursday, "--payment", "CREDIT_CARD")
	wantErr(t, "needs payment data", "checkout", "order")

	// Cancel the first order.
	mustKorb(t, "orders", "delete", orderID)
	if open := decode[[]map[string]any](t, mustKorb(t, "orders")); len(open) != 0 {
		t.Fatalf("order not cancelled: %v", open)
	}
}

func TestE2EPickupAndExtras(t *testing.T) {
	setupFake(t)
	login(t)
	mustKorb(t, "store", "set", fakerewe.PickupMarketID, "12345")
	if n := len(decode[[]any](t, mustKorb(t, "search", "milch", "--organic"))); n != 1 {
		t.Fatalf("organic milk: %d", n)
	}
	if n := len(decode[[]any](t, mustKorb(t, "search", "4046700501030"))); n != 1 {
		t.Fatalf("EAN search: %d", n)
	}
	if n := len(decode[[]any](t, mustKorb(t, "search", "*", "--category", "getraenke"))); n != 3 {
		t.Fatalf("category search: %d", n)
	}
	sug := decode[map[string]any](t, mustKorb(t, "suggestion", "threshold", "3"))
	if s := sug["suggestions"].([]any); len(s) != 3 || s[0].(map[string]any)["freq"] != 2.0 {
		t.Fatalf("suggestions: %v", sug)
	}
	favs := decode[[]map[string]any](t, mustKorb(t, "favorites"))
	milk := listing(t, "Vollmilch")
	added := decode[map[string]any](t, mustKorb(t, "favorites", "add", milk, "100001"))
	mustKorb(t, "favorites", "delete", added["itemId"].(string))
	if n := len(decode[[]any](t, mustKorb(t, "favorites"))); n != len(favs) {
		t.Fatalf("favorites not restored: %d vs %d", n, len(favs))
	}
	mustKorb(t, "basket", "add", milk)
	co := decode[map[string]map[string]any](t, mustKorb(t, "checkout", "create", timeslots(t)[0]["id"].(string)))
	if co["checkout"]["payment"].(map[string]any)["paymentMethod"] != "MARKET_PAYMENT" {
		t.Fatalf("pickup must pay in market: %v", co)
	}
	if id := decode[map[string]map[string]string](t, mustKorb(t, "checkout", "order"))["order"]["orderId"]; id == "" {
		t.Fatal("no pickup order")
	}
	if e := decode[[]any](t, mustKorb(t, "ebons")); len(e) != 1 {
		t.Fatalf("ebons: %v", e)
	}
	pdf := t.TempDir() + "/r.pdf"
	mustKorb(t, "ebons", "download", "11111111-2222-4333-8444-555555555555", "--output", pdf)
	if b, _ := os.ReadFile(pdf); !strings.HasPrefix(string(b), "%PDF") {
		t.Fatal("receipt is not a PDF")
	}
}

func TestFakeRequiresSeparateConfigDir(t *testing.T) {
	t.Setenv("KORB_API_URL", "http://127.0.0.1:1/api")
	t.Setenv("KORB_CONFIG_DIR", "")
	wantErr(t, "KORB_CONFIG_DIR", "basket")
}

// TestE2EShoppingListFavorites plays the agent use case "milk is on the
// shopping list": the user's favorite milk must win over the first search hit
// and the most often ordered milk; items without a favorite fall back to search.
func TestE2EShoppingListFavorites(t *testing.T) {
	setupFake(t)
	login(t)
	mustKorb(t, "store", "set", fakerewe.DeliveryMarketID, "12345")

	favs := decode[[]map[string]any](t, mustKorb(t, "favorites"))
	if len(favs) != len(fakerewe.FavoriteProductIDs) {
		t.Fatalf("favorites: got %d, want %d", len(favs), len(fakerewe.FavoriteProductIDs))
	}
	hits := decode[[]map[string]any](t, mustKorb(t, "search", "milch"))
	if len(hits) < 4 || hits[0]["productId"] == "100002" {
		t.Fatalf("search must offer several milks with the favorite not first: %v", hits)
	}
	favMilk := decode[[]map[string]any](t, mustKorb(t, "favorites", "search", "milch"))
	if len(favMilk) != 1 || favMilk[0]["productId"] != "100002" {
		t.Fatalf("favorite milk: %v", favMilk)
	}
	if n := len(decode[[]any](t, mustKorb(t, "favorites", "search", "tomate"))); n != 0 {
		t.Fatalf("tomatoes must not be a favorite: %d", n)
	}

	mustKorb(t, "basket", "add", favMilk[0]["listing"].(map[string]any)["listingId"].(string), "--qty", "2")
	mustKorb(t, "basket", "add", listing(t, "Rispentomaten"))
	b := mustKorb(t, "basket")
	if !strings.Contains(b, "Schwarzwaldmilch") || !strings.Contains(b, "Rispentomaten") || strings.Contains(b, "ja! Frische Vollmilch") {
		t.Fatalf("basket: %s", b)
	}
}
