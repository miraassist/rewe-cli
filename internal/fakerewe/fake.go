// Package fakerewe is an in-memory imitation of the REWE mobile API and REWE's
// Keycloak login, for developing and testing korb and agent skills without
// touching a real REWE account. It mimics the behaviour observed on the real
// API (response envelopes, 412 on checkout below the minimum order value,
// PayPal direct debit without redirect, rotating refresh tokens, ...).
// Where the real behaviour is unknown (e.g. submitting an order modification)
// it implements the most plausible variant.
package fakerewe

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Fixed IDs of the fake world.
const (
	PickupMarketID   = "1000001"
	PickupMarket2ID  = "1000002"
	DeliveryMarketID = "2000001"
	MinimumDelivery  = 6000  // cents of article value (deposit does not count)
	FreeDelivery     = 12000 // cents of article value
	DeliveryFee      = 390
	FreePickup       = 5000
	PickupFee        = 200
	BagFee           = 300 // reusable bags for delivery
)

type product struct {
	ID, ArticleID, Title, Grammage, EAN, Category string
	Price, Deposit                                int
	Organic, Regional, Vegan, Vegetarian          bool
}

func (p product) listingID() string { return "8-" + p.ArticleID + "-fake-listing" }

var catalog = []product{
	{"100001", "A1MILK01", "ja! Frische Vollmilch 3,5% 1l", "1l", "4337256000011", "milch-kaese-eier", 119, 0, false, true, false, true},
	{"100008", "A1MILK04", "Weihenstephan Frische Weidemilch 3,5% 1l", "1l", "4008452011005", "milch-kaese-eier", 155, 0, false, false, false, true},
	{"100009", "A1MILK05", "Müller Milchreis Klassik 200g", "200g", "4025500165505", "milch-kaese-eier", 69, 0, false, false, false, true},
	{"100002", "A1MILK02", "Schwarzwaldmilch Bio H-Milch 3,5% 1l", "1l", "4046700501030", "milch-kaese-eier", 219, 0, true, true, false, true},
	{"100003", "A1MILK03", "Alpro Haferdrink 1l", "1l", "5411188112709", "milch-kaese-eier", 249, 0, false, false, true, true},
	{"100004", "A1EGGS01", "REWE Bio Eier 10 Stück", "10 Stück", "4337256111111", "milch-kaese-eier", 429, 0, true, false, false, true},
	{"100005", "A1CHEE01", "Castello Höhlenkäse Scheiben 150g", "150g", "5760466925570", "milch-kaese-eier", 299, 0, false, false, false, true},
	{"100006", "A1BUTT01", "Kerrygold Butter 250g", "250g", "5011038133511", "milch-kaese-eier", 279, 0, false, false, false, true},
	{"100007", "A1YOGH01", "Elinas Joghurt Griechischer Art 500g", "500g", "4003490032076", "milch-kaese-eier", 249, 0, false, false, false, true},
	{"100010", "A1BREA01", "Biokaiser Bio Dinkel-Urkorn-Brot 500g", "500g", "4012345000101", "brot-backwaren", 389, 0, true, true, true, true},
	{"100011", "A1BREA02", "Harry Toastbrötchen 6 Stück", "6 Stück", "4009249002211", "brot-backwaren", 179, 0, false, false, true, true},
	{"100020", "A1PAST01", "REWE Bio Mezze Penne 500g", "500g", "4388844000201", "nudeln-reis", 149, 0, true, false, true, true},
	{"100021", "A1RICE01", "REWE Beste Wahl Langkorn Reis 1kg", "1kg", "4388844000202", "nudeln-reis", 229, 0, false, false, true, true},
	{"100030", "A1BANA01", "Bio Bananen ca. 1kg", "ca. 1kg", "", "obst-gemuese", 199, 0, true, false, true, true},
	{"100031", "A1APPL01", "Äpfel Elstar aus der Region 1kg", "1kg", "", "obst-gemuese", 279, 0, false, true, true, true},
	{"100032", "A1BROC01", "Broccoli 500g", "500g", "", "obst-gemuese", 169, 0, false, false, true, true},
	{"100033", "A1TOMA01", "Rispentomaten 500g", "500g", "", "obst-gemuese", 229, 0, false, true, true, true},
	{"100034", "A1CHIC01", "Chicorée aus der Region", "1 Stück", "", "obst-gemuese", 69, 0, false, true, true, true},
	{"100040", "A1CHIK01", "REWE Bio Hähnchengeschnetzeltes 300g", "300g", "4337256000401", "fleisch-fisch", 599, 0, true, false, false, false},
	{"100041", "A1SALA01", "Citterio Salami Milano 70g", "70g", "8005510720001", "fleisch-fisch", 229, 0, false, false, false, false},
	{"100050", "A1WATE01", "Gerolsteiner Naturell 6x1,5l", "6x1,5l", "4001513000501", "getraenke", 549, 150, false, true, true, true},
	{"100051", "A1JUIC01", "Deit Orange 6x0,75l", "6x0,75l", "4019313006094", "getraenke", 599, 150, false, false, true, true},
	{"100052", "A1BEER01", "Rothaus Tannenzäpfle 6x0,33l", "6x0,33l", "4014764000521", "getraenke", 549, 48, false, true, true, true},
	{"100060", "A1COFF01", "Tchibo Feine Milde Kaffee ganze Bohnen 500g", "500g", "4006067084218", "kaffee-tee", 899, 0, false, false, true, true},
	{"100061", "A1NUTS01", "KoRo Bio Erdnussmus 500g", "500g", "4260654780001", "snacks-suesses", 799, 0, true, false, true, true},
	{"100062", "A1CHOC01", "Ritter Sport Nuss-Splitter 100g", "100g", "4000417025005", "snacks-suesses", 179, 0, false, false, false, true},
	{"100063", "A1CHOC02", "Milka Alpenmilch Schokolade 100g", "100g", "7622210449283", "snacks-suesses", 149, 0, false, false, false, true},
	{"100070", "A1DETE01", "Frosch Spülmittel Zitrone 500ml", "500ml", "4001499000701", "haushalt", 189, 0, false, false, true, true},
}

var categories = []map[string]any{
	{"slug": "obst-gemuese", "name": "Obst & Gemüse", "categoryTags": []string{}, "childCategories": []any{}},
	{"slug": "milch-kaese-eier", "name": "Milch, Käse & Eier", "categoryTags": []string{}, "childCategories": []any{}},
	{"slug": "brot-backwaren", "name": "Brot & Backwaren", "categoryTags": []string{}, "childCategories": []any{}},
	{"slug": "fleisch-fisch", "name": "Fleisch & Fisch", "categoryTags": []string{}, "childCategories": []any{}},
	{"slug": "nudeln-reis", "name": "Nudeln & Reis", "categoryTags": []string{}, "childCategories": []any{}},
	{"slug": "getraenke", "name": "Getränke", "categoryTags": []string{}, "childCategories": []any{}},
	{"slug": "kaffee-tee", "name": "Kaffee & Tee", "categoryTags": []string{}, "childCategories": []any{}},
	{"slug": "snacks-suesses", "name": "Snacks & Süßes", "categoryTags": []string{}, "childCategories": []any{}},
	{"slug": "haushalt", "name": "Haushalt", "categoryTags": []string{}, "childCategories": []any{}},
}

type line struct {
	ProductID string
	Qty       int
}

type basket struct {
	ID          string
	Version     int
	Lines       []line
	OrderID     string // non-empty for a modification basket
	ServiceType string
	MarketID    string
	ZipCode     string
}

type slot struct {
	ID         string
	Start, End time.Time
	Fee        int
}

type checkout struct {
	ID          string
	BasketID    string
	Slot        *slot
	Payment     string
	Confirmed   bool
	OrderID     string // for modifications
	ServiceType string
}

type order struct {
	ID          string
	Date        time.Time
	Lines       []line
	Slot        slot
	ServiceType string
	Payment     string
	Status      string // open, delivered, cancelled
}

type grant struct {
	challenge, state string
	expires          time.Time
}

// Server is the fake REWE backend. Create it with New.
type Server struct {
	mu        sync.Mutex
	now       func() time.Time
	basket    *basket
	mods      map[string]*basket // modification baskets by id
	checkouts map[string]*checkout
	orders    []*order
	favorites []string // item ids -> product via favItems
	favItems  map[string]string
	codes     map[string]grant
	access    map[string]time.Time
	refresh   map[string]bool
	noSaved   bool // no saved payment data (SetSavedPayment)
	reserved  map[string]time.Time
	seq       int
	mux       *http.ServeMux
	// Log receives one line per request if set.
	Log func(string)
}

// FavoriteProductIDs is the fixed favorite list every fake starts with, in
// list order. It mirrors a typical household: a specific milk that is not
// the first search hit for "milch" (and not the most often ordered one), a
// plant drink that does not contain "milch" in its title, and staples like
// butter, eggs and bread. Tomatoes, pasta and rice are deliberately not
// favorites, so an agent has to fall back to search or order history.
var FavoriteProductIDs = []string{
	"100002", // Schwarzwaldmilch Bio H-Milch 3,5% 1l
	"100003", // Alpro Haferdrink 1l
	"100006", // Kerrygold Butter 250g
	"100004", // REWE Bio Eier 10 Stück
	"100010", // Biokaiser Bio Dinkel-Urkorn-Brot 500g
	"100031", // Äpfel Elstar aus der Region 1kg
	"100030", // Bio Bananen ca. 1kg
	"100060", // Tchibo Feine Milde Kaffee ganze Bohnen 500g
	"100061", // KoRo Bio Erdnussmus 500g
	"100050", // Gerolsteiner Naturell 6x1,5l
	"100062", // Ritter Sport Nuss-Splitter 100g
}

// New returns a fake with a small order history and favorites.
func New() *Server {
	s := &Server{
		now:       time.Now,
		mods:      map[string]*basket{},
		checkouts: map[string]*checkout{},
		favItems:  map[string]string{},
		codes:     map[string]grant{},
		access:    map[string]time.Time{},
		refresh:   map[string]bool{},
		reserved:  map[string]time.Time{},
	}
	s.basket = &basket{ID: s.uuid()}
	for _, pid := range FavoriteProductIDs {
		id := s.uuid()
		s.favorites = append(s.favorites, id)
		s.favItems[id] = pid
	}
	past := s.now().AddDate(0, 0, -21)
	for i, lines := range [][]line{
		{{"100001", 2}, {"100010", 1}, {"100031", 1}, {"100040", 1}, {"100050", 2}, {"100061", 1}, {"100020", 2}, {"100005", 1}, {"100060", 1}, {"100004", 1}},
		{{"100001", 2}, {"100010", 1}, {"100032", 1}, {"100050", 2}, {"100007", 1}, {"100020", 1}, {"100060", 1}, {"100062", 2}, {"100004", 1}},
	} {
		d := past.AddDate(0, 0, i*10)
		start := time.Date(d.Year(), d.Month(), d.Day()+2, 18, 0, 0, 0, time.Local)
		s.orders = append(s.orders, &order{ID: s.orderID(), Date: d, Lines: lines, ServiceType: "DELIVERY", Payment: "PAYPAL", Status: "delivered",
			Slot: slot{ID: s.uuid(), Start: start, End: start.Add(4 * time.Hour)}})
	}
	s.routes()
	return s
}

// SetSavedPayment switches the saved PayPal direct debit on (default) or off.
func (s *Server) SetSavedPayment(on bool) { s.mu.Lock(); s.noSaved = !on; s.mu.Unlock() }

// SetClock replaces the clock (for tests).
func (s *Server) SetClock(now func() time.Time) { s.mu.Lock(); s.now = now; s.mu.Unlock() }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.Log != nil {
		s.Log(r.Method + " " + r.URL.RequestURI())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mux.ServeHTTP(w, r)
}

func (s *Server) uuid() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func (s *Server) orderID() string {
	s.seq++
	return fmt.Sprintf("B-FAK-%03d-%03d", s.seq/1000, s.seq%1000)
}

func findProduct(id string) (product, bool) {
	i := slices.IndexFunc(catalog, func(p product) bool { return p.ID == id })
	if i < 0 {
		return product{}, false
	}
	return catalog[i], true
}

func findListing(listingID string) (product, bool) {
	i := slices.IndexFunc(catalog, func(p product) bool { return p.listingID() == listingID })
	if i < 0 {
		return product{}, false
	}
	return catalog[i], true
}

// ---- JSON helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func data(w http.ResponseWriter, v any) { writeJSON(w, 200, map[string]any{"data": v}) }

// gqlError mimics REWE's GraphQL gateway: HTTP 200 with an errors array.
func gqlError(w http.ResponseWriter, status int, msg, field string) {
	writeJSON(w, 200, map[string]any{
		"errors": []any{map[string]any{"message": msg, "path": []string{field},
			"extensions": map[string]any{"statusCode": status, "classification": "RemoteResourceFetchingError"}}},
		"data": map[string]any{field: nil},
	})
}

func readBody(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func productJSON(p product, itemID *string) map[string]any {
	return map[string]any{
		"productId": p.ID, "articleId": p.ArticleID, "title": p.Title,
		"imageURL":   "https://fake.rewe.invalid/img/" + p.ArticleID + ".png",
		"orderLimit": 99,
		"listing":    map[string]any{"listingId": p.listingID(), "currentRetailPrice": p.Price, "grammage": p.Grammage},
		"attributes": map[string]any{"isOrganic": p.Organic, "isRegional": p.Regional},
		"itemId":     itemID,
	}
}

// ---- routing ----

func (s *Server) routes() {
	m := http.NewServeMux()
	m.HandleFunc("GET /auth", s.handleAuth)
	m.HandleFunc("POST /token", s.handleToken)
	m.HandleFunc("GET /api/service-portfolio/{zip}", s.handlePortfolio)
	m.HandleFunc("GET /api/markets/{id}/{type}", s.handleMarket)
	m.HandleFunc("GET /api/products", s.market(s.handleProducts))
	m.HandleFunc("GET /api/purchased-products", s.authed(s.handlePurchased))
	m.HandleFunc("GET /api/shop-categories", s.authed(func(w http.ResponseWriter, r *http.Request) { data(w, map[string]any{"categories": categories}) }))
	m.HandleFunc("GET /api/favorites", s.authed(s.handleFavorites))
	m.HandleFunc("POST /api/favorites/{list}/lineitems", s.authed(s.handleFavoriteAdd))
	m.HandleFunc("DELETE /api/favorites/{list}/lineitems/{item}", s.authed(s.handleFavoriteDelete))
	m.HandleFunc("POST /api/baskets", s.authed(s.handleBasket))
	m.HandleFunc("POST /api/baskets/{id}/listings/{listing}", s.authed(s.handleBasketSet))
	m.HandleFunc("GET /api/timeslots/checkout", s.authed(s.handleTimeslots))
	m.HandleFunc("POST /api/timeslots/reservations", s.authed(s.handleReserve))
	m.HandleFunc("POST /api/checkouts", s.authed(s.handleCheckoutCreate))
	m.HandleFunc("PATCH /api/checkouts/{id}/timeslots", s.authed(s.handleCheckoutSlot))
	m.HandleFunc("PATCH /api/checkouts/{id}/payments", s.authed(s.handleCheckoutPayment))
	m.HandleFunc("GET /api/payment-options", s.authed(s.handlePaymentOptions))
	m.HandleFunc("POST /api/checkouts/{id}/confirmations", s.authed(s.handleConfirm))
	m.HandleFunc("POST /api/checkouts/{id}/orders", s.authed(s.handlePlaceOrder))
	m.HandleFunc("POST /api/checkouts/order-modifications", s.authed(s.handleModStart))
	m.HandleFunc("POST /api/checkouts/{id}/cancel-order-modify", s.authed(s.handleModCancel))
	m.HandleFunc("GET /api/orders/history", s.authed(s.handleHistory))
	m.HandleFunc("GET /api/orders/{id}", s.authed(s.handleOrder))
	m.HandleFunc("DELETE /api/orders/{id}", s.authed(s.handleOrderCancel))
	m.HandleFunc("GET /api/ebons", s.authed(func(w http.ResponseWriter, r *http.Request) {
		data(w, map[string]any{"getEbons": map[string]any{"items": []any{map[string]any{
			"receiptId": "11111111-2222-4333-8444-555555555555", "receiptTimestamp": "2026-11-01T10:15:00Z", "receiptTotalPrice": 2345,
			"market":    map[string]any{"name": "REWE Fake Markt", "city": "Musterstadt", "wwIdent": PickupMarketID, "street": "Testweg 1", "zipCode": "12345"},
			"cancelled": false}}}})
	}))
	m.HandleFunc("GET /api/receipts/{id}/pdf", s.authed(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4\n% korb-fake receipt\n%%EOF\n"))
	}))
	s.mux = m
}

// market checks the market headers like the real API does.
func (s *Server) market(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st, id := r.Header.Get("rd-service-types"), r.Header.Get("rd-market-id")
		ok := (st == "PICKUP" && (id == PickupMarketID || id == PickupMarket2ID)) || (st == "DELIVERY" && id != "")
		if !ok || r.Header.Get("rd-postcode") == "" {
			writeJSON(w, 400, map[string]any{"title": "Invalid market selection", "status": 400, "validationErrors": []any{
				map[string]any{"name": "rd-market-id", "reason": "invalid or missing header value"}}})
			return
		}
		h(w, r)
	}
}

func (s *Server) authed(h http.HandlerFunc) http.HandlerFunc {
	return s.market(func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		exp, ok := s.access[tok]
		if !ok || s.now().After(exp) {
			writeJSON(w, 401, map[string]any{"title": "Unauthorized", "status": 401})
			return
		}
		h(w, r)
	})
}

// ---- auth (Keycloak imitation) ----

func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("client_id") != "reweios" || q.Get("redirect_uri") != "de.rewe.app://redirect" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		http.Error(w, "invalid authorization request", 400)
		return
	}
	code := s.uuid() + "." + s.uuid()
	s.codes[code] = grant{challenge: q.Get("code_challenge"), state: q.Get("state"), expires: s.now().Add(5 * time.Minute)}
	loc := "de.rewe.app://redirect?state=" + url.QueryEscape(q.Get("state")) + "&code=" + url.QueryEscape(code)
	// The real login page needs a browser; the fake "logs in" immediately.
	w.Header().Set("Location", loc)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusFound)
	fmt.Fprintf(w, "<html><body><p>korb-fake: logged in. Redirect URL:</p><pre>%s</pre></body></html>\n", loc)
}

func (s *Server) issueTokens(w http.ResponseWriter) {
	access := fakeJWT(s.now().Add(10*time.Minute), s.uuid())
	refresh := fakeJWT(time.Unix(0, 0), s.uuid()) // offline token without expiry, like REWE
	s.access[access] = s.now().Add(10 * time.Minute)
	s.refresh[refresh] = true
	writeJSON(w, 200, map[string]any{"access_token": access, "refresh_token": refresh, "expires_in": 600,
		"refresh_expires_in": 0, "token_type": "Bearer", "id_token": "fake", "scope": "openid email customer offline_access profile"})
}

func fakeJWT(exp time.Time, jti string) string {
	p, _ := json.Marshal(map[string]any{"exp": exp.Unix(), "jti": jti, "azp": "reweios"})
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(p) + ".fake"
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	bad := func(desc string) {
		writeJSON(w, 400, map[string]string{"error": "invalid_grant", "error_description": desc})
	}
	if r.Form.Get("client_id") != "reweios" {
		writeJSON(w, 401, map[string]string{"error": "invalid_client"})
		return
	}
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		g, ok := s.codes[r.Form.Get("code")]
		delete(s.codes, r.Form.Get("code"))
		if !ok || s.now().After(g.expires) {
			bad("Code not valid")
			return
		}
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
			bad("PKCE verification failed")
			return
		}
		s.issueTokens(w)
	case "refresh_token":
		rt := r.Form.Get("refresh_token")
		if !s.refresh[rt] {
			bad("Invalid refresh token")
			return
		}
		delete(s.refresh, rt) // rotation, like REWE
		s.issueTokens(w)
	case "password":
		writeJSON(w, 400, map[string]string{"error": "unauthorized_client", "error_description": "Client not allowed for direct access grants"})
	default:
		writeJSON(w, 400, map[string]string{"error": "unsupported_grant_type"})
	}
}

// ---- markets & products ----

func pickupMarkets(zip string) []map[string]any {
	return []map[string]any{
		{"wwIdent": PickupMarketID, "displayName": "REWE Fake Markt", "companyName": "REWE Fake oHG", "isPickupStation": false,
			"streetWithHouseNumber": "Testweg 1", "zipCode": zip, "city": "Musterstadt", "pickupType": "PICKUP_SERVICE"},
		{"wwIdent": PickupMarket2ID, "displayName": "REWE Fake Center", "companyName": "REWE Markt GmbH", "isPickupStation": false,
			"streetWithHouseNumber": "Beispielallee 99", "zipCode": zip, "city": "Musterstadt-Nord", "pickupType": "PICKUP_SERVICE"},
	}
}

func (s *Server) handlePortfolio(w http.ResponseWriter, r *http.Request) {
	zip := r.PathValue("zip")
	if _, err := strconv.Atoi(zip); err != nil || len(zip) != 5 {
		data(w, map[string]any{"servicePortfolio": nil})
		return
	}
	data(w, map[string]any{"servicePortfolio": map[string]any{
		"customerZipCode": zip, "deliveryMarket": map[string]any{"wwIdent": DeliveryMarketID},
		"pickupMarkets": pickupMarkets(zip), "lsfkMarkets": []any{}, "openLsfkMarkets": []any{},
	}})
}

func (s *Server) handleMarket(w http.ResponseWriter, r *http.Request) {
	id, typ := r.PathValue("id"), r.PathValue("type")
	switch {
	case typ == "DELIVERY" && id == DeliveryMarketID:
		data(w, map[string]any{"marketDetails": map[string]any{"market": map[string]any{"__typename": "DeliveryMarket", "wwIdent": id, "displayName": "REWE Lieferservice (Fake)"}}})
	case typ == "PICKUP" && (id == PickupMarketID || id == PickupMarket2ID):
		for _, m := range pickupMarkets("12345") {
			if m["wwIdent"] == id {
				m["__typename"] = "PickupMarket"
				data(w, map[string]any{"marketDetails": map[string]any{"market": m}})
				return
			}
		}
	default:
		gqlError(w, 404, "Not Found", "marketDetails")
	}
}

func (s *Server) handleProducts(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("query")))
	var attrs, cats []string
	for _, f := range strings.Split(r.URL.Query().Get("filters"), "&") {
		if k, v, ok := strings.Cut(f, "="); ok {
			switch k {
			case "attribute":
				attrs = append(attrs, v)
			case "categorySlug":
				cats = append(cats, v)
			}
		}
	}
	var hits []map[string]any
	for _, p := range catalog {
		if q != "*" && q != "" && !strings.Contains(strings.ToLower(p.Title), q) && p.EAN != q {
			continue
		}
		if len(cats) > 0 && !slices.Contains(cats, p.Category) {
			continue
		}
		okAttrs := true
		for _, a := range attrs {
			okAttrs = okAttrs && map[string]bool{"organic": p.Organic, "regional": p.Regional, "vegan": p.Vegan, "vegetarian": p.Vegetarian}[a]
		}
		if okAttrs {
			hits = append(hits, productJSON(p, nil))
		}
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = max(page, 1)
	const per = 40
	from, to := min((page-1)*per, len(hits)), min(page*per, len(hits))
	data(w, map[string]any{"products": map[string]any{
		"pagination": map[string]any{"objectsPerPage": per, "currentPage": page, "pageCount": (len(hits) + per - 1) / per, "objectCount": len(hits)},
		"products":   append([]map[string]any{}, hits[from:to]...),
	}})
}

func (s *Server) handlePurchased(w http.ResponseWriter, r *http.Request) {
	seen := map[string]bool{}
	out := []map[string]any{}
	for _, o := range s.orders {
		for _, l := range o.Lines {
			if p, ok := findProduct(l.ProductID); ok && !seen[p.ID] {
				seen[p.ID] = true
				out = append(out, productJSON(p, nil))
			}
		}
	}
	data(w, map[string]any{"purchasedProducts": map[string]any{"products": out}})
}

// ---- favorites ----

const favListID = "fav-list-0001"

func (s *Server) favList() map[string]any {
	items := []map[string]any{}
	for _, id := range s.favorites {
		p, _ := findProduct(s.favItems[id])
		items = append(items, productJSON(p, &id))
	}
	return map[string]any{"id": favListID, "name": "Meine Favoriten", "items": items}
}

func (s *Server) handleFavorites(w http.ResponseWriter, r *http.Request) {
	data(w, map[string]any{"favoriteLists": map[string]any{"favorites": []any{s.favList()}}})
}

func (s *Server) handleFavoriteAdd(w http.ResponseWriter, r *http.Request) {
	var req struct{ ListingID, ProductID string }
	if readBody(r, &req) != nil || r.PathValue("list") != favListID {
		gqlError(w, 400, "Bad Request", "addLineItemToFavoriteList")
		return
	}
	p, ok := findListing(req.ListingID)
	if !ok || p.ID != req.ProductID {
		gqlError(w, 404, "Not Found", "addLineItemToFavoriteList")
		return
	}
	for _, id := range s.favorites { // like REWE: no duplicates
		if s.favItems[id] == p.ID {
			data(w, map[string]any{"addLineItemToFavoriteList": s.favList()})
			return
		}
	}
	id := s.uuid()
	s.favorites = append(s.favorites, id)
	s.favItems[id] = p.ID
	data(w, map[string]any{"addLineItemToFavoriteList": s.favList()})
}

func (s *Server) handleFavoriteDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("item")
	i := slices.Index(s.favorites, id)
	if i < 0 || r.PathValue("list") != favListID {
		writeJSON(w, 404, map[string]any{"title": "Not Found", "status": 404})
		return
	}
	s.favorites = slices.Delete(s.favorites, i, i+1)
	delete(s.favItems, id)
	w.WriteHeader(http.StatusNoContent)
}

// ---- basket ----

// basketFor returns the regular basket or, with modification headers, the
// modification basket.
func (s *Server) basketFor(r *http.Request) *basket {
	if r.Header.Get("rd-modified-order-id") != "" {
		if b, ok := s.mods[r.Header.Get("rd-basket-id")]; ok && b.OrderID == r.Header.Get("rd-modified-order-id") {
			return b
		}
		return nil
	}
	b := s.basket
	b.ServiceType, b.MarketID, b.ZipCode = r.Header.Get("rd-service-types"), r.Header.Get("rd-market-id"), r.Header.Get("rd-postcode")
	return b
}

func articlePrice(lines []line) (price, deposit, count int) {
	for _, l := range lines {
		p, _ := findProduct(l.ProductID)
		price += p.Price * l.Qty
		deposit += p.Deposit * l.Qty
		count += l.Qty
	}
	return
}

func (s *Server) basketJSON(b *basket) map[string]any {
	price, deposit, count := articlePrice(b.Lines)
	items := []map[string]any{}
	for _, l := range b.Lines {
		p, _ := findProduct(l.ProductID)
		items = append(items, map[string]any{"quantity": l.Qty, "price": p.Price, "totalPrice": p.Price * l.Qty,
			"grammage": p.Grammage, "product": productJSON(p, nil), "changes": nil, "violations": nil})
	}
	bag, threshold, minimum, noun := 0, FreePickup, 0, "Abholung"
	if b.ServiceType == "DELIVERY" {
		bag, threshold, minimum, noun = BagFee, FreeDelivery, MinimumDelivery, "Lieferung"
	}
	if len(b.Lines) == 0 {
		bag = 0
	}
	violations := []map[string]any{}
	if minimum > 0 && len(b.Lines) > 0 && price < minimum {
		violations = append(violations, map[string]any{"id": "minimum.delivery.not.reached",
			"message": fmt.Sprintf("Mindestbestellwert %d € nicht erreicht!", minimum/100), "detailMessage": nil})
	}
	var next any
	if price < threshold {
		rest := threshold - price
		next = map[string]any{"remainingArticlePrice": rest, "articlePriceThreshold": threshold,
			"displayText": fmt.Sprintf("Kauf noch für %d,%02d € ein, dann ist deine %s gratis.", rest/100, rest%100, noun)}
	}
	var orderID any
	if b.OrderID != "" {
		orderID = b.OrderID
	}
	return map[string]any{
		"id": b.ID, "version": b.Version, "orderId": orderID,
		"serviceSelection":     map[string]any{"wwIdent": b.MarketID, "serviceType": b.ServiceType, "zipCode": b.ZipCode},
		"serviceConfiguration": map[string]any{"minimumOrderAmount": minimum},
		"staggerings": map[string]any{"reachedStaggering": map[string]any{"articlePriceThreshold": 0, "displayText": ""},
			"nextStaggering": next},
		"lineItems": items, "violations": violations, "changes": nil,
		"summary": map[string]any{"articleCount": len(b.Lines), "articlePrice": price,
			"fees":       map[string]any{"refund": deposit, "reusableBagSurcharge": bag, "serviceFee": 0},
			"totalPrice": price + deposit + bag, "totalArticleQuantity": count},
		"timeSlotInformation": map[string]any{"startTime": nil, "endTime": nil, "timeSlotText": ""},
	}
}

func (s *Server) handleBasket(w http.ResponseWriter, r *http.Request) {
	b := s.basketFor(r)
	if b == nil {
		gqlError(w, 404, "Not Found", "basket")
		return
	}
	data(w, map[string]any{"basket": s.basketJSON(b)})
}

func (s *Server) handleBasketSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Quantity      int  `json:"quantity"`
		BasketVersion *int `json:"basketVersion"`
	}
	if readBody(r, &req) != nil || req.BasketVersion == nil || req.Quantity < 0 {
		writeJSON(w, 400, map[string]any{"title": "Bad Request", "status": 400})
		return
	}
	b := s.basketFor(r)
	if b == nil || b.ID != r.PathValue("id") {
		gqlError(w, 404, "Not Found", "basket")
		return
	}
	if *req.BasketVersion != b.Version {
		gqlError(w, 409, "Conflict: basket version outdated", "basket")
		return
	}
	p, ok := findListing(r.PathValue("listing"))
	if !ok {
		gqlError(w, 404, "Listing not found", "basket")
		return
	}
	i := slices.IndexFunc(b.Lines, func(l line) bool { return l.ProductID == p.ID })
	switch {
	case req.Quantity == 0 && i >= 0:
		b.Lines = slices.Delete(b.Lines, i, i+1)
	case req.Quantity > 0 && i >= 0:
		b.Lines[i].Qty = req.Quantity
	case req.Quantity > 0:
		b.Lines = append(b.Lines, line{p.ID, req.Quantity})
	}
	b.Version++
	data(w, map[string]any{"basket": s.basketJSON(b)})
}

// ---- timeslots ----

func slotID(t time.Time, st string) string {
	h := sha256.Sum256([]byte(st + t.Format(time.RFC3339)))
	x := hex.EncodeToString(h[:16])
	return x[0:8] + "-" + x[8:12] + "-4" + x[13:16] + "-a" + x[17:20] + "-" + x[20:32]
}

func (s *Server) slots(serviceType string) []slot {
	var out []slot
	today := s.now()
	for d := 1; d <= 7; d++ {
		day := time.Date(today.Year(), today.Month(), today.Day()+d, 0, 0, 0, 0, time.Local)
		if day.Weekday() == time.Sunday {
			continue
		}
		for _, h := range []int{10, 14, 18} {
			start := day.Add(time.Duration(h) * time.Hour)
			fee := 0
			if serviceType == "DELIVERY" && day.Weekday() != time.Thursday {
				fee = DeliveryFee
			}
			if serviceType == "PICKUP" {
				fee = PickupFee
			}
			out = append(out, slot{ID: slotID(start, serviceType), Start: start, End: start.Add(4 * time.Hour), Fee: fee})
		}
	}
	return out
}

func slotJSON(sl slot) map[string]any {
	return map[string]any{"id": sl.ID, "startTime": sl.Start.Format(time.RFC3339), "endTime": sl.End.Format(time.RFC3339), "serviceFee": sl.Fee}
}

func (s *Server) handleTimeslots(w http.ResponseWriter, r *http.Request) {
	st := r.Header.Get("rd-service-types")
	out := []map[string]any{}
	for _, sl := range s.slots(st) {
		out = append(out, slotJSON(sl))
	}
	var free any
	if st == "DELIVERY" {
		free = map[string]any{"freeDeliveryDays": []string{"THURSDAY"}}
	}
	data(w, map[string]any{"getTimeslotsCheckout": out, "freeDeliveryInfo": free})
}

func (s *Server) findSlot(st, id string) (slot, bool) {
	for _, sl := range s.slots(st) {
		if sl.ID == id {
			return sl, true
		}
	}
	return slot{}, false
}

func (s *Server) handleReserve(w http.ResponseWriter, r *http.Request) {
	var req struct{ SlotID string }
	_ = readBody(r, &req)
	sl, ok := s.findSlot(r.Header.Get("rd-service-types"), req.SlotID)
	if !ok {
		gqlError(w, 404, "Timeslot not found", "createTimeslotReservation")
		return
	}
	exp := s.now().Add(60 * time.Minute)
	s.reserved[sl.ID] = exp
	data(w, map[string]any{"createTimeslotReservation": map[string]any{"slotId": sl.ID, "expireTime": exp.Format(time.RFC3339),
		"slotStartTime": sl.Start.UTC().Format(time.RFC3339), "slotEndTime": sl.End.UTC().Format(time.RFC3339), "fee": nil}})
}

// ---- checkout ----

func (s *Server) checkoutJSON(c *checkout, b *basket) map[string]any {
	bj := s.basketJSON(b)
	sum := bj["summary"].(map[string]any)
	var sl, pay, addr any
	if c.Slot != nil {
		sl = slotJSON(*c.Slot)
	}
	if c.Payment != "" {
		pay = map[string]any{"paymentMethod": c.Payment}
	}
	paymentType := "OFFLINE"
	if c.ServiceType == "DELIVERY" {
		paymentType = "ONLINE"
		addr = map[string]any{"id": "addr-0001", "version": "1", "firstName": "Erika", "lastName": "Musterfrau",
			"street": "Musterstraße", "houseNumber": "1", "zip": b.ZipCode, "city": "Musterstadt"}
	}
	params := url.Values{"serviceTypes": {c.ServiceType}, "totalOrderAmount": {strconv.Itoa(sum["totalPrice"].(int))},
		"marketCode": {b.MarketID}, "selectedPaymentOption": {c.Payment}}
	var orderID any
	if c.OrderID != "" {
		orderID = c.OrderID
	}
	return map[string]any{
		"checkout": map[string]any{"id": c.ID, "basketId": b.ID, "orderId": orderID, "marketId": b.MarketID, "zipCode": b.ZipCode,
			"serviceType": c.ServiceType, "isFreeOrder": false, "paymentType": paymentType, "timeslot": sl, "payment": pay,
			"selectedDeliveryAddress": addr, "redirectData": nil,
			"options": map[string]any{"checkoutType": c.ServiceType, "deliveryAddressRequired": c.ServiceType == "DELIVERY",
				"onlinePaymentSupported": c.ServiceType == "DELIVERY", "offlinePaymentSupported": c.ServiceType == "PICKUP",
				"paymentOptionsParams": "?" + params.Encode()}},
		"basket": map[string]any{"id": b.ID, "version": b.Version, "summary": sum},
	}
}

func (s *Server) handleCheckoutCreate(w http.ResponseWriter, r *http.Request) {
	var req struct{ BasketID string }
	_ = readBody(r, &req)
	b := s.basket
	if req.BasketID != b.ID {
		gqlError(w, 404, "Not Found", "checkout")
		return
	}
	if bj := s.basketJSON(b); len(b.Lines) == 0 || len(bj["violations"].([]map[string]any)) > 0 {
		gqlError(w, 412, "Precondition Failed", "checkout") // what REWE answers below the minimum order value
		return
	}
	for _, c := range s.checkouts {
		if c.BasketID == b.ID && c.OrderID == "" && c.ServiceType == b.ServiceType {
			c.Confirmed = false
			data(w, s.checkoutJSON(c, b))
			return
		}
	}
	c := &checkout{ID: s.uuid(), BasketID: b.ID, ServiceType: b.ServiceType}
	if b.ServiceType == "DELIVERY" {
		c.Payment = "PAYPAL" // last used payment method of the fake customer
	}
	s.checkouts[c.ID] = c
	data(w, s.checkoutJSON(c, b))
}

// checkoutBasket resolves a checkout and its (regular or modification) basket.
func (s *Server) checkoutBasket(w http.ResponseWriter, r *http.Request, field string) (*checkout, *basket) {
	c, ok := s.checkouts[r.PathValue("id")]
	if !ok {
		gqlError(w, 404, "Not Found", field)
		return nil, nil
	}
	b := s.basket
	if c.OrderID != "" {
		b = s.mods[c.BasketID]
		if b == nil {
			gqlError(w, 404, "Modification not found", field)
			return nil, nil
		}
	}
	return c, b
}

func (s *Server) handleCheckoutSlot(w http.ResponseWriter, r *http.Request) {
	c, b := s.checkoutBasket(w, r, "checkout")
	if c == nil {
		return
	}
	var req struct{ TimeslotID string }
	_ = readBody(r, &req)
	sl, ok := s.findSlot(c.ServiceType, req.TimeslotID)
	if exp, reserved := s.reserved[req.TimeslotID]; !ok || !reserved || s.now().After(exp) {
		gqlError(w, 409, "Timeslot not reserved", "checkout")
		return
	}
	c.Slot, c.Confirmed = &sl, false
	data(w, s.checkoutJSON(c, b))
}

var onlineMethods = []string{"DIRECT_DEBIT", "INVOICE", "APPLE_PAY", "GOOGLE_PAY", "CREDIT_CARD", "PAYPAL"}

func (s *Server) handleCheckoutPayment(w http.ResponseWriter, r *http.Request) {
	c, b := s.checkoutBasket(w, r, "checkout")
	if c == nil {
		return
	}
	var req struct{ PaymentOption string }
	_ = readBody(r, &req)
	valid := (c.ServiceType == "PICKUP" && req.PaymentOption == "MARKET_PAYMENT") ||
		(c.ServiceType == "DELIVERY" && slices.Contains(onlineMethods, req.PaymentOption))
	if !valid {
		gqlError(w, 400, "Payment option not available", "checkout")
		return
	}
	c.Payment, c.Confirmed = req.PaymentOption, false
	data(w, s.checkoutJSON(c, b))
}

func (s *Server) handlePaymentOptions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("totalOrderAmount") == "" {
		writeJSON(w, 200, map[string]any{"errors": []any{map[string]any{"message": "Variable 'totalOrderAmount' has coerced Null value for NonNull type 'Int!'",
			"extensions": map[string]any{"classification": "ValidationError"}}}})
		return
	}
	offline, online := []any{}, []any{}
	if q.Get("serviceTypes") == "PICKUP" {
		offline = append(offline, map[string]any{"key": "MARKET_PAYMENT", "variants": []string{"MASTERCARD", "VISA", "DEBIT_CARD", "CASH"}})
	} else {
		for _, k := range onlineMethods {
			m := map[string]any{"key": k, "variants": []string{k}, "savedPaymentData": nil}
			if k == "PAYPAL" && !s.noSaved {
				m["savedPaymentData"] = map[string]any{"id": 4242, "billingAgreementStatus": "VALID"}
			}
			online = append(online, m)
		}
	}
	data(w, map[string]any{"payment": map[string]any{
		"options":               map[string]any{"offlinePaymentMethods": offline, "enabledOnlinePaymentMethods": online, "disabledOnlinePaymentMethods": []any{}, "refusalReason": nil},
		"selectedPaymentOption": q.Get("selectedPaymentOption"),
	}})
}

func (s *Server) handleConfirm(w http.ResponseWriter, r *http.Request) {
	c, b := s.checkoutBasket(w, r, "checkout")
	if c == nil {
		return
	}
	if len(s.basketJSON(b)["violations"].([]map[string]any)) > 0 || len(b.Lines) == 0 {
		gqlError(w, 412, "Precondition Failed", "checkout")
		return
	}
	if c.Slot == nil || c.Payment == "" {
		gqlError(w, 412, "Precondition Failed", "checkout")
		return
	}
	c.Confirmed = true
	out := s.checkoutJSON(c, b)
	if c.Payment != "PAYPAL" && c.Payment != "MARKET_PAYMENT" {
		// no saved payment data: the customer has to enter it in a browser
		out["checkout"].(map[string]any)["redirectData"] = map[string]any{
			"url": "https://payment.fake.rewe.invalid/enter?checkout=" + c.ID, "method": "GET"}
	}
	data(w, out)
}

func (s *Server) handlePlaceOrder(w http.ResponseWriter, r *http.Request) {
	c, b := s.checkoutBasket(w, r, "order")
	if c == nil {
		return
	}
	if !c.Confirmed || (c.Payment != "PAYPAL" && c.Payment != "MARKET_PAYMENT") {
		gqlError(w, 412, "Precondition Failed", "order")
		return
	}
	if c.OrderID != "" { // submit a modification
		i := slices.IndexFunc(s.orders, func(o *order) bool { return o.ID == c.OrderID })
		if i < 0 {
			gqlError(w, 404, "Not Found", "order")
			return
		}
		s.orders[i].Lines = slices.Clone(b.Lines)
		delete(s.mods, b.ID)
		delete(s.checkouts, c.ID)
		data(w, map[string]any{"order": map[string]any{"orderId": c.OrderID}})
		return
	}
	o := &order{ID: s.orderID(), Date: s.now(), Lines: slices.Clone(b.Lines), Slot: *c.Slot, ServiceType: c.ServiceType, Payment: c.Payment, Status: "open"}
	s.orders = append(s.orders, o)
	b.Lines, b.Version = nil, b.Version+1
	delete(s.checkouts, c.ID)
	data(w, map[string]any{"order": map[string]any{"orderId": o.ID}})
}

// ---- orders & modifications ----

// modifiable: open orders can be changed until 12 hours before the slot starts.
func (s *Server) modifiable(o *order) bool {
	return o.Status == "open" && s.now().Before(o.Slot.Start.Add(-12*time.Hour))
}

func fmtDate(t time.Time) string { return t.Local().Format("200601021504") }

func (s *Server) actions(o *order) []string {
	switch {
	case s.modifiable(o):
		return []string{"modify", "cancel"}
	case o.Status == "open":
		return []string{}
	default:
		return []string{"reorder"}
	}
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	out := []map[string]any{}
	for i := len(s.orders) - 1; i >= 0; i-- {
		o := s.orders[i]
		price, deposit, _ := articlePrice(o.Lines)
		out = append(out, map[string]any{"orderId": o.ID, "orderValue": price + deposit, "orderDate": fmtDate(o.Date),
			"subOrders": []any{map[string]any{"isOpen": o.Status == "open", "status": o.Status, "orderActions": s.actions(o),
				"timeSlot": map[string]any{"firstSlotDate": fmtDate(o.Slot.Start), "lastSlotDate": fmtDate(o.Slot.End)}}}})
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("objectsPerPage")); err == nil && n < len(out) {
		out = out[:n]
	}
	data(w, map[string]any{"orderHistory": map[string]any{"orders": out}})
}

func (s *Server) findOrder(id string) *order {
	i := slices.IndexFunc(s.orders, func(o *order) bool { return o.ID == id })
	if i < 0 {
		return nil
	}
	return s.orders[i]
}

func (s *Server) handleOrder(w http.ResponseWriter, r *http.Request) {
	o := s.findOrder(r.PathValue("id"))
	if o == nil {
		gqlError(w, 404, "Not Found", "orderDetails")
		return
	}
	price, deposit, _ := articlePrice(o.Lines)
	items := []map[string]any{}
	for _, l := range o.Lines {
		p, _ := findProduct(l.ProductID)
		items = append(items, map[string]any{"lineItemType": "PRODUCT", "totalPrice": p.Price * l.Qty, "productId": p.ID,
			"title": p.Title, "quantity": l.Qty, "price": p.Price})
	}
	if deposit > 0 {
		items = append(items, map[string]any{"lineItemType": "DEPOSIT", "totalPrice": deposit})
	}
	data(w, map[string]any{"orderDetails": map[string]any{"orderId": o.ID, "orderDate": fmtDate(o.Date), "orderValue": price + deposit,
		"status": o.Status, "articlesPrice": price, "payment": map[string]any{"paymentMethod": o.Payment},
		"subOrders": []any{map[string]any{"status": o.Status, "lineItems": items, "orderActions": s.actions(o),
			"timeSlot": map[string]any{"firstSlotDate": fmtDate(o.Slot.Start), "lastSlotDate": fmtDate(o.Slot.End)}}}}})
}

func (s *Server) handleOrderCancel(w http.ResponseWriter, r *http.Request) {
	o := s.findOrder(r.PathValue("id"))
	if o == nil {
		gqlError(w, 404, "Not Found", "orderCancel")
		return
	}
	if o.Status == "open" && !s.modifiable(o) {
		gqlError(w, 409, "Order can no longer be cancelled", "orderCancel")
		return
	}
	if o.Status == "open" {
		o.Status = "cancelled"
	}
	data(w, map[string]any{"orderCancel": "OK"}) // like REWE: also OK on repeated calls
}

func (s *Server) handleModStart(w http.ResponseWriter, r *http.Request) {
	var req struct{ OrderID string }
	_ = readBody(r, &req)
	o := s.findOrder(req.OrderID)
	if o == nil {
		gqlError(w, 404, "Not Found", "checkout")
		return
	}
	if !s.modifiable(o) {
		gqlError(w, 409, "Order can no longer be modified", "checkout")
		return
	}
	b := &basket{ID: s.uuid(), Lines: slices.Clone(o.Lines), OrderID: o.ID, ServiceType: o.ServiceType,
		MarketID: r.Header.Get("rd-market-id"), ZipCode: r.Header.Get("rd-postcode")}
	s.mods[b.ID] = b
	c := &checkout{ID: s.uuid(), BasketID: b.ID, OrderID: o.ID, ServiceType: o.ServiceType, Payment: o.Payment, Slot: &o.Slot}
	s.checkouts[c.ID] = c
	data(w, map[string]any{"checkout": map[string]any{"id": c.ID, "orderId": o.ID}, "basket": s.basketJSON(b)})
}

func (s *Server) handleModCancel(w http.ResponseWriter, r *http.Request) {
	c, ok := s.checkouts[r.PathValue("id")]
	if !ok || c.OrderID == "" {
		gqlError(w, 404, "Not Found", "cancelOrderModification")
		return
	}
	delete(s.mods, c.BasketID)
	delete(s.checkouts, c.ID)
	data(w, map[string]any{"cancelOrderModification": nil})
}
