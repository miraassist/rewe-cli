package main

import (
	"strings"
	"testing"
	"time"

	"github.com/miraassist/rewe-cli/internal/rewe"
)

func checkoutWith(payment string, total int, withSlot bool) *rewe.CheckoutResponse {
	co := &rewe.CheckoutResponse{}
	co.Basket.Summary.TotalPrice = total
	if payment != "" {
		co.Checkout.Payment = &rewe.CheckoutPayment{PaymentMethod: payment}
	}
	if withSlot {
		co.Checkout.Timeslot = &rewe.Timeslot{ID: "slot", StartTime: time.Now()}
	}
	return co
}

var methods = []paymentMethodInfo{
	{Key: "DIRECT_DEBIT", Online: true},
	{Key: "CREDIT_CARD", Online: true},
	{Key: "PAYPAL", Online: true, Saved: true, Status: "VALID", Usable: true},
}

func TestCheckOrderable(t *testing.T) {
	cases := []struct {
		name    string
		co      *rewe.CheckoutResponse
		methods []paymentMethodInfo
		limit   int
		err     string
	}{
		{"paypal ok", checkoutWith("PAYPAL", 6667, true), methods, 0, ""},
		{"pickup ok", checkoutWith("MARKET_PAYMENT", 5000, true), nil, 0, ""},
		{"no slot", checkoutWith("PAYPAL", 6667, false), methods, 0, "no timeslot"},
		{"no payment", checkoutWith("", 6667, true), methods, 0, "no payment method"},
		{"card without saved data", checkoutWith("CREDIT_CARD", 6667, true), methods, 0, "needs payment data"},
		{"unknown method", checkoutWith("BITCOIN", 6667, true), methods, 0, "needs payment data"},
		{"over limit", checkoutWith("PAYPAL", 15001, true), methods, 15000, "150,01 € exceeds the limit of 150,00 €"},
		{"at limit", checkoutWith("PAYPAL", 15000, true), methods, 15000, ""},
	}
	for _, c := range cases {
		err := checkOrderable(c.co, c.methods, c.limit)
		if c.err == "" && err != nil || c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.err)
		}
	}
}

func TestPickPayment(t *testing.T) {
	if p, _ := pickPayment(methods, ""); p != "PAYPAL" {
		t.Fatalf("want PAYPAL, got %s", p)
	}
	if p, _ := pickPayment(methods, "CREDIT_CARD"); p != "CREDIT_CARD" {
		t.Fatalf("explicit choice must win, got %s", p)
	}
	if _, err := pickPayment(methods, "BITCOIN"); err == nil {
		t.Fatal("unknown method must fail")
	}
	if _, err := pickPayment(methods[:2], ""); err == nil {
		t.Fatal("no usable method must fail")
	}
}

func TestOrderLimit(t *testing.T) {
	ap := func(flag string) *app {
		raw := []string{}
		if flag != "" {
			raw = append(raw, "--max-total", flag)
		}
		a, _ := parseArgs(raw, allBoolFlags, allValueFlags)
		return &app{args: a}
	}
	t.Setenv("KORB_MAX_TOTAL", "")
	if l, _ := ap("").orderLimit(); l != 0 {
		t.Fatalf("no limit expected, got %d", l)
	}
	t.Setenv("KORB_MAX_TOTAL", "10000")
	if l, _ := ap("20000").orderLimit(); l != 10000 {
		t.Fatalf("lower limit (env) must win, got %d", l)
	}
	if l, _ := ap("5000").orderLimit(); l != 5000 {
		t.Fatalf("lower limit (flag) must win, got %d", l)
	}
	t.Setenv("KORB_MAX_TOTAL", "abc")
	if _, err := ap("").orderLimit(); err == nil {
		t.Fatal("invalid env must fail")
	}
}

func TestCheckoutableBasket(t *testing.T) {
	item := []rewe.LineItem{{Quantity: 1}}
	if err := checkoutableBasket(&rewe.Basket{}); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty basket: %v", err)
	}
	if err := checkoutableBasket(&rewe.Basket{LineItems: item}); err != nil {
		t.Fatalf("valid basket: %v", err)
	}
	minimum := 6000
	b := &rewe.Basket{LineItems: item, Violations: []rewe.Violation{{ID: "minimum.delivery.not.reached", Message: "Mindestbestellwert 60 € nicht erreicht!"}},
		ServiceConfiguration: &rewe.ServiceConfiguration{MinimumOrderAmount: &minimum}}
	b.Summary.ArticlePrice, b.Summary.TotalPrice = 5368, 5968 // deposit does not count
	if err := checkoutableBasket(b); err == nil || !strings.Contains(err.Error(), "Mindestbestellwert 60 €") || !strings.Contains(err.Error(), "es fehlen 6,32 €") {
		t.Fatalf("violation: %v", err)
	}
	b.Summary.ArticlePrice = 6100
	if missingForMinimum(b) != 0 || missingForMinimum(&rewe.Basket{}) != 0 {
		t.Fatal("missingForMinimum must be 0 when reached or unknown")
	}
}
