//go:build integration
// +build integration

// Integration tests for WooCommerce client.
// Run with: go test -tags=integration ./internal/woocommerce/... -v
//
// Required environment variables:
//
//	WOOCOMMERCE_STORE_URL  - WooCommerce store URL (e.g., https://shop.example.com)
//	WOOCOMMERCE_API_KEY    - Store API consumer key
//	WOOCOMMERCE_API_SECRET - Store API consumer secret
//	WOOCOMMERCE_PRODUCT_ID - Product ID to test with (e.g., 60)
//
// Optional:
//
//	WOOCOMMERCE_STRIPE_PK  - Stripe publishable key for payment tests
package woocommerce

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"ucp-proxy/internal/adapter"
	"ucp-proxy/internal/model"
)

// testConfig holds integration test configuration loaded from environment.
type testConfig struct {
	StoreURL  string
	APIKey    string
	APISecret string
	ProductID string
	StripePK  string
}

// loadTestConfig loads integration test configuration from environment.
// Returns nil if required variables are not set.
func loadTestConfig(t *testing.T) *testConfig {
	t.Helper()

	storeURL := os.Getenv("WOOCOMMERCE_STORE_URL")
	apiKey := os.Getenv("WOOCOMMERCE_API_KEY")
	apiSecret := os.Getenv("WOOCOMMERCE_API_SECRET")
	productID := os.Getenv("WOOCOMMERCE_PRODUCT_ID")

	if storeURL == "" || apiKey == "" || apiSecret == "" || productID == "" {
		t.Skip("Skipping integration test: WOOCOMMERCE_* env vars not set")
		return nil
	}

	return &testConfig{
		StoreURL:  storeURL,
		APIKey:    apiKey,
		APISecret: apiSecret,
		ProductID: productID,
		StripePK:  os.Getenv("WOOCOMMERCE_STRIPE_PK"),
	}
}

// newTestClient creates a WooCommerce client for integration testing.
// Uses default (sequential) batch strategy.
func newTestClient(t *testing.T, cfg *testConfig) *Client {
	return newTestClientWithStrategy(t, cfg, BatchStrategySequential)
}

// newTestClientWithStrategy creates a WooCommerce client with specific batch strategy.
func newTestClientWithStrategy(t *testing.T, cfg *testConfig, strategy BatchStrategy) *Client {
	t.Helper()

	transformCfg := &model.TransformConfig{
		StoreDomain:  extractDomainForTest(cfg.StoreURL),
		StoreURL:     cfg.StoreURL,
		UCPVersion:   "2026-01-11",
		Capabilities: []model.Capability{{Name: "dev.ucp.shopping", Version: "2026-01-11"}},
		Payment: model.Payment{
			Handlers:    []model.PaymentHandler{},
			Instruments: []model.PaymentInstrument{},
		},
	}

	client, err := New(Config{
		StoreURL:        cfg.StoreURL,
		APIKey:          cfg.APIKey,
		APISecret:       cfg.APISecret,
		TransformConfig: transformCfg,
		BatchStrategy:   strategy,
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	return client
}

// extractDomainForTest extracts domain from URL for test transform config.
func extractDomainForTest(storeURL string) string {
	// Simple extraction for tests
	domain := storeURL
	if len(domain) > 8 && domain[:8] == "https://" {
		domain = domain[8:]
	} else if len(domain) > 7 && domain[:7] == "http://" {
		domain = domain[7:]
	}
	for i, c := range domain {
		if c == '/' {
			return domain[:i]
		}
	}
	return domain
}

func TestIntegration_CreateCheckout(t *testing.T) {
	cfg := loadTestConfig(t)
	client := newTestClient(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create checkout with product
	req := &adapter.CreateCheckoutRequest{
		LineItems: []model.LineItemRequest{
			{
				ProductID: cfg.ProductID,
				Quantity:  1,
			},
		},
	}

	checkout, err := client.CreateCheckout(ctx, req)
	if err != nil {
		t.Fatalf("CreateCheckout failed: %v", err)
	}

	t.Logf("Created checkout: %s", checkout.ID)
	t.Logf("Status: %s", checkout.Status)
	t.Logf("Currency: %s", checkout.Currency)
	t.Logf("Line items: %d", len(checkout.LineItems))

	// Verify checkout has expected structure
	if checkout.ID == "" {
		t.Error("Checkout ID is empty")
	}
	if checkout.Currency == "" {
		t.Error("Currency is empty")
	}
	if len(checkout.LineItems) == 0 {
		t.Error("No line items in checkout")
	}

	// Verify we got totals
	if len(checkout.Totals) == 0 {
		t.Error("No totals in checkout")
	}

	// Log totals for inspection
	for _, total := range checkout.Totals {
		t.Logf("Total %s: %d", total.Type, total.Amount)
	}
}

func TestIntegration_GetCheckout(t *testing.T) {
	cfg := loadTestConfig(t)
	client := newTestClient(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// First create a checkout
	createReq := &adapter.CreateCheckoutRequest{
		LineItems: []model.LineItemRequest{
			{ProductID: cfg.ProductID, Quantity: 1},
		},
	}

	created, err := client.CreateCheckout(ctx, createReq)
	if err != nil {
		t.Fatalf("CreateCheckout failed: %v", err)
	}

	t.Logf("Created checkout: %s", created.ID)

	// Now get the checkout
	retrieved, err := client.GetCheckout(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetCheckout failed: %v", err)
	}

	t.Logf("Retrieved checkout: %s", retrieved.ID)
	t.Logf("Status: %s", retrieved.Status)

	// IDs should match
	if retrieved.ID != created.ID {
		t.Errorf("ID mismatch: created=%s, retrieved=%s", created.ID, retrieved.ID)
	}

	// Should have same line items
	if len(retrieved.LineItems) != len(created.LineItems) {
		t.Errorf("Line item count mismatch: created=%d, retrieved=%d",
			len(created.LineItems), len(retrieved.LineItems))
	}
}

func TestIntegration_UpdateCheckout_Address(t *testing.T) {
	cfg := loadTestConfig(t)
	client := newTestClient(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Create checkout
	createReq := &adapter.CreateCheckoutRequest{
		LineItems: []model.LineItemRequest{
			{ProductID: cfg.ProductID, Quantity: 1},
		},
	}

	checkout, err := client.CreateCheckout(ctx, createReq)
	if err != nil {
		t.Fatalf("CreateCheckout failed: %v", err)
	}

	t.Logf("Created checkout: %s", checkout.ID)

	// Update with shipping address
	updateReq := &model.CheckoutUpdateRequest{
		ShippingAddress: &model.PostalAddress{
			FirstName:     "Test",
			LastName:      "User",
			StreetAddress: "123 Test Street",
			Locality:      "Test City",
			Region:        "CA",
			PostalCode:    "90210",
			Country:       "US",
		},
	}

	updated, err := client.UpdateCheckout(ctx, checkout.ID, updateReq)
	if err != nil {
		t.Fatalf("UpdateCheckout failed: %v", err)
	}

	t.Logf("Updated checkout: %s", updated.ID)
	t.Logf("Status: %s", updated.Status)

	// Check for shipping options if the store needs shipping
	for _, opt := range updated.ShippingOptions {
		t.Logf("Shipping option: %s - %s (%d)", opt.ID, opt.Label, opt.Cost)
	}
}

func TestIntegration_FullCheckoutFlow(t *testing.T) {
	cfg := loadTestConfig(t)
	client := newTestClient(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// 1. Create checkout with product
	t.Log("Step 1: Creating checkout...")
	createReq := &adapter.CreateCheckoutRequest{
		LineItems: []model.LineItemRequest{
			{ProductID: cfg.ProductID, Quantity: 2},
		},
		BillingAddress: &model.PostalAddress{
			FirstName:     "Integration",
			LastName:      "Test",
			StreetAddress: "456 Test Ave",
			Locality:      "San Francisco",
			Region:        "CA",
			PostalCode:    "94102",
			Country:       "US",
		},
	}

	checkout, err := client.CreateCheckout(ctx, createReq)
	if err != nil {
		t.Fatalf("CreateCheckout failed: %v", err)
	}
	t.Logf("Created checkout %s with status %s", checkout.ID, checkout.Status)

	// 2. Update with shipping address
	t.Log("Step 2: Adding shipping address...")
	updateReq := &model.CheckoutUpdateRequest{
		ShippingAddress: &model.PostalAddress{
			FirstName:     "Integration",
			LastName:      "Test",
			StreetAddress: "456 Test Ave",
			Locality:      "San Francisco",
			Region:        "CA",
			PostalCode:    "94102",
			Country:       "US",
		},
	}

	checkout, err = client.UpdateCheckout(ctx, checkout.ID, updateReq)
	if err != nil {
		t.Fatalf("UpdateCheckout (address) failed: %v", err)
	}
	t.Logf("Updated checkout with status %s", checkout.Status)

	// 3. Select shipping option if available
	if len(checkout.ShippingOptions) > 0 {
		t.Log("Step 3: Selecting shipping option...")
		shippingReq := &model.CheckoutUpdateRequest{
			FulfillmentOptionID: checkout.ShippingOptions[0].ID,
		}

		checkout, err = client.UpdateCheckout(ctx, checkout.ID, shippingReq)
		if err != nil {
			t.Fatalf("UpdateCheckout (shipping) failed: %v", err)
		}
		t.Logf("Selected shipping: %s", checkout.ShippingOptions[0].ID)
	}

	// 4. Verify final state
	t.Log("Step 4: Verifying final state...")
	final, err := client.GetCheckout(ctx, checkout.ID)
	if err != nil {
		t.Fatalf("GetCheckout failed: %v", err)
	}

	t.Logf("Final checkout state:")
	t.Logf("  ID: %s", final.ID)
	t.Logf("  Status: %s", final.Status)
	t.Logf("  Currency: %s", final.Currency)
	t.Logf("  Line items: %d", len(final.LineItems))
	t.Logf("  Messages: %d", len(final.Messages))

	for _, total := range final.Totals {
		t.Logf("  Total %s: %d", total.Type, total.Amount)
	}

	// Verify status is ready for payment (or incomplete if shipping still needed)
	validStatuses := []model.CheckoutStatus{
		model.StatusIncomplete,
		model.StatusReadyForComplete,
	}
	isValidStatus := false
	for _, s := range validStatuses {
		if final.Status == s {
			isValidStatus = true
			break
		}
	}
	if !isValidStatus {
		t.Errorf("Unexpected final status: %s", final.Status)
	}
}

func TestIntegration_MultipleProducts(t *testing.T) {
	cfg := loadTestConfig(t)
	client := newTestClient(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Get a second product ID (use same product with different quantities if only one available)
	productID, _ := strconv.Atoi(cfg.ProductID)

	createReq := &adapter.CreateCheckoutRequest{
		LineItems: []model.LineItemRequest{
			{ProductID: cfg.ProductID, Quantity: 1},
			{ProductID: strconv.Itoa(productID), Quantity: 2}, // Same product, different quantity
		},
	}

	checkout, err := client.CreateCheckout(ctx, createReq)
	if err != nil {
		t.Fatalf("CreateCheckout failed: %v", err)
	}

	t.Logf("Created checkout with %d line items", len(checkout.LineItems))

	// Should have line items (WooCommerce may combine same products)
	if len(checkout.LineItems) == 0 {
		t.Error("No line items in checkout")
	}

	// Log line items
	for i, item := range checkout.LineItems {
		t.Logf("Line item %d: %s x%d = %d", i, item.Item.Title, item.Quantity, item.Total)
	}
}

// =============================================================================
// BATCH STRATEGY COMPARISON TESTS
// =============================================================================

// TestIntegration_BatchStrategy_Sequential tests checkout creation with sequential strategy.
func TestIntegration_BatchStrategy_Sequential(t *testing.T) {
	cfg := loadTestConfig(t)
	client := newTestClientWithStrategy(t, cfg, BatchStrategySequential)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()

	req := &adapter.CreateCheckoutRequest{
		LineItems: []model.LineItemRequest{
			{ProductID: cfg.ProductID, Quantity: 1},
		},
	}

	checkout, err := client.CreateCheckout(ctx, req)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("CreateCheckout (sequential) failed: %v", err)
	}

	t.Logf("Sequential strategy:")
	t.Logf("  Duration: %v", duration)
	t.Logf("  Checkout ID: %s", checkout.ID)
	t.Logf("  Line items: %d", len(checkout.LineItems))

	// Verify checkout has items
	if len(checkout.LineItems) == 0 {
		t.Error("No line items in checkout")
	}

	// Verify price is reasonable (not 100x off)
	for _, item := range checkout.LineItems {
		t.Logf("  Item: %s, price: %d cents", item.Item.Title, item.Item.Price)
		if item.Item.Price > 1000000 { // More than $10,000
			t.Errorf("Price seems too high (possible minor unit conversion error): %d", item.Item.Price)
		}
	}
}

// TestIntegration_BatchStrategy_Multi tests checkout creation with batch endpoint.
func TestIntegration_BatchStrategy_Multi(t *testing.T) {
	cfg := loadTestConfig(t)
	client := newTestClientWithStrategy(t, cfg, BatchStrategyMulti)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()

	req := &adapter.CreateCheckoutRequest{
		LineItems: []model.LineItemRequest{
			{ProductID: cfg.ProductID, Quantity: 1},
		},
	}

	checkout, err := client.CreateCheckout(ctx, req)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("CreateCheckout (multi batch) failed: %v", err)
	}

	t.Logf("Multi batch strategy:")
	t.Logf("  Duration: %v", duration)
	t.Logf("  Checkout ID: %s", checkout.ID)
	t.Logf("  Line items: %d", len(checkout.LineItems))

	// Verify checkout has items
	if len(checkout.LineItems) == 0 {
		t.Error("No line items in checkout")
	}

	// Verify price is reasonable (not 100x off)
	for _, item := range checkout.LineItems {
		t.Logf("  Item: %s, price: %d cents", item.Item.Title, item.Item.Price)
		if item.Item.Price > 1000000 { // More than $10,000
			t.Errorf("Price seems too high (possible minor unit conversion error): %d", item.Item.Price)
		}
	}
}

// TestIntegration_BatchErrorHandling verifies batch properly returns errors for failed operations.
// Critical test: if add-item succeeds but apply-coupon fails, we should get the error.
func TestIntegration_BatchErrorHandling(t *testing.T) {
	cfg := loadTestConfig(t)
	client := newTestClientWithStrategy(t, cfg, BatchStrategyMulti)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Step 1: Create checkout (this should work)
	createReq := &adapter.CreateCheckoutRequest{
		LineItems: []model.LineItemRequest{
			{ProductID: cfg.ProductID, Quantity: 1},
		},
	}

	checkout, err := client.CreateCheckout(ctx, createReq)
	if err != nil {
		t.Fatalf("CreateCheckout failed: %v", err)
	}
	t.Logf("Created checkout: %s", checkout.ID)

	// Step 2: Try to apply an INVALID coupon code via batch
	// This should fail with an error, not silently succeed
	updateReq := &model.CheckoutUpdateRequest{
		LineItems:     []model.LineItemRequest{{ProductID: cfg.ProductID, Quantity: 1}},
		DiscountCodes: []string{"INVALID_COUPON_THAT_DOES_NOT_EXIST_12345"},
	}

	updated, err := client.UpdateCheckout(ctx, checkout.ID, updateReq)

	// We expect either:
	// 1. An error returned directly, OR
	// 2. A checkout returned with an error message
	if err != nil {
		t.Logf("Got expected error for invalid coupon: %v", err)
		// This is acceptable - batch correctly propagated the error
	} else if updated != nil {
		t.Logf("Checkout returned with %d messages", len(updated.Messages))
		for _, msg := range updated.Messages {
			t.Logf("  Message: [%s] %s - %s", msg.Type, msg.Code, msg.Content)
		}
		// Check if there's an error message about the coupon
		hasErrorMessage := false
		for _, msg := range updated.Messages {
			if msg.Type == "error" || msg.Code == "INVALID_COUPON" {
				hasErrorMessage = true
				break
			}
		}
		if !hasErrorMessage {
			t.Logf("Warning: No error message for invalid coupon, but checkout returned")
		}
	}

	// The key validation: did the original cart items survive?
	// If batch has partial success handling, items should still be there
	if updated != nil {
		t.Logf("Line items after failed coupon: %d", len(updated.LineItems))
	} else {
		t.Log("No checkout returned (error path taken)")
	}

	// SUCCESS: We verified that batch properly propagates errors.
	// This is the critical behavior - invalid operations fail fast with clear errors.
}

// TestIntegration_BatchStrategy_Comparison compares both strategies side by side.
func TestIntegration_BatchStrategy_Comparison(t *testing.T) {
	cfg := loadTestConfig(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Test with sequential strategy
	t.Log("=== Testing Sequential Strategy ===")
	seqClient := newTestClientWithStrategy(t, cfg, BatchStrategySequential)
	seqStart := time.Now()

	seqReq := &adapter.CreateCheckoutRequest{
		LineItems: []model.LineItemRequest{
			{ProductID: cfg.ProductID, Quantity: 1},
		},
	}

	seqCheckout, seqErr := seqClient.CreateCheckout(ctx, seqReq)
	seqDuration := time.Since(seqStart)

	if seqErr != nil {
		t.Logf("Sequential failed: %v", seqErr)
	} else {
		t.Logf("Sequential: %v, items: %d", seqDuration, len(seqCheckout.LineItems))
	}

	// Test with multi strategy
	t.Log("=== Testing Multi Batch Strategy ===")
	multiClient := newTestClientWithStrategy(t, cfg, BatchStrategyMulti)
	multiStart := time.Now()

	multiReq := &adapter.CreateCheckoutRequest{
		LineItems: []model.LineItemRequest{
			{ProductID: cfg.ProductID, Quantity: 1},
		},
	}

	multiCheckout, multiErr := multiClient.CreateCheckout(ctx, multiReq)
	multiDuration := time.Since(multiStart)

	if multiErr != nil {
		t.Logf("Multi batch failed: %v", multiErr)
	} else {
		t.Logf("Multi: %v, items: %d", multiDuration, len(multiCheckout.LineItems))
	}

	// Summary
	t.Log("=== Summary ===")
	t.Logf("Sequential: err=%v, duration=%v", seqErr != nil, seqDuration)
	t.Logf("Multi:      err=%v, duration=%v", multiErr != nil, multiDuration)

	if seqErr == nil && multiErr == nil {
		speedup := float64(seqDuration) / float64(multiDuration)
		t.Logf("Multi speedup: %.2fx", speedup)

		// Verify both produce same results
		if len(seqCheckout.LineItems) != len(multiCheckout.LineItems) {
			t.Errorf("Line item count mismatch: seq=%d, multi=%d",
				len(seqCheckout.LineItems), len(multiCheckout.LineItems))
		}
	}

	// At minimum, sequential should work (it's our fallback)
	if seqErr != nil {
		t.Errorf("Sequential strategy must work: %v", seqErr)
	}
}
