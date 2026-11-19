package rewe

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Market selects which market and service type requests are made for.
type Market struct {
	WwIdent     string
	ZipCode     string
	ServiceType string // PICKUP or DELIVERY
}

// TokenSource returns a valid bearer token.
type TokenSource func(ctx context.Context) (string, error)

// API is the REWE mobile API bound to a market.
type API struct {
	d      *Doer
	market Market
	token  TokenSource // nil for anonymous use
	mod    *Modification
}

// Modification identifies a running change of a placed order.
type Modification struct {
	OrderID    string
	CheckoutID string
	BasketID   string
}

// WithModification returns a client whose basket calls act on the modification basket.
func (a *API) WithModification(m *Modification) *API {
	c := *a
	c.mod = m
	return &c
}

// NewAPI creates an API client. token may be nil for endpoints that need no login.
func NewAPI(d *Doer, m Market, token TokenSource) *API {
	return &API{d: d, market: m, token: token}
}

// appHeaders are sent by the REWE app with every request.
var appHeaders = map[string]string{
	"rd-is-pickup-station": "false",
	"rd-is-lsfk":           "false",
	"rd-user-consent":      `{"conversionOptimization": 1}`,
}

// ErrNoMarket is returned by market-bound calls on an API without a market.
var ErrNoMarket = errors.New("no store selected - run 'korb store set' first")

func (a *API) headers(ctx context.Context, authed bool) (http.Header, error) {
	h := http.Header{}
	for k, v := range appHeaders {
		h.Set(k, v)
	}
	if a.market.WwIdent != "" {
		h.Set("rd-market-id", a.market.WwIdent)
		h.Set("rd-postcode", a.market.ZipCode)
		h.Set("rd-service-types", a.market.ServiceType)
	}
	if a.mod != nil {
		h.Set("rd-modified-order-id", a.mod.OrderID)
		h.Set("rd-basket-id", a.mod.BasketID)
	}
	if authed {
		if a.token == nil {
			return nil, errors.New("this command requires a login - run 'korb login'")
		}
		tkn, err := a.token(ctx)
		if err != nil {
			return nil, err
		}
		h.Set("Authorization", "Bearer "+tkn)
	}
	return h, nil
}

func doCall[T any](ctx context.Context, a *API, method, path string, authed, needsMarket bool, q url.Values, body any) (*T, error) {
	if needsMarket && a.market.WwIdent == "" {
		return nil, ErrNoMarket
	}
	h, err := a.headers(ctx, authed)
	if err != nil {
		return nil, err
	}
	return call[T](ctx, a.d, Request{Method: method, URL: BaseURL + path, Query: q, Header: h, JSON: body})
}

func seg(s string) string { return url.PathEscape(s) }

// ---- Markets (no login, no market needed) ----

// ServicePortfolio returns pickup markets and the delivery market for a zip code.
func (a *API) ServicePortfolio(ctx context.Context, zip string) (*ServicePortfolio, error) {
	res, err := doCall[struct {
		ServicePortfolio *ServicePortfolio `json:"servicePortfolio"`
	}](ctx, a, http.MethodGet, "/service-portfolio/"+seg(zip), false, false, nil, nil)
	if err != nil {
		return nil, err
	}
	if res.ServicePortfolio == nil {
		return &ServicePortfolio{CustomerZipCode: zip, PickupMarkets: []PickupMarket{}}, nil
	}
	if res.ServicePortfolio.PickupMarkets == nil {
		res.ServicePortfolio.PickupMarkets = []PickupMarket{}
	}
	return res.ServicePortfolio, nil
}

// MarketDetails looks a market up for the given service type. It returns
// (nil, nil) if REWE does not know the market for that service type.
func (a *API) MarketDetails(ctx context.Context, wwIdent, serviceType string) (*MarketDetails, error) {
	res, err := doCall[struct {
		MarketDetails *struct {
			Market *MarketDetails `json:"market"`
		} `json:"marketDetails"`
	}](ctx, a, http.MethodGet, "/markets/"+seg(wwIdent)+"/"+seg(serviceType), false, false, nil, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if res.MarketDetails == nil {
		return nil, nil
	}
	return res.MarketDetails.Market, nil
}

// ---- Products ----

// SearchAttributes supported as search filters.
var SearchAttributes = []string{"organic", "regional", "vegan", "vegetarian"}

// Search products by name or EAN. Needs a market but no login.
func (a *API) Search(ctx context.Context, query string, attributes, categorySlugs []string, page int) ([]Product, error) {
	filters := make([]string, 0, len(attributes)+len(categorySlugs))
	for _, at := range attributes {
		filters = append(filters, "attribute="+at)
	}
	for _, s := range categorySlugs {
		filters = append(filters, "categorySlug="+s)
	}
	q := url.Values{"query": {query}}
	if len(filters) > 0 {
		q.Set("filters", strings.Join(filters, "&"))
	}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	res, err := doCall[struct {
		Products productList `json:"products"`
	}](ctx, a, http.MethodGet, "/products", false, true, q, nil)
	if err != nil {
		return nil, err
	}
	return nonNil(res.Products.Products), nil
}

// PurchasedProducts lists products the customer bought before.
func (a *API) PurchasedProducts(ctx context.Context) ([]Product, error) {
	res, err := doCall[struct {
		PurchasedProducts productList `json:"purchasedProducts"`
	}](ctx, a, http.MethodGet, "/purchased-products", true, true, nil, nil)
	if err != nil {
		return nil, err
	}
	return nonNil(res.PurchasedProducts.Products), nil
}

// Categories returns the shop category tree.
func (a *API) Categories(ctx context.Context) ([]Category, error) {
	res, err := doCall[struct {
		Categories []Category `json:"categories"`
	}](ctx, a, http.MethodGet, "/shop-categories", true, true, nil, nil)
	if err != nil {
		return nil, err
	}
	return nonNil(res.Categories), nil
}

// ---- Favorites ----

func (a *API) FavoriteLists(ctx context.Context) ([]FavoriteList, error) {
	res, err := doCall[struct {
		FavoriteLists struct {
			Favorites []FavoriteList `json:"favorites"`
		} `json:"favoriteLists"`
	}](ctx, a, http.MethodGet, "/favorites", true, true, nil, nil)
	if err != nil {
		return nil, err
	}
	return nonNil(res.FavoriteLists.Favorites), nil
}

func (a *API) AddFavorite(ctx context.Context, listID, listingID, productID string) (*FavoriteList, error) {
	body := map[string]any{"listingId": listingID, "quantity": nil, "productId": productID}
	res, err := doCall[struct {
		List FavoriteList `json:"addLineItemToFavoriteList"`
	}](ctx, a, http.MethodPost, "/favorites/"+seg(listID)+"/lineitems", true, true, nil, body)
	if err != nil {
		return nil, err
	}
	return &res.List, nil
}

func (a *API) DeleteFavorite(ctx context.Context, listID, itemID string) error {
	h, err := a.headers(ctx, true)
	if err != nil {
		return err
	}
	_, err = a.d.Do(ctx, Request{Method: http.MethodDelete, URL: BaseURL + "/favorites/" + seg(listID) + "/lineitems/" + seg(itemID), Header: h})
	return err
}

// ---- Basket ----

// Basket returns (and if necessary creates) the customer's basket.
func (a *API) Basket(ctx context.Context) (*Basket, error) {
	res, err := doCall[struct {
		Basket Basket `json:"basket"`
	}](ctx, a, http.MethodPost, "/baskets", true, true, nil, map[string]any{"includeTimeslot": true})
	if err != nil {
		return nil, err
	}
	return &res.Basket, nil
}

// SetQuantity sets the absolute quantity of a listing (0 removes it).
func (a *API) SetQuantity(ctx context.Context, basketID, listingID string, qty, version int) (*Basket, error) {
	body := map[string]any{"quantity": qty, "basketVersion": version, "includeTimeslot": true}
	res, err := doCall[struct {
		Basket Basket `json:"basket"`
	}](ctx, a, http.MethodPost, "/baskets/"+seg(basketID)+"/listings/"+seg(listingID), true, true, nil, body)
	if err != nil {
		return nil, err
	}
	return &res.Basket, nil
}

// ---- Timeslots & checkout ----

func (a *API) Timeslots(ctx context.Context) (*TimeslotsResponse, error) {
	res, err := doCall[TimeslotsResponse](ctx, a, http.MethodGet, "/timeslots/checkout", true, true, nil, nil)
	if err != nil {
		return nil, err
	}
	res.Timeslots = nonNil(res.Timeslots)
	for i := range res.Timeslots {
		res.Timeslots[i].StartTime = res.Timeslots[i].StartTime.Local()
		res.Timeslots[i].EndTime = res.Timeslots[i].EndTime.Local()
	}
	return res, nil
}

func (a *API) CreateCheckout(ctx context.Context, basketID string) (*CheckoutResponse, error) {
	body := map[string]any{"basketId": basketID, "loadBonusCredit": false}
	return doCall[CheckoutResponse](ctx, a, http.MethodPost, "/checkouts", true, true, nil, body)
}

func (a *API) ReserveTimeslot(ctx context.Context, timeslotID string) (*TimeslotReservation, error) {
	res, err := doCall[struct {
		R TimeslotReservation `json:"createTimeslotReservation"`
	}](ctx, a, http.MethodPost, "/timeslots/reservations", true, true, nil, map[string]any{"slotId": timeslotID})
	if err != nil {
		return nil, err
	}
	return &res.R, nil
}

func (a *API) SetCheckoutTimeslot(ctx context.Context, checkoutID, basketID, timeslotID string) (*CheckoutResponse, error) {
	body := map[string]any{"basketId": basketID, "timeslotId": timeslotID}
	return doCall[CheckoutResponse](ctx, a, http.MethodPatch, "/checkouts/"+seg(checkoutID)+"/timeslots", true, true, nil, body)
}

// SetPayment selects the payment option (MARKET_PAYMENT, PAYPAL, CREDIT_CARD, ...).
func (a *API) SetPayment(ctx context.Context, checkoutID, basketID, option string) (*CheckoutResponse, error) {
	body := map[string]any{"basketId": basketID, "paymentOption": option}
	return doCall[CheckoutResponse](ctx, a, http.MethodPatch, "/checkouts/"+seg(checkoutID)+"/payments", true, true, nil, body)
}

// PaymentOptions lists payment methods; params is CheckoutOptions.PaymentOptionsParams.
func (a *API) PaymentOptions(ctx context.Context, params string) (*PaymentOptions, error) {
	q, err := url.ParseQuery(strings.TrimPrefix(params, "?"))
	if err != nil {
		return nil, fmt.Errorf("invalid paymentOptionsParams: %w", err)
	}
	res, err := doCall[struct {
		Payment PaymentOptions `json:"payment"`
	}](ctx, a, http.MethodGet, "/payment-options", true, true, q, nil)
	if err != nil {
		return nil, err
	}
	return &res.Payment, nil
}

func (a *API) ConfirmCheckout(ctx context.Context, checkoutID, basketID string) (*CheckoutResponse, error) {
	body := map[string]any{"basketId": basketID}
	return doCall[CheckoutResponse](ctx, a, http.MethodPost, "/checkouts/"+seg(checkoutID)+"/confirmations", true, true, nil, body)
}

func (a *API) PlaceOrder(ctx context.Context, checkoutID string) (*OrderResponse, error) {
	return doCall[OrderResponse](ctx, a, http.MethodPost, "/checkouts/"+seg(checkoutID)+"/orders", true, true, nil, map[string]any{})
}

// ---- Order modifications ----

// OrderModification is the result of starting to change a placed order.
type OrderModification struct {
	Checkout struct {
		ID      string `json:"id"`
		OrderID string `json:"orderId"`
	} `json:"checkout"`
	Basket Basket `json:"basket"`
}

// StartOrderModification opens a placed order for changes. REWE returns a
// separate basket (with orderId set) and checkout for the modification.
func (a *API) StartOrderModification(ctx context.Context, orderID string) (*OrderModification, error) {
	return doCall[OrderModification](ctx, a, http.MethodPost, "/checkouts/order-modifications", true, true, nil, map[string]any{"orderId": orderID})
}

// CancelOrderModification discards the changes; the original order stays as it was.
func (a *API) CancelOrderModification(ctx context.Context, checkoutID string) error {
	_, err := doCall[map[string]any](ctx, a, http.MethodPost, "/checkouts/"+seg(checkoutID)+"/cancel-order-modify", true, true, nil, map[string]any{})
	return err
}

// ---- Orders ----

func (a *API) OrderHistory(ctx context.Context, perPage int) ([]OrderHistoryEntry, error) {
	var q url.Values
	if perPage > 0 {
		q = url.Values{"objectsPerPage": {strconv.Itoa(perPage)}}
	}
	res, err := doCall[struct {
		OrderHistory struct {
			Orders []OrderHistoryEntry `json:"orders"`
		} `json:"orderHistory"`
	}](ctx, a, http.MethodGet, "/orders/history", true, true, q, nil)
	if err != nil {
		return nil, err
	}
	return nonNil(res.OrderHistory.Orders), nil
}

func (a *API) Order(ctx context.Context, orderID string) (*OrderDetail, error) {
	res, err := doCall[struct {
		OrderDetails OrderDetail `json:"orderDetails"`
	}](ctx, a, http.MethodGet, "/orders/"+seg(orderID), true, true, nil, nil)
	if err != nil {
		return nil, err
	}
	return &res.OrderDetails, nil
}

func (a *API) CancelOrder(ctx context.Context, orderID string) (*OrderCancelResponse, error) {
	return doCall[OrderCancelResponse](ctx, a, http.MethodDelete, "/orders/"+seg(orderID), true, true, nil, nil)
}

// ---- eBons ----

// Ebons lists digital receipts. Receipts are not bound to the selected market.
func (a *API) Ebons(ctx context.Context) ([]EbonEntry, error) {
	res, err := doCall[struct {
		GetEbons struct {
			Items []EbonEntry `json:"items"`
		} `json:"getEbons"`
	}](ctx, a, http.MethodGet, "/ebons", true, false, nil, nil)
	if err != nil {
		return nil, err
	}
	return nonNil(res.GetEbons.Items), nil
}

// ReceiptPDF downloads an eBon as PDF.
func (a *API) ReceiptPDF(ctx context.Context, ebonID string) ([]byte, error) {
	h, err := a.headers(ctx, true)
	if err != nil {
		return nil, err
	}
	h.Set("Accept", "application/pdf")
	b, err := a.d.Do(ctx, Request{Method: http.MethodGet, URL: BaseURL + "/receipts/" + seg(ebonID) + "/pdf", Header: h})
	if err != nil {
		return nil, err
	}
	if len(b) < 4 || string(b[:4]) != "%PDF" {
		return nil, fmt.Errorf("receipt %s: response is not a PDF", ebonID)
	}
	return b, nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
