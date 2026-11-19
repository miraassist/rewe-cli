package rewe

import (
	"encoding/json"
	"time"
)

// JSON field names mirror the Haskell implementation so agent-facing output stays
// compatible. Optional values are pointers and are emitted as null when absent.

// Product is shared by search, favorites, purchased products and basket line items.
type Product struct {
	ArticleID  string             `json:"articleId"`
	ProductID  string             `json:"productId"`
	Title      string             `json:"title"`
	ImageURL   string             `json:"imageURL"`
	OrderLimit *int               `json:"orderLimit"`
	Listing    Listing            `json:"listing"`
	Attributes *ProductAttributes `json:"attributes"`
	ItemID     *string            `json:"itemId"`
}

type Listing struct {
	ListingID          string  `json:"listingId"`
	CurrentRetailPrice int     `json:"currentRetailPrice"`
	Grammage           *string `json:"grammage"`
}

type ProductAttributes struct {
	IsOrganic  *bool `json:"isOrganic"`
	IsRegional *bool `json:"isRegional"`
}

type productList struct {
	Products []Product `json:"products"`
}

// Markets

// PickupMarket is an entry of the service portfolio of a zip code.
type PickupMarket struct {
	WwIdent               string `json:"wwIdent"`
	DisplayName           string `json:"displayName"`
	CompanyName           string `json:"companyName"`
	StreetWithHouseNumber string `json:"streetWithHouseNumber"`
	ZipCode               string `json:"zipCode"`
	City                  string `json:"city"`
	PickupType            string `json:"pickupType"`
	IsPickupStation       bool   `json:"isPickupStation"`
	// ServiceType is set by korb: PICKUP for markets, DELIVERY for the delivery service.
	ServiceType string `json:"serviceType"`
}

// ServicePortfolio lists what REWE offers for a zip code.
type ServicePortfolio struct {
	CustomerZipCode string `json:"customerZipCode"`
	DeliveryMarket  *struct {
		WwIdent string `json:"wwIdent"`
	} `json:"deliveryMarket"`
	PickupMarkets []PickupMarket `json:"pickupMarkets"`
}

// MarketDetails is the result of GET /markets/{id}/{serviceType}.
type MarketDetails struct {
	Typename              string `json:"__typename"`
	WwIdent               string `json:"wwIdent"`
	DisplayName           string `json:"displayName"`
	CompanyName           string `json:"companyName,omitempty"`
	StreetWithHouseNumber string `json:"streetWithHouseNumber,omitempty"`
	ZipCode               string `json:"zipCode,omitempty"`
	City                  string `json:"city,omitempty"`
	PickupType            string `json:"pickupType,omitempty"`
}

// Favorites

type FavoriteList struct {
	ID    string    `json:"id"`
	Name  string    `json:"name"`
	Items []Product `json:"items"`
}

// Basket

type Change struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

type LineItem struct {
	Quantity   int      `json:"quantity"`
	Price      int      `json:"price"`
	TotalPrice int      `json:"totalPrice"`
	Grammage   *string  `json:"grammage"`
	Product    Product  `json:"product"`
	Changes    []Change `json:"changes"`
}

type BasketSummary struct {
	ArticleCount int         `json:"articleCount"`
	ArticlePrice int         `json:"articlePrice"`
	TotalPrice   int         `json:"totalPrice"`
	Fees         *BasketFees `json:"fees,omitempty"`
}

// BasketFees are the parts of the total besides the articles; null when none.
type BasketFees struct {
	Refund                *int `json:"refund"` // deposit ("Pfand")
	BeverageSurcharge     *int `json:"beverageSurcharge"`
	ReusableBagSurcharge  *int `json:"reusableBagSurcharge"`
	TransportBoxSurcharge *int `json:"transportBoxSurcharge"`
	ServiceFee            *int `json:"serviceFee"`
}

type Staggering struct {
	ArticlePriceThreshold int    `json:"articlePriceThreshold"`
	DisplayText           string `json:"displayText"`
}

type NextStaggering struct {
	RemainingArticlePrice int    `json:"remainingArticlePrice"`
	ArticlePriceThreshold int    `json:"articlePriceThreshold"`
	DisplayText           string `json:"displayText"`
}

type Staggerings struct {
	ReachedStaggering *Staggering     `json:"reachedStaggering"`
	NextStaggering    *NextStaggering `json:"nextStaggering"`
}

type TimeSlotInformation struct {
	StartTime    *string `json:"startTime"`
	EndTime      *string `json:"endTime"`
	TimeSlotText string  `json:"timeSlotText"`
}

type ServiceSelection struct {
	WwIdent     string `json:"wwIdent"`
	ServiceType string `json:"serviceType"`
	ZipCode     string `json:"zipCode"`
}

// Violation is a reason why the basket cannot be ordered yet,
// e.g. "minimum.delivery.not.reached".
type Violation struct {
	ID            string  `json:"id"`
	Message       string  `json:"message"`
	DetailMessage *string `json:"detailMessage"`
}

type ServiceConfiguration struct {
	MinimumOrderAmount *int `json:"minimumOrderAmount"`
}

type Basket struct {
	ID                  string              `json:"id"`
	OrderID             *string             `json:"orderId"` // set for the basket of an order modification
	Version             int                 `json:"version"`
	ServiceSelection    ServiceSelection    `json:"serviceSelection"`
	LineItems           []LineItem          `json:"lineItems"`
	Summary             BasketSummary       `json:"summary"`
	Staggerings         Staggerings         `json:"staggerings"`
	TimeSlotInformation TimeSlotInformation `json:"timeSlotInformation"`
	Changes             []Change            `json:"changes"`
	// Violations must be empty before the basket can be checked out.
	Violations           []Violation           `json:"violations"`
	ServiceConfiguration *ServiceConfiguration `json:"serviceConfiguration"`
}

// Checkout

type CheckoutPayment struct {
	PaymentMethod string `json:"paymentMethod"`
}

// Address is a delivery or invoice address of the customer.
type Address struct {
	FirstName   string `json:"firstName"`
	LastName    string `json:"lastName"`
	Street      string `json:"street"`
	HouseNumber string `json:"houseNumber"`
	Zip         string `json:"zip"`
	City        string `json:"city"`
}

// CheckoutOptions describes what the checkout supports.
type CheckoutOptions struct {
	CheckoutType            string `json:"checkoutType"`
	DeliveryAddressRequired bool   `json:"deliveryAddressRequired"`
	OnlinePaymentSupported  bool   `json:"onlinePaymentSupported"`
	OfflinePaymentSupported bool   `json:"offlinePaymentSupported"`
	// PaymentOptionsParams is the query string for GET /payment-options.
	PaymentOptionsParams string `json:"paymentOptionsParams"`
}

type CheckoutInfo struct {
	ID                      string           `json:"id"`
	BasketID                string           `json:"basketId"`
	MarketID                string           `json:"marketId"`
	ZipCode                 string           `json:"zipCode"`
	ServiceType             string           `json:"serviceType"`
	IsFreeOrder             bool             `json:"isFreeOrder"`
	PaymentType             string           `json:"paymentType"`
	Timeslot                *Timeslot        `json:"timeslot"`
	Payment                 *CheckoutPayment `json:"payment"`
	SelectedDeliveryAddress *Address         `json:"selectedDeliveryAddress"`
	Options                 *CheckoutOptions `json:"options"`
	// RedirectData is set when REWE needs the customer to confirm the payment in a browser.
	RedirectData json.RawMessage `json:"redirectData"`
}

// PaymentMethod is one entry of GET /payment-options.
type PaymentMethod struct {
	Key              string   `json:"key"`
	Variants         []string `json:"variants"`
	SavedPaymentData *struct {
		ID                     json.Number `json:"id"`
		BillingAgreementStatus string      `json:"billingAgreementStatus"`
	} `json:"savedPaymentData"`
}

// PaymentOptions is the result of GET /payment-options.
type PaymentOptions struct {
	Options struct {
		OfflinePaymentMethods        []PaymentMethod `json:"offlinePaymentMethods"`
		EnabledOnlinePaymentMethods  []PaymentMethod `json:"enabledOnlinePaymentMethods"`
		DisabledOnlinePaymentMethods []PaymentMethod `json:"disabledOnlinePaymentMethods"`
		RefusalReason                json.RawMessage `json:"refusalReason"`
	} `json:"options"`
	SelectedPaymentOption *string `json:"selectedPaymentOption"`
}

type CheckoutBasketSummary struct {
	ID      string        `json:"id"`
	Version int           `json:"version"`
	Summary BasketSummary `json:"summary"`
}

type CheckoutResponse struct {
	Checkout CheckoutInfo          `json:"checkout"`
	Basket   CheckoutBasketSummary `json:"basket"`
}

type OrderResponse struct {
	Order struct {
		OrderID string `json:"orderId"`
	} `json:"order"`
}

type OrderCancelResponse struct {
	OrderCancel string `json:"orderCancel"`
}

// Orders

type OrderTimeSlot struct {
	FirstSlotDate string `json:"firstSlotDate"`
	LastSlotDate  string `json:"lastSlotDate"`
}

type SubOrder struct {
	IsOpen       bool          `json:"isOpen"`
	Status       string        `json:"status"`
	TimeSlot     OrderTimeSlot `json:"timeSlot"`
	OrderActions []string      `json:"orderActions"`
}

type OrderHistoryEntry struct {
	OrderID    string     `json:"orderId"`
	OrderValue int        `json:"orderValue"`
	OrderDate  string     `json:"orderDate"`
	SubOrders  []SubOrder `json:"subOrders"`
}

type OrderDetailLineItem struct {
	LineItemType string  `json:"lineItemType"`
	TotalPrice   int     `json:"totalPrice"`
	ProductID    *string `json:"productId"`
	Title        *string `json:"title"`
	Quantity     *int    `json:"quantity"`
	Price        *int    `json:"price"`
}

type OrderDetailSubOrder struct {
	TimeSlot  OrderTimeSlot         `json:"timeSlot"`
	Status    string                `json:"status"`
	LineItems []OrderDetailLineItem `json:"lineItems"`
}

type OrderDetail struct {
	OrderID       string                `json:"orderId"`
	OrderDate     string                `json:"orderDate"`
	OrderValue    int                   `json:"orderValue"`
	Status        string                `json:"status"`
	ArticlesPrice int                   `json:"articlesPrice"`
	SubOrders     []OrderDetailSubOrder `json:"subOrders"`
}

// Timeslots

type Timeslot struct {
	ID         string    `json:"id"`
	StartTime  time.Time `json:"startTime"`
	EndTime    time.Time `json:"endTime"`
	ServiceFee int       `json:"serviceFee"`
}

type TimeslotsResponse struct {
	Timeslots        []Timeslot `json:"getTimeslotsCheckout"`
	FreeDeliveryInfo *struct {
		FreeDeliveryDays []string `json:"freeDeliveryDays"`
	} `json:"freeDeliveryInfo"`
}

type TimeslotReservation struct {
	SlotID        string `json:"slotId"`
	ExpireTime    string `json:"expireTime"`
	SlotStartTime string `json:"slotStartTime"`
	SlotEndTime   string `json:"slotEndTime"`
	Fee           *int   `json:"fee"`
}

// eBons

type EbonMarket struct {
	Name    string `json:"name"`
	City    string `json:"city"`
	WwIdent string `json:"wwIdent"`
	Street  string `json:"street"`
	ZipCode string `json:"zipCode"`
}

type EbonEntry struct {
	ReceiptID         string     `json:"receiptId"`
	ReceiptTimestamp  string     `json:"receiptTimestamp"`
	ReceiptTotalPrice int        `json:"receiptTotalPrice"`
	Market            EbonMarket `json:"market"`
	Cancelled         bool       `json:"cancelled"`
}

// Categories

type Category struct {
	Slug            string     `json:"slug"`
	Name            *string    `json:"name"`
	CategoryTags    []string   `json:"categoryTags"`
	ChildCategories []Category `json:"childCategories"`
}
