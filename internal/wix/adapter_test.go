package wix

import (
	"testing"

	"ucp-proxy/internal/model"
)

func newTestTransformConfig() *model.TransformConfig {
	return &model.TransformConfig{
		StoreDomain: "test-site-id",
		StoreURL:    "https://test-store.wixsite.com",
		UCPVersion:  "2026-01-11",
		Capabilities: map[string][]model.Capability{
			"dev.ucp.shopping": {{Version: "2026-01-11"}},
		},
		PolicyLinks:     []model.Link{{Type: model.LinkTypePrivacyPolicy, URL: "https://test-store.wixsite.com/privacy"}},
		PaymentHandlers: map[string][]model.PaymentHandler{}, // Wix always escalates to browser, no programmatic payment handlers
	}
}

func TestCheckoutIDEncodeDecode(t *testing.T) {
	tests := []struct {
		name          string
		siteID        string
		checkoutID    string
		instanceToken string
	}{
		{
			name:          "basic encoding",
			siteID:        "site-123",
			checkoutID:    "checkout-456",
			instanceToken: "instance-token-abc",
		},
		{
			name:          "UUID format",
			siteID:        "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
			checkoutID:    "f0e9d8c7-b6a5-4321-fedc-ba0987654321",
			instanceToken: "IST.eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded := BuildCheckoutID(tc.siteID, tc.checkoutID, tc.instanceToken)

			// Decode
			gotCheckoutID, gotInstanceToken, err := ParseCheckoutID(encoded)
			if err != nil {
				t.Fatalf("ParseCheckoutID: %v", err)
			}

			if gotCheckoutID != tc.checkoutID {
				t.Errorf("checkoutID = %s, want %s", gotCheckoutID, tc.checkoutID)
			}
			if gotInstanceToken != tc.instanceToken {
				t.Errorf("instanceToken = %s, want %s", gotInstanceToken, tc.instanceToken)
			}
		})
	}
}

func TestParseCheckoutIDErrors(t *testing.T) {
	tests := []struct {
		name    string
		gid     string
		wantErr bool
	}{
		{
			name:    "empty string",
			gid:     "",
			wantErr: true,
		},
		{
			name:    "wrong prefix",
			gid:     "gid://shopify/Checkout/123:abc",
			wantErr: true,
		},
		{
			name:    "missing checkout path",
			gid:     "gid://wix.site123/Cart/abc",
			wantErr: true,
		},
		{
			name:    "missing instance token",
			gid:     "gid://wix.site123/Checkout/abc",
			wantErr: true,
		},
		{
			name:    "empty checkout ID",
			gid:     "gid://wix.site123/Checkout/:token",
			wantErr: true,
		},
		{
			name:    "valid ID",
			gid:     "gid://wix.site123/Checkout/abc:token",
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ParseCheckoutID(tc.gid)
			if (err != nil) != tc.wantErr {
				t.Errorf("ParseCheckoutID() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestDetermineStatus(t *testing.T) {
	tests := []struct {
		name     string
		checkout *WixCheckout
		want     model.CheckoutStatus
	}{
		{
			name:     "nil checkout",
			checkout: nil,
			want:     model.StatusIncomplete,
		},
		{
			name:     "empty line items",
			checkout: &WixCheckout{},
			want:     model.StatusIncomplete,
		},
		{
			name: "no buyer email",
			checkout: &WixCheckout{
				LineItems: []WixLineItem{{ID: "item1"}},
			},
			want: model.StatusIncomplete,
		},
		{
			name: "has email but needs shipping without address",
			checkout: &WixCheckout{
				LineItems: []WixLineItem{{
					ID:                 "item1",
					PhysicalProperties: &WixPhysicalProps{ShippingRequired: true},
				}},
				BuyerInfo: &WixBuyerInfo{Email: "test@example.com"},
			},
			want: model.StatusIncomplete,
		},
		{
			name: "has email and address but no shipping selected",
			checkout: &WixCheckout{
				LineItems: []WixLineItem{{
					ID:                 "item1",
					PhysicalProperties: &WixPhysicalProps{ShippingRequired: true},
				}},
				BuyerInfo: &WixBuyerInfo{Email: "test@example.com"},
				ShippingInfo: &WixShippingInfo{
					ShippingDestination: &WixShippingDestination{
						Address: &WixAddress{
							AddressLine: "123 Main St",
							City:        "San Francisco",
							Country:     "US",
						},
					},
				},
				AvailableShippingOptions: []WixShippingOption{
					{Code: "standard", Title: "Standard"},
				},
			},
			want: model.StatusIncomplete,
		},
		{
			name: "complete checkout ready for escalation",
			checkout: &WixCheckout{
				LineItems: []WixLineItem{{
					ID:                 "item1",
					PhysicalProperties: &WixPhysicalProps{ShippingRequired: true},
				}},
				BuyerInfo: &WixBuyerInfo{Email: "test@example.com"},
				ShippingInfo: &WixShippingInfo{
					ShippingDestination: &WixShippingDestination{
						Address: &WixAddress{
							AddressLine: "123 Main St",
							City:        "San Francisco",
							Country:     "US",
						},
					},
				},
				AvailableShippingOptions: []WixShippingOption{
					{Code: "standard", Title: "Standard"},
				},
				SelectedShippingOption: &WixSelectedShipping{Code: "standard"},
			},
			want: model.StatusRequiresEscalation,
		},
		{
			name: "digital product - no shipping needed",
			checkout: &WixCheckout{
				LineItems: []WixLineItem{{
					ID:                 "digital1",
					PhysicalProperties: &WixPhysicalProps{ShippingRequired: false},
				}},
				BuyerInfo: &WixBuyerInfo{Email: "test@example.com"},
			},
			want: model.StatusRequiresEscalation,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := determineStatus(tc.checkout)
			if got != tc.want {
				t.Errorf("determineStatus() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestTransformLineItems(t *testing.T) {
	items := []WixLineItem{
		{
			ID: "item1",
			CatalogReference: &WixCatalogRef{
				CatalogItemID: "prod-123",
				AppID:         WixStoresAppID,
			},
			ProductName: &WixProductName{
				Original:   "Test Product",
				Translated: "Test Product Translated",
			},
			Price: &WixPrice{
				Amount: "29.99",
			},
			Quantity: 2,
			Image: &WixImage{
				URL: "https://static.wixstatic.com/image.jpg",
			},
		},
	}

	result := transformLineItems(items)

	if len(result) != 1 {
		t.Fatalf("expected 1 item, got %d", len(result))
	}

	item := result[0]
	if item.ID != "item1" {
		t.Errorf("ID = %s, want item1", item.ID)
	}
	if item.Item.ID != "prod-123" {
		t.Errorf("Item.ID = %s, want prod-123", item.Item.ID)
	}
	if item.Item.Title != "Test Product Translated" {
		t.Errorf("Item.Title = %s, want Test Product Translated", item.Item.Title)
	}
	if item.Item.Price != 2999 { // $29.99 in cents
		t.Errorf("Item.Price = %d, want 2999", item.Item.Price)
	}
	if item.Quantity != 2 {
		t.Errorf("Quantity = %d, want 2", item.Quantity)
	}
	if item.BaseAmount != 5998 { // 2999 * 2
		t.Errorf("BaseAmount = %d, want 5998", item.BaseAmount)
	}
}

func TestBuildTotals(t *testing.T) {
	ps := &WixPriceSummary{
		Subtotal: &WixPrice{Amount: "100.00"},
		Shipping: &WixPrice{Amount: "5.99"},
		Tax:      &WixPrice{Amount: "8.50"},
		Discount: &WixPrice{Amount: "10.00"},
		Total:    &WixPrice{Amount: "104.49"},
	}

	totals := buildTotals(ps)

	// Per UCP spec: subtotal, discount, fulfillment, tax, total
	if len(totals) < 4 {
		t.Fatalf("expected at least 4 totals, got %d", len(totals))
	}

	// Verify total is correct
	var found bool
	for _, total := range totals {
		if total.Type == model.TotalTypeTotal {
			found = true
			if total.Amount != 10449 { // $104.49 in cents
				t.Errorf("Total = %d, want 10449", total.Amount)
			}
		}
	}
	if !found {
		t.Error("TotalTypeTotal not found in totals")
	}
}

func TestAddressTransforms(t *testing.T) {
	// UCP → Wix
	ucpAddr := &model.PostalAddress{
		FirstName:     "John",
		LastName:      "Doe",
		StreetAddress: "123 Main St",
		Locality:      "San Francisco",
		Region:        "CA",
		Country:       "US",
		PostalCode:    "94102",
		PhoneNumber:   "+1234567890",
	}

	wixDest := AddressToWix(ucpAddr)

	// AddressToWix returns WixShippingDestination with nested Address and ContactDetails
	if wixDest.Address.AddressLine != "123 Main St" {
		t.Errorf("Address.AddressLine = %s, want 123 Main St", wixDest.Address.AddressLine)
	}
	if wixDest.Address.City != "San Francisco" {
		t.Errorf("Address.City = %s, want San Francisco", wixDest.Address.City)
	}
	if wixDest.Address.Subdivision != "US-CA" {
		t.Errorf("Address.Subdivision = %s, want US-CA", wixDest.Address.Subdivision)
	}
	if wixDest.Address.Country != "US" {
		t.Errorf("Address.Country = %s, want US", wixDest.Address.Country)
	}
	if wixDest.ContactDetails.FirstName != "John" {
		t.Errorf("ContactDetails.FirstName = %s, want John", wixDest.ContactDetails.FirstName)
	}
}

func TestCheckoutToUCP(t *testing.T) {
	cfg := newTestTransformConfig()

	wc := &WixCheckout{
		ID:       "checkout-123",
		Currency: "USD",
		LineItems: []WixLineItem{
			{
				ID:       "item1",
				Quantity: 1,
				CatalogReference: &WixCatalogRef{
					CatalogItemID: "prod-1",
				},
				ProductName: &WixProductName{Original: "Test Product"},
				Price:       &WixPrice{Amount: "29.99"},
			},
		},
		BuyerInfo: &WixBuyerInfo{
			Email:     "test@example.com",
			FirstName: "John",
			LastName:  "Doe",
		},
		PriceSummary: &WixPriceSummary{
			Subtotal: &WixPrice{Amount: "29.99"},
			Total:    &WixPrice{Amount: "29.99"},
		},
	}

	checkout := CheckoutToUCP(wc, "instance-token", cfg)

	if checkout == nil {
		t.Fatal("expected non-nil checkout")
	}

	// Verify ID encoding
	if checkout.ID == "" {
		t.Error("expected non-empty checkout ID")
	}

	// Verify currency
	if checkout.Currency != "USD" {
		t.Errorf("Currency = %s, want USD", checkout.Currency)
	}

	// Verify buyer
	if checkout.Buyer == nil {
		t.Fatal("expected non-nil buyer")
	}
	if checkout.Buyer.Email != "test@example.com" {
		t.Errorf("Buyer.Email = %s, want test@example.com", checkout.Buyer.Email)
	}

	// Verify line items
	if len(checkout.LineItems) != 1 {
		t.Errorf("LineItems = %d, want 1", len(checkout.LineItems))
	}

	// Verify UCP metadata
	if checkout.UCP.Version != "2026-01-11" {
		t.Errorf("UCP.Version = %s, want 2026-01-11", checkout.UCP.Version)
	}
}

func TestExtractFulfillmentAddress(t *testing.T) {
	si := &WixShippingInfo{
		ShippingDestination: &WixShippingDestination{
			Address: &WixAddress{
				AddressLine:  "123 Main St",
				AddressLine2: "Apt 4",
				City:         "San Francisco",
				Subdivision:  "US-CA",
				Country:      "US",
				PostalCode:   "94102",
			},
			ContactDetails: &WixContactDetails{
				FirstName: "John",
				LastName:  "Doe",
				Phone:     "+1234567890",
			},
		},
	}

	addr := extractFulfillmentAddress(si)

	if addr == nil {
		t.Fatal("expected non-nil address")
	}
	if addr.StreetAddress != "123 Main St" {
		t.Errorf("StreetAddress = %s, want 123 Main St", addr.StreetAddress)
	}
	if addr.ExtendedAddress != "Apt 4" {
		t.Errorf("ExtendedAddress = %s, want Apt 4", addr.ExtendedAddress)
	}
	if addr.Region != "CA" { // Should extract "CA" from "US-CA"
		t.Errorf("Region = %s, want CA", addr.Region)
	}
	if addr.FirstName != "John" {
		t.Errorf("FirstName = %s, want John", addr.FirstName)
	}
}

func TestExtractShippingOptions(t *testing.T) {
	// extractShippingOptions reads from shippingInfo.carrierServiceOptions[].shippingOptions[]
	// and cost is nested at cost.price.amount
	info := &WixShippingInfo{
		CarrierServiceOptions: []WixCarrierServiceGroup{
			{
				CarrierID: "carrier1",
				ShippingOptions: []WixShippingOption{
					{
						Code:  "standard",
						Title: "Standard Shipping",
						Cost:  &WixShippingCost{Price: &WixPrice{Amount: "5.99"}},
						Logistics: &WixLogistics{
							DeliveryTime: "3-5 business days",
						},
					},
					{
						Code:  "express",
						Title: "Express Shipping",
						Cost:  &WixShippingCost{Price: &WixPrice{Amount: "14.99"}},
					},
				},
			},
		},
		SelectedCarrierServiceOption: &WixSelectedShipping{
			Code: "standard",
		},
	}

	result, selectedID := extractShippingOptions(info)

	if len(result) != 2 {
		t.Fatalf("expected 2 options, got %d", len(result))
	}

	if result[0].ID != "standard" {
		t.Errorf("result[0].ID = %s, want standard", result[0].ID)
	}
	if result[0].Title != "Standard Shipping" {
		t.Errorf("result[0].Title = %s, want Standard Shipping", result[0].Title)
	}
	if result[0].SubTitle != "3-5 business days" {
		t.Errorf("result[0].SubTitle = %s, want 3-5 business days", result[0].SubTitle)
	}
	if result[0].Subtotal != 599 { // $5.99 in cents
		t.Errorf("result[0].Subtotal = %d, want 599", result[0].Subtotal)
	}

	if selectedID != "standard" {
		t.Errorf("selectedID = %s, want standard", selectedID)
	}
}

func TestNewAdapterValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name:    "empty client ID",
			cfg:     Config{TransformConfig: newTestTransformConfig()},
			wantErr: true,
		},
		{
			name:    "empty transform config",
			cfg:     Config{ClientID: "af926288-1715-4d94-82f8-a6a24c6604f1"},
			wantErr: true,
		},
		{
			name: "valid config",
			cfg: Config{
				ClientID:        "af926288-1715-4d94-82f8-a6a24c6604f1",
				TransformConfig: newTestTransformConfig(),
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.cfg)
			if (err != nil) != tc.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
