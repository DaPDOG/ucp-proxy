// Package woocommerce implements the adapter for WooCommerce stores using the Store API.
// All WooCommerce-specific types, transforms, and HTTP client logic live here.
package woocommerce

import "encoding/json"

// === WooCommerce API Response Types ===

// WooCheckoutResponse represents WooCommerce Store API checkout response.
type WooCheckoutResponse struct {
	OrderID         int               `json:"order_id"`
	Status          string            `json:"status"`
	OrderKey        string            `json:"order_key"`
	CustomerID      int               `json:"customer_id"`
	CustomerNote    string            `json:"customer_note"`
	BillingAddress  WooAddress        `json:"billing_address"`
	ShippingAddress WooAddress        `json:"shipping_address"`
	LineItems       []WooLineItem     `json:"line_items"`
	Totals          WooTotals         `json:"totals"`
	PaymentResult   *WooPaymentResult `json:"payment_result,omitempty"`
}

// WooCartResponse represents WooCommerce Store API cart response.
// Used for incremental checkout building (before payment submission).
type WooCartResponse struct {
	Items                 []WooCartItem    `json:"items"`
	Totals                WooTotals        `json:"totals"`
	ShippingRates         []WooShippingPkg `json:"shipping_rates,omitempty"`
	Coupons               []WooCoupon      `json:"coupons,omitempty"`
	Fees                  []WooFee         `json:"fees,omitempty"`
	NeedsShipping         bool             `json:"needs_shipping"`
	NeedsPayment          bool             `json:"needs_payment"`
	HasCalculatedShipping bool             `json:"has_calculated_shipping"`
	BillingAddress        WooAddress       `json:"billing_address"`
	ShippingAddress       WooAddress       `json:"shipping_address"`
	Errors                []WooCartError   `json:"errors,omitempty"`
}

// WooCartError represents an error in cart state.
type WooCartError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WooLineItem represents an item in checkout response.
type WooLineItem struct {
	ID          int          `json:"id"`
	Name        string       `json:"name"`
	Quantity    int          `json:"quantity"`
	Price       string       `json:"price"`    // "99.00" - string decimal
	Subtotal    string       `json:"subtotal"` // "99.00"
	SubtotalTax string       `json:"subtotal_tax"`
	Total       string       `json:"total"` // "99.00"
	TotalTax    string       `json:"total_tax"`
	SKU         string       `json:"sku"`
	Images      []WooImage   `json:"images,omitempty"`
	Variation   []WooVariant `json:"variation,omitempty"`
}

// WooCartItem represents an item in cart response.
// Structure differs from WooLineItem (checkout response).
type WooCartItem struct {
	Key      string            `json:"key"` // Cart item key (not numeric ID)
	ID       int               `json:"id"`  // Product ID
	Name     string            `json:"name"`
	Quantity int               `json:"quantity"`
	Prices   WooCartItemPrices `json:"prices"`
	Totals   WooCartItemTotals `json:"totals"`
	Images   []WooImage        `json:"images,omitempty"`
	MetaData []WooItemMeta     `json:"meta_data,omitempty"`
}

// WooItemMeta represents custom field metadata on a cart item.
// Exposed via Store API when merchant adds data via woocommerce_store_api_add_to_cart_data filter.
type WooItemMeta struct {
	ID    int    `json:"id"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

// WooCartItemPrices contains price info for a cart item.
type WooCartItemPrices struct {
	Price        string `json:"price"`         // Current unit price in minor units
	RegularPrice string `json:"regular_price"` // Regular price
	SalePrice    string `json:"sale_price"`    // Sale price if on sale
}

// WooCartItemTotals contains totals for a cart item.
type WooCartItemTotals struct {
	LineSubtotal    string `json:"line_subtotal"` // price * quantity
	LineSubtotalTax string `json:"line_subtotal_tax"`
	LineTotal       string `json:"line_total"` // After discounts
	LineTotalTax    string `json:"line_total_tax"`
}

// WooTotals contains all pricing totals from WooCommerce.
// All string fields are decimal representations (e.g., "99.00").
type WooTotals struct {
	CurrencyCode      string `json:"currency_code"`
	CurrencySymbol    string `json:"currency_symbol"`
	CurrencyMinorUnit int    `json:"currency_minor_unit"`
	TotalItems        string `json:"total_items"`
	TotalItemsTax     string `json:"total_items_tax"`
	TotalFees         string `json:"total_fees"`
	TotalFeesTax      string `json:"total_fees_tax"`
	TotalDiscount     string `json:"total_discount"`
	TotalDiscountTax  string `json:"total_discount_tax"`
	TotalShipping     string `json:"total_shipping"`
	TotalShippingTax  string `json:"total_shipping_tax"`
	TotalPrice        string `json:"total_price"`
	TotalTax          string `json:"total_tax"`
}

// WooAddress represents a WooCommerce address.
type WooAddress struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Company   string `json:"company"`
	Address1  string `json:"address_1"`
	Address2  string `json:"address_2"`
	City      string `json:"city"`
	State     string `json:"state"`
	Postcode  string `json:"postcode"`
	Country   string `json:"country"`
	Email     string `json:"email,omitempty"`
	Phone     string `json:"phone,omitempty"`
}

// WooImage represents a product image.
type WooImage struct {
	ID   int    `json:"id"`
	Src  string `json:"src"`
	Name string `json:"name"`
	Alt  string `json:"alt"`
}

// WooVariant represents a product variation attribute.
type WooVariant struct {
	Attribute string `json:"attribute"`
	Value     string `json:"value"`
}

// WooShippingPkg represents a shipping package with available rates.
// Used in cart response shipping_rates array.
type WooShippingPkg struct {
	PackageID     int               `json:"package_id"`
	Name          string            `json:"name"`
	Destination   WooAddress        `json:"destination"`
	ShippingRates []WooShippingRate `json:"shipping_rates"`
}

// WooShippingRate represents a single shipping option.
type WooShippingRate struct {
	RateID       string `json:"rate_id"`
	Name         string `json:"name"`
	Price        string `json:"price"` // Minor units as string
	MethodID     string `json:"method_id"`
	Selected     bool   `json:"selected"`
	DeliveryTime string `json:"delivery_time,omitempty"`
}

// WooCoupon represents an applied discount code.
// The actual discount amount is in Totals.TotalDiscount (minor units).
type WooCoupon struct {
	Code   string          `json:"code"`
	Totals WooCouponTotals `json:"totals"`
}

// WooCouponTotals contains the calculated discount amounts for a coupon.
type WooCouponTotals struct {
	TotalDiscount    string `json:"total_discount"`     // Minor units as string
	TotalDiscountTax string `json:"total_discount_tax"` // Minor units as string
}

// WooFee represents an additional fee.
type WooFee struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Amount string `json:"amount"`
}

// WooPaymentResult contains payment processing outcome.
type WooPaymentResult struct {
	PaymentStatus string `json:"payment_status"`
	RedirectURL   string `json:"redirect_url,omitempty"` // 3DS redirect when present
}

// === WooCommerce API Request Types ===

// WooCheckoutRequest is sent to WooCommerce to update/complete checkout.
type WooCheckoutRequest struct {
	BillingAddress  *WooAddress      `json:"billing_address,omitempty"`
	ShippingAddress *WooAddress      `json:"shipping_address,omitempty"`
	PaymentMethod   string           `json:"payment_method,omitempty"`
	PaymentData     []WooPaymentData `json:"payment_data,omitempty"`
}

// WooPaymentData is a key-value pair for payment gateway data.
// For Stripe: key="wc-stripe-payment-method", value="pm_xxx"
type WooPaymentData struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// WooCartAddRequest adds an item to cart.
type WooCartAddRequest struct {
	ID       int `json:"id"`
	Quantity int `json:"quantity"`
}

// WooErrorResponse represents a WooCommerce API error.
type WooErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Status int `json:"status"`
	} `json:"data"`
}

// === Batch API Types ===

// WooBatchRequest is the payload for POST /batch endpoint.
// Combines multiple cart operations into a single request.
type WooBatchRequest struct {
	Requests []WooBatchOperation `json:"requests"`
}

// WooBatchOperation is a single operation within a batch.
// Uses WooCommerce Store API batch format: path, method, body, headers.
// Headers field allows per-operation authentication (Cart-Token, Nonce).
type WooBatchOperation struct {
	Path    string            `json:"path"`
	Method  string            `json:"method"`
	Body    json.RawMessage   `json:"body,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// WooBatchResponse is the response from POST /batch endpoint.
type WooBatchResponse struct {
	Responses []WooBatchResult `json:"responses"`
}

// WooBatchResult is a single result within a batch response.
type WooBatchResult struct {
	Status  int             `json:"status"`
	Body    json.RawMessage `json:"body"`    // Raw JSON response or error
	Headers WooBatchHeaders `json:"headers"` // Response headers including nonce
}

// WooBatchHeaders contains headers from a batch response.
type WooBatchHeaders struct {
	Nonce     string `json:"Nonce"`
	CartToken string `json:"Cart-Token"`
}

// === Draft Checkout Types ===

// WooDraftCheckout is the response from GET /checkout (creates draft order).
// Contains order info but not full cart state.
type WooDraftCheckout struct {
	OrderID         int        `json:"order_id"`
	Status          string     `json:"status"` // "checkout-draft" for new drafts
	OrderKey        string     `json:"order_key"`
	CustomerID      int        `json:"customer_id"`
	BillingAddress  WooAddress `json:"billing_address"`
	ShippingAddress WooAddress `json:"shipping_address"`
}
