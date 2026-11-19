package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miraassist/rewe-cli/internal/auth"
	"github.com/miraassist/rewe-cli/internal/config"
	"github.com/miraassist/rewe-cli/internal/rewe"
	"github.com/miraassist/rewe-cli/internal/suggest"
)

// ---- store ----

func (ap *app) cmdStore() error {
	if err := ap.only(); err != nil {
		return err
	}
	switch ap.sub() {
	case "":
		s, err := config.ReadStore()
		if err != nil {
			return err
		}
		return ap.print(s)
	case "search":
		if err := ap.args.want(1, "store search <ZIP>"); err != nil {
			return err
		}
		markets, _, err := ap.marketsFor(ap.args.pos[0])
		if err != nil {
			return err
		}
		return ap.print(markets)
	case "set":
		if err := ap.args.want(2, "store set <ID> <ZIP>"); err != nil {
			return err
		}
		return ap.storeSet(ap.args.pos[0], ap.args.pos[1])
	case "info":
		if err := ap.args.want(1, "store info <ID>"); err != nil {
			return err
		}
		api := ap.anonymousAPI()
		pickup, err := api.MarketDetails(ap.ctx, ap.args.pos[0], config.ServicePickup)
		if err != nil {
			return err
		}
		delivery, err := api.MarketDetails(ap.ctx, ap.args.pos[0], config.ServiceDelivery)
		if err != nil {
			return err
		}
		return ap.print(map[string]*rewe.MarketDetails{"pickup": pickup, "delivery": delivery})
	default:
		return errUsage
	}
}

// marketsFor lists pickup markets plus the delivery service for a zip code.
func (ap *app) marketsFor(zip string) ([]rewe.PickupMarket, *rewe.ServicePortfolio, error) {
	api := ap.anonymousAPI()
	pf, err := api.ServicePortfolio(ap.ctx, zip)
	if err != nil {
		return nil, nil, err
	}
	out := make([]rewe.PickupMarket, 0, len(pf.PickupMarkets)+1)
	for _, m := range pf.PickupMarkets {
		m.ServiceType = config.ServicePickup
		out = append(out, m)
	}
	if pf.DeliveryMarket != nil && pf.DeliveryMarket.WwIdent != "" {
		d := rewe.PickupMarket{
			WwIdent:     pf.DeliveryMarket.WwIdent,
			DisplayName: "REWE Lieferservice",
			ZipCode:     zip,
			City:        "Lieferservice",
			ServiceType: config.ServiceDelivery,
		}
		if det, err := api.MarketDetails(ap.ctx, d.WwIdent, config.ServiceDelivery); err == nil && det != nil && det.DisplayName != "" {
			d.DisplayName = det.DisplayName
		}
		out = append(out, d)
	}
	return out, pf, nil
}

func (ap *app) storeSet(id, zip string) error {
	s, err := ap.chooseStore(id, zip)
	if err != nil {
		return err
	}
	return ap.print(s)
}

// chooseStore selects a market or the delivery service for a zip code and
// stores it.
func (ap *app) chooseStore(id, zip string) (config.Store, error) {
	markets, _, err := ap.marketsFor(zip)
	if err != nil {
		return config.Store{}, err
	}
	for _, m := range markets {
		if m.WwIdent == id {
			s := config.Store{WwIdent: id, ZipCode: zip, ServiceType: m.ServiceType}
			return s, config.WriteStore(s)
		}
	}
	// Not offered for this zip code: explain why as precisely as possible.
	pickup, err := ap.anonymousAPI().MarketDetails(ap.ctx, id, config.ServicePickup)
	if err != nil {
		return config.Store{}, err
	}
	if pickup != nil {
		return config.Store{}, fmt.Errorf("market %s (%s, %s %s) offers pickup, but REWE does not list it for ZIP %s - try 'korb store set %s %s'",
			id, pickup.StreetWithHouseNumber, pickup.ZipCode, pickup.City, zip, id, pickup.ZipCode)
	}
	ids := make([]string, 0, len(markets))
	for _, m := range markets {
		ids = append(ids, fmt.Sprintf("%s (%s %s)", m.WwIdent, m.ServiceType, strings.TrimSpace(m.City)))
	}
	return config.Store{}, fmt.Errorf("REWE offers neither pickup nor delivery from market %s for ZIP %s (it may be a store without online ordering); available: %s",
		id, zip, strings.Join(ids, ", "))
}

// ---- products ----

func (ap *app) cmdSearch() error {
	if err := ap.only("organic", "regional", "vegan", "vegetarian", "category", "page"); err != nil {
		return err
	}
	if err := ap.args.want(1, "search <QUERY|EAN> [--organic] [--regional] [--vegan] [--vegetarian] [--category SLUG]... [--page N]"); err != nil {
		return err
	}
	page, err := ap.args.intValue("page", 1)
	if err != nil {
		return err
	}
	var attrs []string
	for _, at := range rewe.SearchAttributes {
		if ap.args.bools[at] {
			attrs = append(attrs, at)
		}
	}
	api, _, err := ap.api()
	if err != nil {
		return err
	}
	products, err := api.Search(ap.ctx, ap.args.pos[0], attrs, ap.args.values["category"], page)
	if err != nil {
		return err
	}
	return ap.print(products)
}

func (ap *app) cmdCategories() error {
	if err := ap.only(); err != nil {
		return err
	}
	if err := ap.args.want(0, "categories"); err != nil {
		return err
	}
	api, _, err := ap.api()
	if err != nil {
		return err
	}
	cats, err := api.Categories(ap.ctx)
	if err != nil {
		return err
	}
	return ap.print(cats)
}

// ---- favorites ----

func (ap *app) cmdFavorites() error {
	if err := ap.only(); err != nil {
		return err
	}
	api, _, err := ap.api()
	if err != nil {
		return err
	}
	sub := ap.sub()
	switch sub {
	case "", "search":
		filter := ""
		if sub == "search" {
			if err := ap.args.want(1, "favorites search <QUERY>"); err != nil {
				return err
			}
			filter = strings.ToLower(ap.args.pos[0])
		} else if err := ap.args.want(0, "favorites"); err != nil {
			return err
		}
		lists, err := api.FavoriteLists(ap.ctx)
		if err != nil {
			return err
		}
		out := []rewe.Product{}
		for _, l := range lists {
			for _, p := range l.Items {
				if filter == "" || strings.Contains(strings.ToLower(p.Title), filter) {
					out = append(out, p)
				}
			}
		}
		return ap.print(out)
	case "add":
		if err := ap.args.want(2, "favorites add <LISTING_ID> <PRODUCT_ID>"); err != nil {
			return err
		}
		listID, err := defaultFavoriteList(ap.ctx, api)
		if err != nil {
			return err
		}
		listingID, productID := ap.args.pos[0], ap.args.pos[1]
		l, err := api.AddFavorite(ap.ctx, listID, listingID, productID)
		if err != nil {
			return err
		}
		for _, p := range l.Items {
			if p.ProductID == productID {
				return ap.print(p)
			}
		}
		return errors.New("item was not added to favorites")
	case "delete":
		if err := ap.args.want(1, "favorites delete <ITEM_ID>"); err != nil {
			return err
		}
		listID, err := defaultFavoriteList(ap.ctx, api)
		if err != nil {
			return err
		}
		if err := api.DeleteFavorite(ap.ctx, listID, ap.args.pos[0]); err != nil {
			return err
		}
		return ap.print(map[string]string{"deleted": ap.args.pos[0]})
	default:
		return errUsage
	}
}

func defaultFavoriteList(ctx context.Context, api *rewe.API) (string, error) {
	lists, err := api.FavoriteLists(ctx)
	if err != nil {
		return "", err
	}
	if len(lists) == 0 {
		return "", errors.New("no favorite list found")
	}
	return lists[0].ID, nil
}

// ---- basket ----

func (ap *app) cmdBasket() error {
	switch ap.sub() {
	case "":
		if err := ap.only(); err != nil {
			return err
		}
		if err := ap.args.want(0, "basket"); err != nil {
			return err
		}
		api, mod, err := ap.basketAPI()
		if err != nil {
			return err
		}
		b, err := ap.currentBasket(api, mod)
		if err != nil {
			return err
		}
		return ap.print(b)
	case "add":
		if err := ap.only("qty"); err != nil {
			return err
		}
		if err := ap.args.want(1, "basket add <LISTING_ID> [--qty N]"); err != nil {
			return err
		}
		qty, err := ap.args.intValue("qty", 1)
		if err != nil {
			return err
		}
		if qty < 0 {
			return errors.New("--qty must be >= 0")
		}
		api, mod, err := ap.basketAPI()
		if err != nil {
			return err
		}
		listingID := ap.args.pos[0]
		b, err := ap.setQuantity(api, mod, listingID, qty)
		if err != nil {
			return err
		}
		for _, li := range b.LineItems {
			if li.Product.Listing.ListingID == listingID {
				if qty == 0 {
					return fmt.Errorf("item %s is still in the basket", listingID)
				}
				return ap.print(li)
			}
		}
		if qty == 0 {
			return ap.print(nil)
		}
		msg := "item was not added to basket"
		for _, c := range b.Changes {
			msg += "; " + c.Message
		}
		return errors.New(msg)
	default:
		return errUsage
	}
}

// setQuantity sets the quantity of a listing in the current (or the
// modification's) basket and returns the new basket.
func (ap *app) setQuantity(api *rewe.API, mod *config.Modification, listingID string, qty int) (*rewe.Basket, error) {
	var b *rewe.Basket
	for attempt := 0; ; attempt++ {
		cur, err := ap.currentBasket(api, mod)
		if err != nil {
			return nil, err
		}
		b, err = api.SetQuantity(ap.ctx, cur.ID, listingID, qty, cur.Version)
		var apiErr *rewe.APIError
		// Basket changed concurrently (version mismatch): reload and retry once.
		if attempt == 0 && errors.As(err, &apiErr) && (apiErr.Status == http.StatusConflict || apiErr.Status == http.StatusPreconditionFailed) {
			continue
		}
		if err != nil {
			return nil, err
		}
		break
	}
	return b, saveModBasket(mod, b)
}

// ---- timeslots & checkout ----

func (ap *app) cmdTimeslots() error {
	if err := ap.only(); err != nil {
		return err
	}
	if err := ap.args.want(0, "timeslots"); err != nil {
		return err
	}
	api, _, err := ap.api()
	if err != nil {
		return err
	}
	ts, err := api.Timeslots(ap.ctx)
	if err != nil {
		return err
	}
	return ap.print(ts)
}

func (ap *app) cmdCheckout() error {
	api, store, err := ap.api()
	if err != nil {
		return err
	}
	if mod, err := config.ReadModification(); err != nil {
		return err
	} else if mod != nil {
		return fmt.Errorf("order %s is being modified - finish with 'korb orders modify submit' or 'korb orders modify cancel' first", mod.OrderID)
	}
	switch ap.sub() {
	case "":
		if err := ap.only(); err != nil {
			return err
		}
		if err := ap.args.want(0, "checkout"); err != nil {
			return err
		}
		co, err := createCheckout(ap.ctx, api)
		if err != nil {
			return err
		}
		return ap.print(co)
	case "payment-options":
		if err := ap.only(); err != nil {
			return err
		}
		if err := ap.args.want(0, "checkout payment-options"); err != nil {
			return err
		}
		co, err := createCheckout(ap.ctx, api)
		if err != nil {
			return err
		}
		methods, err := paymentMethods(ap.ctx, api, co)
		if err != nil {
			return err
		}
		return ap.print(methods)
	case "create":
		if err := ap.only("payment"); err != nil {
			return err
		}
		if err := ap.args.want(1, "checkout create <TIMESLOT_ID> [--payment KEY]"); err != nil {
			return err
		}
		out, err := prepareCheckout(ap.ctx, api, store, ap.args.pos[0], ap.args.value("payment", ""))
		if err != nil {
			return err
		}
		return ap.print(out)
	case "order":
		if err := ap.only("max-total"); err != nil {
			return err
		}
		if err := ap.args.want(0, "checkout order [--max-total CENTS]"); err != nil {
			return err
		}
		limit, err := ap.orderLimit()
		if err != nil {
			return err
		}
		out, err := ap.placeOrder(api, limit, 0)
		if err != nil {
			return err
		}
		return ap.print(out)
	default:
		return errUsage
	}
}

// prepareCheckout creates the checkout, reserves the slot and selects the
// payment method (explicit, else market payment for pickup, else a saved one).
func prepareCheckout(ctx context.Context, api *rewe.API, store config.Store, slot, payment string) (*rewe.CheckoutResponse, error) {
	if !isUUID(slot) {
		return nil, errors.New("invalid timeslot ID (expected UUID, get IDs from korb timeslots)")
	}
	co, err := createCheckout(ctx, api)
	if err != nil {
		return nil, err
	}
	payment, err = choosePayment(ctx, api, store, co, payment)
	if err != nil {
		return nil, err
	}
	res, err := api.ReserveTimeslot(ctx, slot)
	if err != nil {
		return nil, err
	}
	if _, err := api.SetCheckoutTimeslot(ctx, co.Checkout.ID, co.Basket.ID, res.SlotID); err != nil {
		return nil, err
	}
	return api.SetPayment(ctx, co.Checkout.ID, co.Basket.ID, payment)
}

func createCheckout(ctx context.Context, api *rewe.API) (*rewe.CheckoutResponse, error) {
	b, err := api.Basket(ctx)
	if err != nil {
		return nil, err
	}
	if err := checkoutableBasket(b); err != nil {
		return nil, err
	}
	co, err := api.CreateCheckout(ctx, b.ID)
	var apiErr *rewe.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusPreconditionFailed {
		return nil, fmt.Errorf("REWE refused the checkout (412 Precondition Failed) - check 'korb basket' for violations: %w", err)
	}
	return co, err
}

// checkoutableBasket reports why a basket cannot be checked out (REWE itself
// only answers 412 without details).
func checkoutableBasket(b *rewe.Basket) error {
	if len(b.LineItems) == 0 {
		return errors.New("basket is empty - can't checkout an empty basket")
	}
	if len(b.Violations) == 0 {
		return nil
	}
	msgs := make([]string, 0, len(b.Violations))
	for _, v := range b.Violations {
		m := fmt.Sprintf("%s (%s)", v.Message, v.ID)
		if missing := missingForMinimum(b); v.ID == "minimum.delivery.not.reached" && missing > 0 {
			m += fmt.Sprintf(" - es fehlen %s Warenwert (ohne Pfand)", euro(missing))
		}
		msgs = append(msgs, m)
	}
	return fmt.Errorf("basket cannot be ordered yet: %s", strings.Join(msgs, "; "))
}

// paymentMethodInfo is the agent-facing view of a payment method.
type paymentMethodInfo struct {
	Key      string   `json:"key"`
	Variants []string `json:"variants"`
	Online   bool     `json:"online"`
	// Usable means korb can pay with it without any customer interaction:
	// offline payment, or an online method with saved, valid payment data.
	Usable   bool   `json:"usable"`
	Saved    bool   `json:"saved"`
	Status   string `json:"status,omitempty"`
	Selected bool   `json:"selected"`
}

func paymentMethods(ctx context.Context, api *rewe.API, co *rewe.CheckoutResponse) ([]paymentMethodInfo, error) {
	if co.Checkout.Options == nil || co.Checkout.Options.PaymentOptionsParams == "" {
		return nil, errors.New("checkout does not expose payment options")
	}
	po, err := api.PaymentOptions(ctx, co.Checkout.Options.PaymentOptionsParams)
	if err != nil {
		return nil, err
	}
	selected := ""
	if co.Checkout.Payment != nil {
		selected = co.Checkout.Payment.PaymentMethod
	}
	out := []paymentMethodInfo{}
	for _, m := range po.Options.OfflinePaymentMethods {
		out = append(out, paymentMethodInfo{Key: m.Key, Variants: m.Variants, Usable: true, Selected: m.Key == selected})
	}
	for _, m := range po.Options.EnabledOnlinePaymentMethods {
		i := paymentMethodInfo{Key: m.Key, Variants: m.Variants, Online: true, Selected: m.Key == selected}
		if m.SavedPaymentData != nil {
			i.Saved = true
			i.Status = m.SavedPaymentData.BillingAgreementStatus
			i.Usable = i.Status == "" || i.Status == "VALID"
		}
		out = append(out, i)
	}
	return out, nil
}

// choosePayment returns the payment option for a checkout: MARKET_PAYMENT for
// pickup, otherwise the requested or a saved, usable online method (PayPal first).
func choosePayment(ctx context.Context, api *rewe.API, store config.Store, co *rewe.CheckoutResponse, requested string) (string, error) {
	requested = strings.ToUpper(requested)
	if store.ServiceType == config.ServicePickup && (requested == "" || requested == "MARKET_PAYMENT") {
		return "MARKET_PAYMENT", nil
	}
	methods, err := paymentMethods(ctx, api, co)
	if err != nil {
		return "", err
	}
	return pickPayment(methods, requested)
}

// envOrderLimit combines $KORB_MAX_TOTAL (cents) and $KORB_MAX_TOTAL_EURO
// (euros); the lower one wins, 0 = no limit.
func envOrderLimit() (int, error) {
	limit := 0
	if env := os.Getenv("KORB_MAX_TOTAL"); env != "" {
		n, err := strconv.Atoi(env)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("KORB_MAX_TOTAL must be a positive amount in cents, got %q", env)
		}
		limit = n
	}
	if env := os.Getenv("KORB_MAX_TOTAL_EURO"); env != "" {
		n, err := parseEuro(env)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("KORB_MAX_TOTAL_EURO must be a positive amount in euros, got %q", env)
		}
		if limit == 0 || n < limit {
			limit = n
		}
	}
	return limit, nil
}

// parseEuro reads an amount like "63,40", "63.40 €", "150" into cents.
func parseEuro(s string) (int, error) {
	t := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "€"))
	t = strings.TrimSpace(strings.TrimSuffix(t, "EUR"))
	t = strings.ReplaceAll(t, ",", ".")
	whole, frac, hasFrac := strings.Cut(t, ".")
	if whole == "" || strings.ContainsAny(frac, ".") || len(frac) > 2 || (hasFrac && frac == "") {
		return 0, fmt.Errorf("%q is not an amount in euros", s)
	}
	for len(frac) < 2 {
		frac += "0"
	}
	w, err1 := strconv.Atoi(whole)
	f, err2 := strconv.Atoi(frac)
	if err1 != nil || err2 != nil || w < 0 || strings.HasPrefix(whole, "+") {
		return 0, fmt.Errorf("%q is not an amount in euros", s)
	}
	return w*100 + f, nil
}

// orderLimit combines --max-total and the environment (the lower one wins); 0 = no limit.
func (ap *app) orderLimit() (int, error) {
	limit, err := envOrderLimit()
	if err != nil {
		return 0, err
	}
	n, err := ap.args.intValue("max-total", 0)
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, errors.New("--max-total must be a positive amount in cents")
	}
	if n > 0 && (limit == 0 || n < limit) {
		limit = n
	}
	return limit, nil
}

// paymentActionRequired is returned instead of an order when REWE wants the
// customer to approve the payment in a browser.
type paymentActionRequired struct {
	Status       string          `json:"status"`
	RedirectData json.RawMessage `json:"redirectData"`
	Next         string          `json:"next"`
}

// placeOrder orders the prepared checkout. expected (cents, 0 = unchecked)
// must equal the checkout's total: an agent orders exactly what it announced.
func (ap *app) placeOrder(api *rewe.API, limit, expected int) (any, error) {
	co, err := createCheckout(ap.ctx, api)
	if err != nil {
		return nil, err
	}
	var methods []paymentMethodInfo
	if co.Checkout.Payment != nil && co.Checkout.Payment.PaymentMethod != "MARKET_PAYMENT" {
		if methods, err = paymentMethods(ap.ctx, api, co); err != nil {
			return nil, err
		}
	}
	if err := checkOrderable(co, methods, limit); err != nil {
		return nil, err
	}
	if err := checkExpected(co.Basket.Summary.TotalPrice, expected); err != nil {
		return nil, err
	}
	confirmed, err := api.ConfirmCheckout(ap.ctx, co.Checkout.ID, co.Basket.ID)
	if err != nil {
		return nil, err
	}
	if rd := confirmed.Checkout.RedirectData; len(rd) > 0 && string(rd) != "null" {
		// REWE wants the customer to approve the payment in a browser; don't place the order blindly.
		return &paymentActionRequired{"payment_action_required", rd,
			"complete the payment step in a browser / the REWE app; nothing was ordered by korb"}, nil
	}
	return api.PlaceOrder(ap.ctx, co.Checkout.ID)
}

// checkExpected refuses when the total differs from the one announced.
func checkExpected(total, expected int) error {
	if expected > 0 && total != expected {
		return fmt.Errorf("the total is %s, not %s - nothing was ordered; tell the user the new total and ask again", euro(total), euro(expected))
	}
	return nil
}

// checkOrderable refuses orders that korb must not place on its own.
func checkOrderable(co *rewe.CheckoutResponse, methods []paymentMethodInfo, limit int) error {
	if co.Checkout.Timeslot == nil {
		return errors.New("no timeslot set - run 'korb checkout create <TIMESLOT_ID>' first")
	}
	if co.Checkout.Payment == nil || co.Checkout.Payment.PaymentMethod == "" {
		return errors.New("no payment method set - run 'korb checkout create <TIMESLOT_ID>' first")
	}
	if total := co.Basket.Summary.TotalPrice; limit > 0 && total > limit {
		return fmt.Errorf("order total %s exceeds the limit of %s - nothing was ordered", euro(total), euro(limit))
	}
	method := co.Checkout.Payment.PaymentMethod
	if method == "MARKET_PAYMENT" {
		return nil
	}
	i := slices.IndexFunc(methods, func(m paymentMethodInfo) bool { return m.Key == method })
	if i < 0 || !methods[i].Usable {
		return fmt.Errorf("payment method %s needs payment data entered in the REWE app - choose a saved one with 'korb checkout create <TIMESLOT_ID> --payment KEY'", method)
	}
	return nil
}

// pickPayment chooses among available methods: the requested one, else PayPal
// if usable, else the first usable method.
func pickPayment(methods []paymentMethodInfo, requested string) (string, error) {
	if requested != "" {
		for _, m := range methods {
			if m.Key == requested {
				return requested, nil
			}
		}
		return "", fmt.Errorf("payment option %s is not available for this checkout (see 'korb checkout payment-options')", requested)
	}
	var usable []string
	for _, m := range methods {
		if m.Usable {
			usable = append(usable, m.Key)
		}
	}
	if slices.Contains(usable, "PAYPAL") {
		return "PAYPAL", nil
	}
	if len(usable) > 0 {
		return usable[0], nil
	}
	return "", errors.New("no saved payment method - set one up in the REWE app (e.g. PayPal direct debit) or pass --payment")
}

// missingForMinimum is the article value (excluding deposit and fees) still
// needed to reach the minimum order amount; 0 if reached or there is none.
func missingForMinimum(b *rewe.Basket) int {
	if b.ServiceConfiguration == nil || b.ServiceConfiguration.MinimumOrderAmount == nil {
		return 0
	}
	return max(0, *b.ServiceConfiguration.MinimumOrderAmount-b.Summary.ArticlePrice)
}

func euro(cents int) string { return fmt.Sprintf("%d,%02d €", cents/100, cents%100) }

// ---- orders ----

func (ap *app) cmdOrders() error {
	if err := ap.only("max-total", "force"); err != nil {
		return err
	}
	api, _, err := ap.api()
	if err != nil {
		return err
	}
	switch ap.sub() {
	case "":
		if err := ap.args.want(0, "orders"); err != nil {
			return err
		}
		open, err := openOrders(ap.ctx, api)
		if err != nil {
			return err
		}
		return ap.print(open)
	case "history":
		if err := ap.args.want(0, "orders history"); err != nil {
			return err
		}
		all, err := api.OrderHistory(ap.ctx, 0)
		if err != nil {
			return err
		}
		return ap.print(all)
	case "get":
		if err := ap.args.want(1, "orders get <ORDER_ID>"); err != nil {
			return err
		}
		o, err := api.Order(ap.ctx, ap.args.pos[0])
		if err != nil {
			return err
		}
		return ap.print(o)
	case "modify":
		return ap.cmdModify(api)
	case "delete":
		if err := ap.args.want(1, "orders delete <ORDER_ID>"); err != nil {
			return err
		}
		r, err := api.CancelOrder(ap.ctx, ap.args.pos[0])
		if err != nil {
			return err
		}
		return ap.print(r)
	default:
		return errUsage
	}
}

// openOrders are the orders that can still be changed or cancelled.
func openOrders(ctx context.Context, api *rewe.API) ([]rewe.OrderHistoryEntry, error) {
	all, err := api.OrderHistory(ctx, 0)
	if err != nil {
		return nil, err
	}
	open := []rewe.OrderHistoryEntry{}
	for _, o := range all {
		if slices.ContainsFunc(o.SubOrders, func(s rewe.SubOrder) bool {
			return s.IsOpen && (slices.Contains(s.OrderActions, "modify") || slices.Contains(s.OrderActions, "cancel"))
		}) {
			open = append(open, o)
		}
	}
	return open, nil
}

// ---- order modifications ----

// basketAPI returns the API for basket commands: bound to the running order
// modification if there is one.
func (ap *app) basketAPI() (*rewe.API, *config.Modification, error) {
	api, _, err := ap.api()
	if err != nil {
		return nil, nil, err
	}
	mod, err := config.ReadModification()
	if err != nil || mod == nil {
		return api, nil, err
	}
	if !ap.mcp {
		fmt.Fprintf(os.Stderr, "note: acting on the modification of order %s\n", mod.OrderID)
	}
	return api.WithModification(&rewe.Modification{OrderID: mod.OrderID, CheckoutID: mod.CheckoutID, BasketID: mod.BasketID}), mod, nil
}

// currentBasket returns the regular basket, or the modification basket. REWE
// may not hand out the modification basket via POST /baskets; then the last
// known state (from starting / the last change) is used.
func (ap *app) currentBasket(api *rewe.API, mod *config.Modification) (*rewe.Basket, error) {
	b, err := api.Basket(ap.ctx)
	if mod == nil {
		return b, err
	}
	if err == nil && b.OrderID != nil && *b.OrderID == mod.OrderID {
		return b, saveModBasket(mod, b)
	}
	if len(mod.Basket) > 0 {
		var snap rewe.Basket
		if jerr := json.Unmarshal(mod.Basket, &snap); jerr == nil {
			return &snap, nil
		}
	}
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("REWE did not return the basket of the modification of order %s", mod.OrderID)
}

func saveModBasket(mod *config.Modification, b *rewe.Basket) error {
	if mod == nil || b == nil {
		return nil
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	mod.Basket = raw
	return config.WriteModification(mod)
}

func (ap *app) cmdModify(api *rewe.API) error {
	mod, err := config.ReadModification()
	if err != nil {
		return err
	}
	if len(ap.args.pos) == 0 {
		return errUsage
	}
	var modAPI *rewe.API
	if mod != nil {
		modAPI = api.WithModification(&rewe.Modification{OrderID: mod.OrderID, CheckoutID: mod.CheckoutID, BasketID: mod.BasketID})
	}
	switch ap.args.pos[0] {
	case "status":
		if mod == nil {
			return ap.print(map[string]any{"modification": nil})
		}
		b, err := ap.currentBasket(modAPI, mod)
		if err != nil {
			return err
		}
		return ap.print(map[string]any{"modification": modView(mod), "basket": b, "missingForMinimumOrderCents": missingForMinimum(b)})
	case "cancel":
		if mod == nil {
			return errors.New("no order modification is running")
		}
		if err := modAPI.CancelOrderModification(ap.ctx, mod.CheckoutID); err != nil && !ap.args.bools["force"] {
			return fmt.Errorf("%w - use --force to clear korb's local modification state anyway", err)
		}
		if err := config.WriteModification(nil); err != nil {
			return err
		}
		return ap.print(map[string]string{"cancelled": mod.OrderID})
	case "submit":
		limit, err := ap.orderLimit()
		if err != nil {
			return err
		}
		out, err := ap.submitModification(api, mod, limit, 0)
		if err != nil {
			return err
		}
		return ap.print(out)
	default:
		if err := ap.args.want(1, "orders modify <ORDER_ID>|status|submit|cancel"); err != nil {
			return err
		}
		m, b, err := ap.startModification(api, mod, ap.args.pos[0])
		if err != nil {
			return err
		}
		return ap.print(map[string]any{"modification": modView(m), "basket": b})
	}
}

// startModification starts changing a placed order, if REWE still allows it.
func (ap *app) startModification(api *rewe.API, running *config.Modification, orderID string) (*config.Modification, *rewe.Basket, error) {
	if running != nil {
		return nil, nil, fmt.Errorf("order %s is already being modified - submit or cancel that first", running.OrderID)
	}
	orders, err := api.OrderHistory(ap.ctx, 0)
	if err != nil {
		return nil, nil, err
	}
	i := slices.IndexFunc(orders, func(o rewe.OrderHistoryEntry) bool { return o.OrderID == orderID })
	if i < 0 {
		return nil, nil, fmt.Errorf("order %s not found in the order history", orderID)
	}
	if !slices.ContainsFunc(orders[i].SubOrders, func(s rewe.SubOrder) bool { return slices.Contains(s.OrderActions, "modify") }) {
		return nil, nil, fmt.Errorf("order %s can no longer be modified (REWE allows changes only until shortly before delivery/pickup)", orderID)
	}
	started, err := api.StartOrderModification(ap.ctx, orderID)
	if err != nil {
		return nil, nil, err
	}
	m := &config.Modification{OrderID: orderID, CheckoutID: started.Checkout.ID, BasketID: started.Basket.ID, Started: time.Now()}
	if err := saveModBasket(m, &started.Basket); err != nil {
		return nil, nil, err
	}
	return m, &started.Basket, nil
}

// submitModification applies a running modification. expected works as for placeOrder.
func (ap *app) submitModification(api *rewe.API, mod *config.Modification, limit, expected int) (any, error) {
	if mod == nil {
		return nil, errors.New("no order modification is running")
	}
	modAPI := api.WithModification(&rewe.Modification{OrderID: mod.OrderID, CheckoutID: mod.CheckoutID, BasketID: mod.BasketID})
	b, err := ap.currentBasket(modAPI, mod)
	if err != nil {
		return nil, err
	}
	if err := checkoutableBasket(b); err != nil {
		return nil, err
	}
	if total := b.Summary.TotalPrice; limit > 0 && total > limit {
		return nil, fmt.Errorf("order total %s exceeds the limit of %s - nothing was changed", euro(total), euro(limit))
	}
	if err := checkExpected(b.Summary.TotalPrice, expected); err != nil {
		return nil, err
	}
	confirmed, err := modAPI.ConfirmCheckout(ap.ctx, mod.CheckoutID, mod.BasketID)
	if err != nil {
		return nil, err
	}
	if rd := confirmed.Checkout.RedirectData; len(rd) > 0 && string(rd) != "null" {
		return &paymentActionRequired{"payment_action_required", rd,
			"complete the payment step in a browser / the REWE app; the modification is still open"}, nil
	}
	o, err := modAPI.PlaceOrder(ap.ctx, mod.CheckoutID)
	if err != nil {
		return nil, err
	}
	return o, config.WriteModification(nil)
}

func modView(m *config.Modification) map[string]any {
	return map[string]any{"orderId": m.OrderID, "checkoutId": m.CheckoutID, "basketId": m.BasketID, "started": m.Started}
}

// ---- suggestions ----

func (ap *app) cmdSuggestion() error {
	if err := ap.only(); err != nil {
		return err
	}
	if ap.sub() != "threshold" {
		return errUsage
	}
	if err := ap.args.want(1, "suggestion threshold <N>"); err != nil {
		return err
	}
	var n int
	if _, err := fmt.Sscanf(ap.args.pos[0], "%d", &n); err != nil || n < 0 {
		return fmt.Errorf("%q is not a valid number of suggestions", ap.args.pos[0])
	}
	api, _, err := ap.api()
	if err != nil {
		return err
	}
	ordered, err := orderedProductIDs(ap.ctx, api)
	if err != nil {
		return err
	}
	purchased, err := api.PurchasedProducts(ap.ctx)
	if err != nil {
		return err
	}
	b, err := api.Basket(ap.ctx)
	if err != nil {
		return err
	}
	inBasket := make([]string, 0, len(b.LineItems))
	for _, li := range b.LineItems {
		inBasket = append(inBasket, li.Product.ProductID)
	}
	remaining := 0
	if b.Staggerings.NextStaggering != nil {
		remaining = b.Staggerings.NextStaggering.RemainingArticlePrice
	}
	return ap.print(map[string]any{
		"suggestions":                   suggest.Engine(ordered, purchased, inBasket, n),
		"remainingArticlePriceCents":    remaining,
		"remainingForMinimumOrderCents": missingForMinimum(b),
	})
}

// orderedProductIDs lists the product ids of the last 10 orders, one entry per
// order line.
func orderedProductIDs(ctx context.Context, api *rewe.API) ([]string, error) {
	orders, err := api.OrderHistory(ctx, 10) // the last 10 orders are enough
	if err != nil {
		return nil, err
	}
	details := make([]*rewe.OrderDetail, len(orders))
	errs := make([]error, len(orders))
	var wg sync.WaitGroup
	for i, o := range orders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			details[i], errs[i] = api.Order(ctx, o.OrderID)
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	var ordered []string
	for _, d := range details {
		for _, s := range d.SubOrders {
			for _, li := range s.LineItems {
				if li.ProductID != nil {
					ordered = append(ordered, *li.ProductID)
				}
			}
		}
	}
	return ordered, nil
}

// ---- ebons ----

func (ap *app) cmdEbons() error {
	switch ap.sub() {
	case "":
		if err := ap.only(); err != nil {
			return err
		}
		if err := ap.args.want(0, "ebons"); err != nil {
			return err
		}
		api, _, err := ap.api()
		if err != nil {
			return err
		}
		e, err := api.Ebons(ap.ctx)
		if err != nil {
			return err
		}
		return ap.print(e)
	case "download":
		if err := ap.only("output"); err != nil {
			return err
		}
		if err := ap.args.want(1, "ebons download <EBON_ID> [--output FILE]"); err != nil {
			return err
		}
		api, _, err := ap.api()
		if err != nil {
			return err
		}
		pdf, err := api.ReceiptPDF(ap.ctx, ap.args.pos[0])
		if err != nil {
			return err
		}
		file := ap.args.value("output", "ebon.pdf")
		if err := os.WriteFile(file, pdf, 0o644); err != nil {
			return err
		}
		return ap.print("Stored receipt to: " + file)
	default:
		return errUsage
	}
}

// ---- login ----

func (ap *app) cmdLogin() error {
	m := ap.tokens
	sub := ap.sub()
	switch sub {
	case "":
		if err := ap.only("no-browser", "no-handler", "timeout", "wait"); err != nil {
			return err
		}
		if err := ap.args.want(0, "login [--no-browser] [--no-handler] [--wait] [--timeout 10m]"); err != nil {
			return err
		}
		return ap.interactiveLogin()
	case "complete", "callback":
		if err := ap.only(); err != nil {
			return err
		}
		if err := ap.args.want(1, "login complete '<de.rewe.app://redirect?...>'"); err != nil {
			return err
		}
		err := m.CompleteLogin(ap.ctx, ap.args.pos[0])
		if sub == "callback" {
			if err != nil {
				auth.Notify("korb: REWE login failed", err.Error())
			} else {
				auth.Notify("korb: REWE login succeeded", "You can close the browser tab.")
			}
		}
		if err != nil {
			return err
		}
		return ap.print("Login succeeded, tokens stored")
	case "status":
		if err := ap.only(); err != nil {
			return err
		}
		s := m.Status()
		s.LoginHandlerInstalled = auth.HandlerInstalled()
		return ap.print(s)
	case "install-handler":
		if err := ap.only(); err != nil {
			return err
		}
		path, err := auth.InstallHandler()
		if err != nil {
			return err
		}
		return ap.print(map[string]string{"installed": path})
	case "uninstall-handler":
		if err := ap.only(); err != nil {
			return err
		}
		if err := auth.UninstallHandler(); err != nil {
			return err
		}
		return ap.print("URL handler removed")
	default:
		return errUsage
	}
}

func (ap *app) fakeLogin(authURL string) error {
	noFollow := *ap.doer.HTTP
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ap.ctx, http.MethodGet, authURL, nil)
	if err != nil {
		return err
	}
	res, err := noFollow.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	loc := res.Header.Get("Location")
	if loc == "" {
		return fmt.Errorf("test auth server did not redirect (HTTP %d)", res.StatusCode)
	}
	if err := ap.tokens.CompleteLogin(ap.ctx, loc); err != nil {
		return err
	}
	return ap.print("Login succeeded, tokens stored")
}

// localDesktop reports whether a browser opened by korb would run on this
// machine, i.e. whether a de.rewe.app:// handler registered here can receive
// the redirect. Over SSH the browser runs elsewhere.
func localDesktop() bool {
	for _, v := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		if os.Getenv(v) != "" {
			return false
		}
	}
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return false
	}
	return true
}

const pasteHelp = `After logging in the page stays blank, because the browser cannot open the REWE app.
Get the redirect URL from the browser's developer tools (F12):
  - Chrome/Edge: Console shows "Failed to launch 'de.rewe.app://redirect?...'" - copy that URL
  - any browser: Network tab (enable "Preserve log" before logging in) - the last request's
    "Location" response header is the de.rewe.app://redirect?... URL`

func (ap *app) interactiveLogin() error {
	m := ap.tokens
	timeout, err := time.ParseDuration(ap.args.value("timeout", "10m"))
	if err != nil {
		return fmt.Errorf("--timeout: %w", err)
	}
	local := localDesktop()
	useHandler := auth.HandlerSupported && local && !ap.args.bools["no-handler"]
	if useHandler && !auth.HandlerInstalled() {
		path, err := auth.InstallHandler()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Could not register the de.rewe.app:// handler (%v) - falling back to pasting the URL.\n", err)
			useHandler = false
		} else {
			fmt.Fprintf(os.Stderr, "Registered korb for de.rewe.app:// links (%s).\n", path)
		}
	}
	authURL, state, err := m.StartLogin()
	if err != nil {
		return err
	}
	if strings.HasPrefix(authURL, "http://") {
		// Plain-HTTP auth server = a test backend such as korb-fake: it logs in
		// without a browser, so follow the redirect directly.
		return ap.fakeLogin(authURL)
	}
	interactive := stdinIsTerminal()
	wait := ap.args.bools["wait"] && useHandler

	if !interactive && !wait {
		// Agents and other non-interactive callers: never block, hand over the next step.
		// If a local handler is registered it still completes the login in the background.
		next := "open authUrl in a browser, log in, then run: korb login complete '<de.rewe.app://redirect?... URL>'"
		if useHandler {
			next = "open authUrl in a browser on this machine and allow opening the link with korb (or run: korb login complete '<de.rewe.app://redirect?... URL>'); check with: korb login status"
		}
		return ap.print(map[string]string{"status": "pending", "authUrl": authURL, "next": next, "help": pasteHelp})
	}

	fmt.Fprintf(os.Stderr, "\nOpen this URL in a browser and log in to REWE (email, password, 2FA):\n\n%s\n\n", authURL)
	if local && !ap.args.bools["no-browser"] {
		if err := auth.OpenBrowser(authURL); err == nil {
			fmt.Fprintln(os.Stderr, "(opened in your default browser)")
		}
	}
	if useHandler {
		fmt.Fprintln(os.Stderr, "When the browser asks to open the link with 'korb REWE login', allow it - korb finishes automatically.")
	}
	if interactive {
		if useHandler {
			fmt.Fprintln(os.Stderr, "If that does not work (e.g. browser on another machine):")
		}
		fmt.Fprintln(os.Stderr, pasteHelp)
		fmt.Fprint(os.Stderr, "\nPaste the de.rewe.app://redirect?... URL and press Enter: ")
	}

	ctx, cancel := context.WithTimeout(ap.ctx, timeout)
	defer cancel()
	done := make(chan error, 2)
	if useHandler {
		go func() {
			r, err := m.WaitForCompletion(ctx, state)
			if err != nil {
				done <- err
				return
			}
			if !r.OK {
				done <- errors.New(r.Error)
				return
			}
			done <- nil
		}()
	}
	if interactive {
		go func() {
			sc := bufio.NewScanner(os.Stdin)
			sc.Buffer(make([]byte, 64*1024), 1<<20)
			for sc.Scan() {
				if line := strings.TrimSpace(sc.Text()); line != "" {
					done <- m.CompleteLogin(ctx, line)
					return
				}
			}
			// stdin closed: keep waiting for the URL handler, otherwise give up
			if !useHandler {
				done <- fmt.Errorf("no redirect URL received - finish the login with: korb login complete '<de.rewe.app://redirect?... URL>'")
			}
		}()
	}
	select {
	case err := <-done:
		if err != nil {
			return err
		}
		return ap.print("Login succeeded, tokens stored")
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("login not completed within %s - finish it with: korb login complete '<de.rewe.app://redirect?... URL>'", timeout)
		}
		return ctx.Err()
	}
}
