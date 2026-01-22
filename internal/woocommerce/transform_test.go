package woocommerce

import (
	"testing"

	"ucp-proxy/internal/model"
)

func TestMapOrderStatus(t *testing.T) {
	tests := []struct {
		wcStatus string
		want     model.CheckoutStatus
	}{
		{"checkout-draft", model.StatusIncomplete},
		{"pending", model.StatusCompleteInProgress},
		{"processing", model.StatusCompleteInProgress},
		{"on-hold", model.StatusRequiresEscalation},
		{"completed", model.StatusCompleted},
		{"cancelled", model.StatusCanceled},
		{"failed", model.StatusIncomplete}, // Failed payment, can retry
		{"refunded", model.StatusCompleted},
		{"unknown-status", model.StatusIncomplete}, // Default fallback
		{"", model.StatusIncomplete},               // Empty string
	}

	for _, tt := range tests {
		t.Run(tt.wcStatus, func(t *testing.T) {
			got := MapOrderStatus(tt.wcStatus)
			if got != tt.want {
				t.Errorf("MapOrderStatus(%q) = %q, want %q", tt.wcStatus, got, tt.want)
			}
		})
	}
}

// === Status Determination Tests ===
// Direct tests for status determination logic (previously only tested indirectly)

func TestDetermineCartStatus(t *testing.T) {
	tests := []struct {
		name string
		cart *WooCartResponse
		want model.CheckoutStatus
	}{
		{
			name: "nil cart",
			cart: nil,
			want: model.StatusIncomplete,
		},
		{
			name: "empty cart",
			cart: &WooCartResponse{Items: []WooCartItem{}},
			want: model.StatusIncomplete,
		},
		{
			name: "cart with errors",
			cart: &WooCartResponse{
				Items:  []WooCartItem{{Key: "a", ID: 1}},
				Errors: []WooCartError{{Code: "error", Message: "test"}},
			},
			want: model.StatusIncomplete,
		},
		{
			name: "missing email",
			cart: &WooCartResponse{
				Items:          []WooCartItem{{Key: "a", ID: 1}},
				BillingAddress: WooAddress{FirstName: "John"}, // Name but no email
			},
			want: model.StatusIncomplete,
		},
		{
			name: "email only - sufficient for billing",
			cart: &WooCartResponse{
				Items:          []WooCartItem{{Key: "a", ID: 1}},
				BillingAddress: WooAddress{Email: "j@test.com"}, // Email only, no name
				NeedsShipping:  false,
			},
			want: model.StatusReadyForComplete, // Full billing comes with payment instrument
		},
		{
			name: "needs shipping but no shipping address",
			cart: &WooCartResponse{
				Items:           []WooCartItem{{Key: "a", ID: 1}},
				BillingAddress:  WooAddress{FirstName: "John", Email: "j@test.com"},
				NeedsShipping:   true,
				ShippingAddress: WooAddress{}, // Empty
			},
			want: model.StatusIncomplete,
		},
		{
			name: "needs shipping but no rate selected",
			cart: &WooCartResponse{
				Items:           []WooCartItem{{Key: "a", ID: 1}},
				BillingAddress:  WooAddress{FirstName: "John", Email: "j@test.com"},
				NeedsShipping:   true,
				ShippingAddress: WooAddress{Address1: "123 Main", City: "NYC", Postcode: "10001", Country: "US"},
				ShippingRates: []WooShippingPkg{{
					ShippingRates: []WooShippingRate{{RateID: "flat:1", Selected: false}},
				}},
			},
			want: model.StatusIncomplete,
		},
		{
			name: "ready for complete - physical items",
			cart: &WooCartResponse{
				Items:           []WooCartItem{{Key: "a", ID: 1}},
				BillingAddress:  WooAddress{FirstName: "John", Email: "j@test.com"},
				NeedsShipping:   true,
				ShippingAddress: WooAddress{Address1: "123 Main", City: "NYC", Postcode: "10001", Country: "US"},
				ShippingRates: []WooShippingPkg{{
					ShippingRates: []WooShippingRate{{RateID: "flat:1", Selected: true}},
				}},
			},
			want: model.StatusReadyForComplete,
		},
		{
			name: "ready for complete - digital only",
			cart: &WooCartResponse{
				Items:          []WooCartItem{{Key: "a", ID: 1}},
				BillingAddress: WooAddress{FirstName: "John", Email: "j@test.com"},
				NeedsShipping:  false,
			},
			want: model.StatusReadyForComplete,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := determineCartStatus(tt.cart)
			if got != tt.want {
				t.Errorf("determineCartStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetermineDraftStatus(t *testing.T) {
	readyCart := &WooCartResponse{
		Items:          []WooCartItem{{Key: "a", ID: 1}},
		BillingAddress: WooAddress{FirstName: "John", Email: "j@test.com"},
		NeedsShipping:  false,
	}
	incompleteCart := &WooCartResponse{Items: []WooCartItem{}}

	tests := []struct {
		name     string
		wcStatus string
		cart     *WooCartResponse
		want     model.CheckoutStatus
	}{
		{"checkout-draft with ready cart", "checkout-draft", readyCart, model.StatusReadyForComplete},
		{"checkout-draft with incomplete cart", "checkout-draft", incompleteCart, model.StatusIncomplete},
		{"pending", "pending", nil, model.StatusCompleteInProgress},
		{"processing", "processing", nil, model.StatusCompleteInProgress},
		{"on-hold", "on-hold", nil, model.StatusRequiresEscalation},
		{"completed", "completed", nil, model.StatusCompleted},
		{"cancelled", "cancelled", nil, model.StatusCanceled},
		{"failed", "failed", nil, model.StatusIncomplete},
		{"unknown", "unknown-status", nil, model.StatusIncomplete},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := determineDraftStatus(tt.wcStatus, tt.cart)
			if got != tt.want {
				t.Errorf("determineDraftStatus(%q) = %q, want %q", tt.wcStatus, got, tt.want)
			}
		})
	}
}

func TestHasRequiredShippingFields(t *testing.T) {
	tests := []struct {
		name string
		addr *WooAddress
		want bool
	}{
		{"nil address", nil, false},
		{"empty address", &WooAddress{}, false},
		{"only address1", &WooAddress{Address1: "123 Main"}, false},
		{"missing country", &WooAddress{Address1: "123 Main", City: "NYC", Postcode: "10001"}, false},
		{"missing postcode", &WooAddress{Address1: "123 Main", City: "NYC", Country: "US"}, false},
		{"all required fields", &WooAddress{Address1: "123 Main", City: "NYC", Postcode: "10001", Country: "US"}, true},
		{"with extra fields", &WooAddress{Address1: "123 Main", Address2: "Apt 4", City: "NYC", State: "NY", Postcode: "10001", Country: "US"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasRequiredShippingFields(tt.addr)
			if got != tt.want {
				t.Errorf("hasRequiredShippingFields() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasSelectedShippingRate(t *testing.T) {
	tests := []struct {
		name     string
		packages []WooShippingPkg
		want     bool
	}{
		{"nil packages", nil, false},
		{"empty packages", []WooShippingPkg{}, false},
		{"no rates", []WooShippingPkg{{ShippingRates: []WooShippingRate{}}}, false},
		{"none selected", []WooShippingPkg{{ShippingRates: []WooShippingRate{{RateID: "a", Selected: false}}}}, false},
		{"one selected", []WooShippingPkg{{ShippingRates: []WooShippingRate{{RateID: "a", Selected: true}}}}, true},
		{"second selected", []WooShippingPkg{{ShippingRates: []WooShippingRate{
			{RateID: "a", Selected: false},
			{RateID: "b", Selected: true},
		}}}, true},
		{"multiple packages - second has selection", []WooShippingPkg{
			{ShippingRates: []WooShippingRate{{RateID: "a", Selected: false}}},
			{ShippingRates: []WooShippingRate{{RateID: "b", Selected: true}}},
		}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasSelectedShippingRate(tt.packages)
			if got != tt.want {
				t.Errorf("hasSelectedShippingRate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildTotals(t *testing.T) {
	// WooCommerce Store API returns totals as integer strings in minor units (cents)
	totals := &WooTotals{
		TotalItems:    "10000", // $100.00 = 10000 cents
		TotalDiscount: "1000",  // $10.00 = 1000 cents
		TotalShipping: "500",   // $5.00 = 500 cents
		TotalTax:      "800",   // $8.00 = 800 cents
		TotalFees:     "200",   // $2.00 = 200 cents
		TotalPrice:    "10500", // $105.00 = 10500 cents
	}

	result := buildTotals(totals)

	// Verify required totals are present (values remain in minor units)
	// Per UCP spec: items_discount, subtotal, discount, fulfillment, tax, fee, total
	assertTotalPresent(t, result, model.TotalTypeSubtotal, 9000) // 10000 - 1000
	assertTotalPresent(t, result, model.TotalTypeTotal, 10500)

	// Verify optional totals with values
	assertTotalPresent(t, result, model.TotalTypeItemsDiscount, 1000)
	assertTotalPresent(t, result, model.TotalTypeFulfillment, 500)
	assertTotalPresent(t, result, model.TotalTypeTax, 800)
	assertTotalPresent(t, result, model.TotalTypeFee, 200)
}

func TestBuildTotalsZeroValues(t *testing.T) {
	// WooCommerce Store API returns totals as integer strings in minor units (cents)
	totals := &WooTotals{
		TotalItems:    "5000", // $50.00 = 5000 cents
		TotalDiscount: "0",
		TotalShipping: "0",
		TotalTax:      "0",
		TotalFees:     "0",
		TotalPrice:    "5000", // $50.00 = 5000 cents
	}

	result := buildTotals(totals)

	// Required totals should still be present (values remain in minor units)
	// Per UCP spec: subtotal and total are always included
	assertTotalPresent(t, result, model.TotalTypeSubtotal, 5000)
	assertTotalPresent(t, result, model.TotalTypeTotal, 5000)

	// Zero-value optional totals should be filtered out
	for _, total := range result {
		if total.Amount == 0 &&
			total.Type != model.TotalTypeSubtotal && total.Type != model.TotalTypeTotal {
			t.Errorf("Zero-value optional total %s should be filtered", total.Type)
		}
	}
}

func assertTotalPresent(t *testing.T, totals []model.Total, wantType model.TotalType, wantAmount int64) {
	t.Helper()
	for _, total := range totals {
		if total.Type == wantType {
			if total.Amount != wantAmount {
				t.Errorf("Total %s amount = %d, want %d", wantType, total.Amount, wantAmount)
			}
			return
		}
	}
	t.Errorf("Total type %s not found in results", wantType)
}

func TestTransformLineItem(t *testing.T) {
	wcItem := &WooLineItem{
		ID:       123,
		Name:     "Test Product",
		Quantity: 2,
		Price:    "25.00",
		Subtotal: "45.00", // After discount
		Total:    "45.00",
		Images: []WooImage{
			{Src: "https://example.com/image.jpg"},
		},
	}

	result := transformLineItem(wcItem)

	if result.ID != "123" {
		t.Errorf("ID = %s, want 123", result.ID)
	}
	if result.Item.Title != "Test Product" {
		t.Errorf("Title = %s, want Test Product", result.Item.Title)
	}
	if result.Item.Price != 2500 {
		t.Errorf("Price = %d, want 2500", result.Item.Price)
	}
	if result.Quantity != 2 {
		t.Errorf("Quantity = %d, want 2", result.Quantity)
	}
	if result.BaseAmount != 5000 { // 2500 * 2
		t.Errorf("BaseAmount = %d, want 5000", result.BaseAmount)
	}
	if result.Subtotal != 4500 {
		t.Errorf("Subtotal = %d, want 4500", result.Subtotal)
	}
	if result.Item.ImageURL != "https://example.com/image.jpg" {
		t.Errorf("ImageURL = %s, want https://example.com/image.jpg", result.Item.ImageURL)
	}
}

func TestTransformLineItemNoImage(t *testing.T) {
	wcItem := &WooLineItem{
		ID:       456,
		Name:     "No Image Product",
		Quantity: 1,
		Price:    "10.00",
		Subtotal: "10.00",
		Total:    "10.00",
		Images:   []WooImage{}, // Empty
	}

	result := transformLineItem(wcItem)

	if result.Item.ImageURL != "" {
		t.Errorf("ImageURL = %s, want empty string", result.Item.ImageURL)
	}
}

func TestCheckoutToUCP(t *testing.T) {
	wcResp := &WooCheckoutResponse{
		OrderID:  12345,
		Status:   "pending",
		OrderKey: "wc_order_abc123",
		Totals: WooTotals{
			CurrencyCode:  "USD",
			TotalItems:    "100.00",
			TotalDiscount: "0.00",
			TotalShipping: "10.00",
			TotalTax:      "9.00",
			TotalFees:     "0.00",
			TotalPrice:    "119.00",
		},
		LineItems: []WooLineItem{
			{
				ID:       1,
				Name:     "Widget",
				Quantity: 2,
				Price:    "50.00",
				Subtotal: "100.00",
				Total:    "100.00",
			},
		},
		BillingAddress: WooAddress{
			FirstName: "John",
			LastName:  "Doe",
			Email:     "john@example.com",
		},
	}

	cfg := &model.TransformConfig{
		StoreDomain: "test.example.com",
		StoreURL:    "https://test.example.com",
		UCPVersion:  "2026-01-11",
		Capabilities: map[string][]model.Capability{
			"dev.ucp.shopping": {{Version: "2026-01-11"}},
		},
		PolicyLinks: []model.Link{
			{Type: model.LinkTypePrivacyPolicy, URL: "https://test.example.com/privacy"},
		},
	}

	result := CheckoutToUCP(wcResp, cfg)

	// Verify basic fields
	if result.ID != "gid://test.example.com/Checkout/12345" {
		t.Errorf("ID = %s, want gid://test.example.com/Checkout/12345", result.ID)
	}
	if result.Status != model.StatusCompleteInProgress {
		t.Errorf("Status = %s, want %s", result.Status, model.StatusCompleteInProgress)
	}
	if result.Currency != "USD" {
		t.Errorf("Currency = %s, want USD", result.Currency)
	}

	// Verify line items
	if len(result.LineItems) != 1 {
		t.Fatalf("LineItems length = %d, want 1", len(result.LineItems))
	}
	if result.LineItems[0].Item.Title != "Widget" {
		t.Errorf("LineItem title = %s, want Widget", result.LineItems[0].Item.Title)
	}

	// Verify buyer
	if result.Buyer == nil {
		t.Fatal("Buyer is nil")
	}
	if result.Buyer.Email != "john@example.com" {
		t.Errorf("Buyer email = %s, want john@example.com", result.Buyer.Email)
	}

	// Verify continue URL
	expected := "https://test.example.com/checkout/order-pay/12345/?key=wc_order_abc123"
	if result.ContinueURL != expected {
		t.Errorf("ContinueURL = %s, want %s", result.ContinueURL, expected)
	}

	// Verify UCP metadata
	if result.UCP.Version != "2026-01-11" {
		t.Errorf("UCP.Version = %s, want 2026-01-11", result.UCP.Version)
	}
}

func TestCheckoutToUCPNilInputs(t *testing.T) {
	cfg := &model.TransformConfig{StoreDomain: "test.com"}

	if CheckoutToUCP(nil, cfg) != nil {
		t.Error("Expected nil for nil WC response")
	}

	wcResp := &WooCheckoutResponse{}
	if CheckoutToUCP(wcResp, nil) != nil {
		t.Error("Expected nil for nil config")
	}
}

func TestExtractBuyer(t *testing.T) {
	tests := []struct {
		name    string
		addr    *WooAddress
		wantNil bool
	}{
		{
			name:    "nil address",
			addr:    nil,
			wantNil: true,
		},
		{
			name:    "empty address",
			addr:    &WooAddress{},
			wantNil: true,
		},
		{
			name:    "only company (no buyer)",
			addr:    &WooAddress{Company: "Acme Inc"},
			wantNil: true,
		},
		{
			name: "has first name",
			addr: &WooAddress{FirstName: "John"},
		},
		{
			name: "has email only",
			addr: &WooAddress{Email: "test@example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractBuyer(tt.addr)
			if tt.wantNil && result != nil {
				t.Error("Expected nil buyer")
			}
			if !tt.wantNil && result == nil {
				t.Error("Expected non-nil buyer")
			}
		})
	}
}

func TestAddressFromUCP(t *testing.T) {
	addr := &model.PostalAddress{
		StreetAddress:   "123 Main St",
		ExtendedAddress: "Apt 4",
		Locality:        "San Francisco",
		Region:          "CA",
		Country:         "US",
		PostalCode:      "94102",
		FirstName:       "Jane",
		LastName:        "Smith",
		PhoneNumber:     "+14155551234",
	}

	result := AddressFromUCP(addr)

	if result.FirstName != "Jane" {
		t.Errorf("FirstName = %s, want Jane", result.FirstName)
	}
	if result.Address1 != "123 Main St" {
		t.Errorf("Address1 = %s, want 123 Main St", result.Address1)
	}
	if result.City != "San Francisco" {
		t.Errorf("City = %s, want San Francisco", result.City)
	}
	if result.State != "CA" {
		t.Errorf("State = %s, want CA", result.State)
	}
}

func TestAddressFromUCPOnlyStreetAddress(t *testing.T) {
	// Test that address transform works with just street address (minimal fields)
	addr := &model.PostalAddress{
		StreetAddress: "456 Oak Ave",
	}

	result := AddressFromUCP(addr)

	if result.Address1 != "456 Oak Ave" {
		t.Errorf("Address1 = %s, want 456 Oak Ave", result.Address1)
	}
	// FirstName/LastName should be empty when not provided
	if result.FirstName != "" {
		t.Errorf("FirstName = %s, want empty", result.FirstName)
	}
	if result.LastName != "" {
		t.Errorf("LastName = %s, want empty", result.LastName)
	}
}

func TestAddressFromUCPNil(t *testing.T) {
	if AddressFromUCP(nil) != nil {
		t.Error("Expected nil for nil input")
	}
}

func TestExtractCartToken(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantToken string
	}{
		{
			name:      "checkout ID with token",
			input:     "gid://example.com/Checkout/123:abc123token",
			wantToken: "abc123token",
		},
		{
			name:      "checkout ID without token",
			input:     "gid://example.com/Checkout/456",
			wantToken: "",
		},
		{
			name:      "cart ID with token",
			input:     "gid://example.com/Cart/abc123token",
			wantToken: "abc123token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extracted := ExtractCartToken(tt.input)
			if extracted != tt.wantToken {
				t.Errorf("ExtractCartToken() = %s, want %s", extracted, tt.wantToken)
			}
		})
	}
}

func TestParseCheckoutID(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantIsCart  bool
		wantOrderID int
		wantToken   string
		wantErr     bool
	}{
		{
			name:        "cart ID",
			input:       "gid://example.com/Cart/abc123token",
			wantIsCart:  true,
			wantOrderID: 0,
			wantToken:   "abc123token",
		},
		{
			name:        "checkout ID with token",
			input:       "gid://example.com/Checkout/123:abc123",
			wantIsCart:  false,
			wantOrderID: 123,
			wantToken:   "abc123",
		},
		{
			name:        "checkout ID without token",
			input:       "gid://example.com/Checkout/456",
			wantIsCart:  false,
			wantOrderID: 456,
			wantToken:   "",
		},
		{
			name:    "invalid format - no gid prefix",
			input:   "invalid/Checkout/123",
			wantErr: true,
		},
		{
			name:    "invalid format - unknown type",
			input:   "gid://example.com/Order/123",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isCart, orderID, token, err := ParseCheckoutID(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseCheckoutID(%s) expected error", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("ParseCheckoutID(%s) error = %v", tt.input, err)
				return
			}
			if isCart != tt.wantIsCart {
				t.Errorf("isCart = %v, want %v", isCart, tt.wantIsCart)
			}
			if orderID != tt.wantOrderID {
				t.Errorf("orderID = %d, want %d", orderID, tt.wantOrderID)
			}
			if token != tt.wantToken {
				t.Errorf("token = %s, want %s", token, tt.wantToken)
			}
		})
	}
}

func TestBuildCheckoutID(t *testing.T) {
	tests := []struct {
		name      string
		domain    string
		orderID   int
		cartToken string
		want      string
	}{
		{
			name:      "without cart token",
			domain:    "shop.example.com",
			orderID:   999,
			cartToken: "",
			want:      "gid://shop.example.com/Checkout/999",
		},
		{
			name:      "with cart token",
			domain:    "shop.example.com",
			orderID:   123,
			cartToken: "abc123token",
			want:      "gid://shop.example.com/Checkout/123:abc123token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildCheckoutID(tt.domain, tt.orderID, tt.cartToken)
			if result != tt.want {
				t.Errorf("BuildCheckoutID() = %s, want %s", result, tt.want)
			}
		})
	}
}

func TestBuildCartID(t *testing.T) {
	result := BuildCartID("shop.example.com", "abc123token")
	want := "gid://shop.example.com/Cart/abc123token"
	if result != want {
		t.Errorf("BuildCartID() = %s, want %s", result, want)
	}
}

func TestBuildContinueURL(t *testing.T) {
	tests := []struct {
		name     string
		storeURL string
		orderID  int
		orderKey string
		want     string
	}{
		{
			name:     "with order key",
			storeURL: "https://shop.com",
			orderID:  123,
			orderKey: "wc_order_xyz",
			want:     "https://shop.com/checkout/order-pay/123/?key=wc_order_xyz",
		},
		{
			name:     "without order key",
			storeURL: "https://shop.com",
			orderID:  456,
			orderKey: "",
			want:     "https://shop.com/checkout/order-pay/456/",
		},
		{
			name:     "trailing slash in URL",
			storeURL: "https://shop.com/",
			orderID:  789,
			orderKey: "key123",
			want:     "https://shop.com/checkout/order-pay/789/?key=key123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildContinueURL(tt.storeURL, tt.orderID, tt.orderKey)
			if got != tt.want {
				t.Errorf("buildContinueURL() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestBuildOrderPermalinkURL(t *testing.T) {
	tests := []struct {
		name     string
		storeURL string
		orderID  int
		orderKey string
		want     string
	}{
		{
			name:     "with order key",
			storeURL: "https://shop.com",
			orderID:  123,
			orderKey: "wc_order_xyz",
			want:     "https://shop.com/checkout/order-received/123/?key=wc_order_xyz",
		},
		{
			name:     "without order key",
			storeURL: "https://shop.com",
			orderID:  456,
			orderKey: "",
			want:     "https://shop.com/checkout/order-received/456/",
		},
		{
			name:     "trailing slash in URL",
			storeURL: "https://shop.com/",
			orderID:  789,
			orderKey: "key123",
			want:     "https://shop.com/checkout/order-received/789/?key=key123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildOrderPermalinkURL(tt.storeURL, tt.orderID, tt.orderKey)
			if got != tt.want {
				t.Errorf("buildOrderPermalinkURL() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestOrderIDFormat(t *testing.T) {
	// Verify the expected gid format for order IDs: gid://{domain}/Order/{order_id}
	// This format is used in client.go buildCompletionResponse
	tests := []struct {
		name    string
		domain  string
		orderID int
		want    string
	}{
		{
			name:    "standard order",
			domain:  "shop.example.com",
			orderID: 12345,
			want:    "gid://shop.example.com/Order/12345",
		},
		{
			name:    "order ID 1",
			domain:  "test.com",
			orderID: 1,
			want:    "gid://test.com/Order/1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Using the same format as client.go: fmt.Sprintf("gid://%s/Order/%d", domain, orderID)
			got := "gid://" + tt.domain + "/Order/" + formatInt(tt.orderID)
			if got != tt.want {
				t.Errorf("Order ID format = %s, want %s", got, tt.want)
			}
		})
	}
}

// formatInt converts int to string for testing (mirrors fmt.Sprintf %d)
func formatInt(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + formatInt(-n)
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

// === Discount Capability Tests ===

func TestTransformCoupons(t *testing.T) {
	tests := []struct {
		name    string
		coupons []WooCoupon
		want    *model.Discounts
	}{
		{
			name:    "empty coupons returns nil",
			coupons: []WooCoupon{},
			want:    nil,
		},
		{
			name:    "nil coupons returns nil",
			coupons: nil,
			want:    nil,
		},
		{
			name: "single coupon",
			coupons: []WooCoupon{
				{Code: "SAVE10", Totals: WooCouponTotals{TotalDiscount: "1000"}},
			},
			want: &model.Discounts{
				Codes: []string{"SAVE10"},
				Applied: []model.AppliedDiscount{
					{Code: "SAVE10", Title: "SAVE10", Amount: 1000},
				},
			},
		},
		{
			name: "multiple coupons",
			coupons: []WooCoupon{
				{Code: "SAVE10", Totals: WooCouponTotals{TotalDiscount: "1000"}},
				{Code: "FREESHIP", Totals: WooCouponTotals{TotalDiscount: "500"}},
			},
			want: &model.Discounts{
				Codes: []string{"SAVE10", "FREESHIP"},
				Applied: []model.AppliedDiscount{
					{Code: "SAVE10", Title: "SAVE10", Amount: 1000},
					{Code: "FREESHIP", Title: "FREESHIP", Amount: 500},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := transformCoupons(tt.coupons)
			if tt.want == nil {
				if got != nil {
					t.Errorf("transformCoupons() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("transformCoupons() = nil, want %v", tt.want)
			}
			if len(got.Codes) != len(tt.want.Codes) {
				t.Errorf("Codes length = %d, want %d", len(got.Codes), len(tt.want.Codes))
			}
			for i, code := range got.Codes {
				if code != tt.want.Codes[i] {
					t.Errorf("Codes[%d] = %s, want %s", i, code, tt.want.Codes[i])
				}
			}
			if len(got.Applied) != len(tt.want.Applied) {
				t.Errorf("Applied length = %d, want %d", len(got.Applied), len(tt.want.Applied))
			}
			for i, applied := range got.Applied {
				if applied.Code != tt.want.Applied[i].Code {
					t.Errorf("Applied[%d].Code = %s, want %s", i, applied.Code, tt.want.Applied[i].Code)
				}
				if applied.Amount != tt.want.Applied[i].Amount {
					t.Errorf("Applied[%d].Amount = %d, want %d", i, applied.Amount, tt.want.Applied[i].Amount)
				}
			}
		})
	}
}

func TestCartToUCPWithDiscounts(t *testing.T) {
	cart := &WooCartResponse{
		Items: []WooCartItem{
			{
				Key:      "item-1",
				ID:       60,
				Name:     "Test Product",
				Quantity: 1,
				Prices:   WooCartItemPrices{Price: "2500"},
				Totals:   WooCartItemTotals{LineSubtotal: "2250", LineTotal: "2250"},
			},
		},
		Coupons: []WooCoupon{
			{Code: "10OFF", Totals: WooCouponTotals{TotalDiscount: "250"}}, // $2.50 discount
		},
		Totals: WooTotals{
			CurrencyCode:  "USD",
			TotalItems:    "2500",
			TotalDiscount: "250",
			TotalPrice:    "2250",
		},
		BillingAddress: WooAddress{
			FirstName: "Test",
			Email:     "test@example.com",
		},
	}

	cfg := &model.TransformConfig{
		StoreDomain: "test.example.com",
		StoreURL:    "https://test.example.com",
		UCPVersion:  "2026-01-11",
		Capabilities: map[string][]model.Capability{
			"dev.ucp.shopping": {{Version: "2026-01-11"}},
		},
		PolicyLinks:     []model.Link{},
		PaymentHandlers: map[string][]model.PaymentHandler{},
	}

	checkout := CartToUCP(cart, "test-token", cfg)

	// Verify discounts object is present
	if checkout.Discounts == nil {
		t.Fatal("expected Discounts to be non-nil")
	}

	if len(checkout.Discounts.Codes) != 1 {
		t.Errorf("Discounts.Codes length = %d, want 1", len(checkout.Discounts.Codes))
	}

	if checkout.Discounts.Codes[0] != "10OFF" {
		t.Errorf("Discounts.Codes[0] = %s, want 10OFF", checkout.Discounts.Codes[0])
	}

	if len(checkout.Discounts.Applied) != 1 {
		t.Errorf("Discounts.Applied length = %d, want 1", len(checkout.Discounts.Applied))
	}

	applied := checkout.Discounts.Applied[0]
	if applied.Code != "10OFF" {
		t.Errorf("Applied[0].Code = %s, want 10OFF", applied.Code)
	}
	if applied.Amount != 250 {
		t.Errorf("Applied[0].Amount = %d, want 250", applied.Amount)
	}

	// Verify totals include discount
	var itemsDiscount int64
	for _, total := range checkout.Totals {
		if total.Type == model.TotalTypeItemsDiscount {
			itemsDiscount = total.Amount
		}
	}
	if itemsDiscount != 250 {
		t.Errorf("items_discount total = %d, want 250", itemsDiscount)
	}
}

func TestCartToUCPWithoutDiscounts(t *testing.T) {
	cart := &WooCartResponse{
		Items: []WooCartItem{
			{
				Key:      "item-1",
				ID:       60,
				Name:     "Test Product",
				Quantity: 1,
				Prices:   WooCartItemPrices{Price: "2500"},
				Totals:   WooCartItemTotals{LineSubtotal: "2500", LineTotal: "2500"},
			},
		},
		Coupons: []WooCoupon{}, // No coupons
		Totals: WooTotals{
			CurrencyCode: "USD",
			TotalItems:   "2500",
			TotalPrice:   "2500",
		},
	}

	cfg := &model.TransformConfig{
		StoreDomain:     "test.example.com",
		StoreURL:        "https://test.example.com",
		UCPVersion:      "2026-01-11",
		Capabilities:    map[string][]model.Capability{},
		PolicyLinks:     []model.Link{},
		PaymentHandlers: map[string][]model.PaymentHandler{},
	}

	checkout := CartToUCP(cart, "test-token", cfg)

	// Verify discounts object is nil (not empty object) when no coupons
	if checkout.Discounts != nil {
		t.Errorf("expected Discounts to be nil when no coupons, got %v", checkout.Discounts)
	}
}

// === Fulfillment Capability Tests ===

func TestTransformFulfillment(t *testing.T) {
	tests := []struct {
		name           string
		packages       []WooShippingPkg
		wantOptions    []model.FulfillmentOption
		wantSelectedID string
	}{
		{
			name:           "empty packages",
			packages:       []WooShippingPkg{},
			wantOptions:    nil,
			wantSelectedID: "",
		},
		{
			name: "single rate not selected",
			packages: []WooShippingPkg{
				{
					ShippingRates: []WooShippingRate{
						{RateID: "flat_rate:1", Name: "Standard Shipping", Price: "599", Selected: false},
					},
				},
			},
			wantOptions: []model.FulfillmentOption{
				{ID: "flat_rate:1", Type: "shipping", Title: "Standard Shipping", Subtotal: 599, Total: 599},
			},
			wantSelectedID: "",
		},
		{
			name: "single rate selected",
			packages: []WooShippingPkg{
				{
					ShippingRates: []WooShippingRate{
						{RateID: "flat_rate:1", Name: "Standard Shipping", Price: "599", Selected: true},
					},
				},
			},
			wantOptions: []model.FulfillmentOption{
				{ID: "flat_rate:1", Type: "shipping", Title: "Standard Shipping", Subtotal: 599, Total: 599},
			},
			wantSelectedID: "flat_rate:1",
		},
		{
			name: "multiple rates with one selected",
			packages: []WooShippingPkg{
				{
					ShippingRates: []WooShippingRate{
						{RateID: "flat_rate:1", Name: "Standard Shipping", Price: "599", Selected: false},
						{RateID: "express:2", Name: "Express Shipping", Price: "1499", Selected: true},
						{RateID: "free_shipping:3", Name: "Free Shipping", Price: "0", Selected: false},
					},
				},
			},
			wantOptions: []model.FulfillmentOption{
				{ID: "flat_rate:1", Type: "shipping", Title: "Standard Shipping", Subtotal: 599, Total: 599},
				{ID: "express:2", Type: "shipping", Title: "Express Shipping", Subtotal: 1499, Total: 1499},
				{ID: "free_shipping:3", Type: "shipping", Title: "Free Shipping", Subtotal: 0, Total: 0},
			},
			wantSelectedID: "express:2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOptions, gotSelectedID := transformFulfillment(tt.packages)

			if len(gotOptions) != len(tt.wantOptions) {
				t.Fatalf("options length = %d, want %d", len(gotOptions), len(tt.wantOptions))
			}

			for i, opt := range gotOptions {
				want := tt.wantOptions[i]
				if opt.ID != want.ID {
					t.Errorf("options[%d].ID = %s, want %s", i, opt.ID, want.ID)
				}
				if opt.Type != want.Type {
					t.Errorf("options[%d].Type = %s, want %s", i, opt.Type, want.Type)
				}
				if opt.Title != want.Title {
					t.Errorf("options[%d].Title = %s, want %s", i, opt.Title, want.Title)
				}
				if opt.Subtotal != want.Subtotal {
					t.Errorf("options[%d].Subtotal = %d, want %d", i, opt.Subtotal, want.Subtotal)
				}
				if opt.Total != want.Total {
					t.Errorf("options[%d].Total = %d, want %d", i, opt.Total, want.Total)
				}
			}

			if gotSelectedID != tt.wantSelectedID {
				t.Errorf("selectedID = %s, want %s", gotSelectedID, tt.wantSelectedID)
			}
		})
	}
}

func TestExtractFulfillmentAddress(t *testing.T) {
	tests := []struct {
		name    string
		addr    *WooAddress
		wantNil bool
	}{
		{
			name:    "nil address",
			addr:    nil,
			wantNil: true,
		},
		{
			name:    "empty address",
			addr:    &WooAddress{},
			wantNil: true,
		},
		{
			name: "full address",
			addr: &WooAddress{
				FirstName: "John",
				LastName:  "Doe",
				Address1:  "123 Main St",
				Address2:  "Apt 4B",
				City:      "New York",
				State:     "NY",
				Country:   "US",
				Postcode:  "10001",
				Phone:     "+15551234567",
			},
			wantNil: false,
		},
		{
			name:    "only city (partial address)",
			addr:    &WooAddress{City: "Seattle"},
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractFulfillmentAddress(tt.addr)
			if tt.wantNil {
				if result != nil {
					t.Error("expected nil fulfillment address")
				}
				return
			}
			if result == nil {
				t.Fatal("expected non-nil fulfillment address")
			}
			// Verify field mapping for full address test
			if tt.addr.FirstName != "" {
				if result.FirstName != tt.addr.FirstName {
					t.Errorf("FirstName = %s, want %s", result.FirstName, tt.addr.FirstName)
				}
				if result.StreetAddress != tt.addr.Address1 {
					t.Errorf("StreetAddress = %s, want %s", result.StreetAddress, tt.addr.Address1)
				}
				if result.Locality != tt.addr.City {
					t.Errorf("Locality = %s, want %s", result.Locality, tt.addr.City)
				}
				if result.Region != tt.addr.State {
					t.Errorf("Region = %s, want %s", result.Region, tt.addr.State)
				}
				if result.Country != tt.addr.Country {
					t.Errorf("Country = %s, want %s", result.Country, tt.addr.Country)
				}
			}
		})
	}
}

func TestCartToUCPWithFulfillment(t *testing.T) {
	cart := &WooCartResponse{
		Items: []WooCartItem{
			{
				Key:      "item-1",
				ID:       60,
				Name:     "Test Product",
				Quantity: 1,
				Prices:   WooCartItemPrices{Price: "2500"},
				Totals:   WooCartItemTotals{LineSubtotal: "2500", LineTotal: "2500"},
			},
		},
		ShippingRates: []WooShippingPkg{
			{
				ShippingRates: []WooShippingRate{
					{RateID: "flat_rate:1", Name: "Standard Shipping", Price: "599", Selected: true},
					{RateID: "express:2", Name: "Express Shipping", Price: "1499", Selected: false},
				},
			},
		},
		ShippingAddress: WooAddress{
			FirstName: "Jane",
			LastName:  "Smith",
			Address1:  "456 Oak Ave",
			City:      "Portland",
			State:     "OR",
			Country:   "US",
			Postcode:  "97201",
		},
		Totals: WooTotals{
			CurrencyCode:  "USD",
			TotalItems:    "2500",
			TotalShipping: "599",
			TotalPrice:    "3099",
		},
		BillingAddress: WooAddress{
			FirstName: "Jane",
			LastName:  "Smith",
			Email:     "jane@example.com",
		},
	}

	cfg := &model.TransformConfig{
		StoreDomain: "test.example.com",
		StoreURL:    "https://test.example.com",
		UCPVersion:  "2026-01-11",
		Capabilities: map[string][]model.Capability{
			"dev.ucp.shopping.fulfillment": {{Version: "2026-01-11"}},
		},
		PolicyLinks:     []model.Link{},
		PaymentHandlers: map[string][]model.PaymentHandler{},
	}

	checkout := CartToUCP(cart, "test-token", cfg)

	// Verify fulfillment options are populated
	if len(checkout.FulfillmentOptions) != 2 {
		t.Fatalf("FulfillmentOptions length = %d, want 2", len(checkout.FulfillmentOptions))
	}

	// First option should be Standard Shipping
	if checkout.FulfillmentOptions[0].ID != "flat_rate:1" {
		t.Errorf("FulfillmentOptions[0].ID = %s, want flat_rate:1", checkout.FulfillmentOptions[0].ID)
	}
	if checkout.FulfillmentOptions[0].Type != "shipping" {
		t.Errorf("FulfillmentOptions[0].Type = %s, want shipping", checkout.FulfillmentOptions[0].Type)
	}
	if checkout.FulfillmentOptions[0].Title != "Standard Shipping" {
		t.Errorf("FulfillmentOptions[0].Title = %s, want Standard Shipping", checkout.FulfillmentOptions[0].Title)
	}
	if checkout.FulfillmentOptions[0].Subtotal != 599 {
		t.Errorf("FulfillmentOptions[0].Subtotal = %d, want 599", checkout.FulfillmentOptions[0].Subtotal)
	}

	// Verify selected option ID
	if checkout.FulfillmentOptionID != "flat_rate:1" {
		t.Errorf("FulfillmentOptionID = %s, want flat_rate:1", checkout.FulfillmentOptionID)
	}

	// Verify fulfillment address is populated
	if checkout.FulfillmentAddress == nil {
		t.Fatal("FulfillmentAddress is nil")
	}
	if checkout.FulfillmentAddress.FirstName != "Jane" {
		t.Errorf("FulfillmentAddress.FirstName = %s, want Jane", checkout.FulfillmentAddress.FirstName)
	}
	if checkout.FulfillmentAddress.StreetAddress != "456 Oak Ave" {
		t.Errorf("FulfillmentAddress.StreetAddress = %s, want 456 Oak Ave", checkout.FulfillmentAddress.StreetAddress)
	}
	if checkout.FulfillmentAddress.Locality != "Portland" {
		t.Errorf("FulfillmentAddress.Locality = %s, want Portland", checkout.FulfillmentAddress.Locality)
	}
}

func TestCartToUCPWithoutFulfillment(t *testing.T) {
	cart := &WooCartResponse{
		Items: []WooCartItem{
			{
				Key:      "item-1",
				ID:       60,
				Name:     "Digital Product",
				Quantity: 1,
				Prices:   WooCartItemPrices{Price: "1000"},
				Totals:   WooCartItemTotals{LineSubtotal: "1000", LineTotal: "1000"},
			},
		},
		NeedsShipping: false,
		ShippingRates: []WooShippingPkg{}, // No shipping rates
		Totals: WooTotals{
			CurrencyCode: "USD",
			TotalItems:   "1000",
			TotalPrice:   "1000",
		},
	}

	cfg := &model.TransformConfig{
		StoreDomain:     "test.example.com",
		StoreURL:        "https://test.example.com",
		UCPVersion:      "2026-01-11",
		Capabilities:    map[string][]model.Capability{},
		PolicyLinks:     []model.Link{},
		PaymentHandlers: map[string][]model.PaymentHandler{},
	}

	checkout := CartToUCP(cart, "test-token", cfg)

	// Verify fulfillment fields are empty/nil for digital products
	if len(checkout.FulfillmentOptions) != 0 {
		t.Errorf("FulfillmentOptions should be empty for digital products, got %d options", len(checkout.FulfillmentOptions))
	}
	if checkout.FulfillmentOptionID != "" {
		t.Errorf("FulfillmentOptionID should be empty, got %s", checkout.FulfillmentOptionID)
	}
	if checkout.FulfillmentAddress != nil {
		t.Error("FulfillmentAddress should be nil for digital products")
	}
}
