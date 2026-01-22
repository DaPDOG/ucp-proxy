package woocommerce

import (
	"encoding/json"
	"testing"

	"ucp-proxy/internal/adapter"
	"ucp-proxy/internal/model"
)

func TestBatchBuilder_AddItem(t *testing.T) {
	b := NewBatch().AddItem(123, 2)

	if !b.HasOperations() {
		t.Fatal("expected operations")
	}
	if b.OperationCount() != 1 {
		t.Errorf("count = %d, want 1", b.OperationCount())
	}

	req := b.Build()
	if len(req.Requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(req.Requests))
	}

	op := req.Requests[0]
	if op.Path != "/wc/store/v1/cart/add-item" {
		t.Errorf("path = %s, want /wc/store/v1/cart/add-item", op.Path)
	}
	if op.Method != "POST" {
		t.Errorf("method = %s, want POST", op.Method)
	}

	var body map[string]int
	if err := json.Unmarshal(op.Body, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body["id"] != 123 {
		t.Errorf("id = %d, want 123", body["id"])
	}
	if body["quantity"] != 2 {
		t.Errorf("quantity = %d, want 2", body["quantity"])
	}
}

func TestBatchBuilder_AddItems(t *testing.T) {
	items := []model.LineItemRequest{
		{ProductID: "100", Quantity: 1},
		{ProductID: "200", Quantity: 3},
	}

	b := NewBatch().AddItems(items)
	req := b.Build()

	if len(req.Requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(req.Requests))
	}

	// Verify first item
	var body1 map[string]interface{}
	json.Unmarshal(req.Requests[0].Body, &body1)
	if body1["id"] != "100" {
		t.Errorf("first id = %v, want 100", body1["id"])
	}

	// Verify second item
	var body2 map[string]interface{}
	json.Unmarshal(req.Requests[1].Body, &body2)
	if body2["id"] != "200" {
		t.Errorf("second id = %v, want 200", body2["id"])
	}
}

func TestBatchBuilder_UpdateCustomer(t *testing.T) {
	billing := &model.PostalAddress{
		FirstName:     "John",
		LastName:      "Doe",
		StreetAddress: "123 Main St",
		Locality:      "NYC",
		Region:        "NY",
		PostalCode:    "10001",
		Country:       "US",
	}

	b := NewBatch().UpdateCustomer(billing, nil)
	req := b.Build()

	if len(req.Requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(req.Requests))
	}

	op := req.Requests[0]
	if op.Path != "/wc/store/v1/cart/update-customer" {
		t.Errorf("path = %s", op.Path)
	}

	var body map[string]interface{}
	json.Unmarshal(op.Body, &body)
	if _, ok := body["billing_address"]; !ok {
		t.Error("expected billing_address in body")
	}
	if _, ok := body["shipping_address"]; ok {
		t.Error("unexpected shipping_address in body")
	}
}

func TestBatchBuilder_UpdateCustomer_BothAddresses(t *testing.T) {
	billing := &model.PostalAddress{FirstName: "John"}
	shipping := &model.PostalAddress{FirstName: "Jane"}

	b := NewBatch().UpdateCustomer(billing, shipping)
	req := b.Build()

	var body map[string]interface{}
	json.Unmarshal(req.Requests[0].Body, &body)

	if _, ok := body["billing_address"]; !ok {
		t.Error("expected billing_address")
	}
	if _, ok := body["shipping_address"]; !ok {
		t.Error("expected shipping_address")
	}
}

func TestBatchBuilder_UpdateCustomer_NilAddresses(t *testing.T) {
	b := NewBatch().UpdateCustomer(nil, nil)

	if b.HasOperations() {
		t.Error("expected no operations for nil addresses")
	}
	if b.Build() != nil {
		t.Error("expected nil build result")
	}
}

func TestBatchBuilder_ApplyCoupon(t *testing.T) {
	b := NewBatch().ApplyCoupon("SAVE10")
	req := b.Build()

	if len(req.Requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(req.Requests))
	}

	op := req.Requests[0]
	if op.Path != "/wc/store/v1/cart/apply-coupon" {
		t.Errorf("path = %s", op.Path)
	}

	var body map[string]string
	json.Unmarshal(op.Body, &body)
	if body["code"] != "SAVE10" {
		t.Errorf("code = %s, want SAVE10", body["code"])
	}
}

func TestBatchBuilder_ApplyCoupon_Empty(t *testing.T) {
	b := NewBatch().ApplyCoupon("")

	if b.HasOperations() {
		t.Error("expected no operations for empty coupon")
	}
}

func TestBatchBuilder_SelectShippingRate(t *testing.T) {
	b := NewBatch().SelectShippingRate("flat_rate:1", 0)
	req := b.Build()

	if len(req.Requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(req.Requests))
	}

	op := req.Requests[0]
	if op.Path != "/wc/store/v1/cart/select-shipping-rate" {
		t.Errorf("path = %s", op.Path)
	}

	var body map[string]interface{}
	json.Unmarshal(op.Body, &body)
	if body["rate_id"] != "flat_rate:1" {
		t.Errorf("rate_id = %v", body["rate_id"])
	}
	if body["package_id"] != float64(0) { // JSON numbers are float64
		t.Errorf("package_id = %v", body["package_id"])
	}
}

func TestBatchBuilder_SelectShippingRate_Empty(t *testing.T) {
	b := NewBatch().SelectShippingRate("", 0)

	if b.HasOperations() {
		t.Error("expected no operations for empty rate")
	}
}

func TestBatchBuilder_FluentChaining(t *testing.T) {
	b := NewBatch().
		AddItem(100, 1).
		AddItem(200, 2).
		UpdateCustomer(&model.PostalAddress{FirstName: "John"}, nil).
		ApplyCoupon("DISCOUNT").
		SelectShippingRate("free_shipping", 0)

	if b.OperationCount() != 5 {
		t.Errorf("count = %d, want 5", b.OperationCount())
	}

	req := b.Build()
	if len(req.Requests) != 5 {
		t.Errorf("requests = %d, want 5", len(req.Requests))
	}

	// Verify operation order
	paths := []string{
		"/wc/store/v1/cart/add-item",
		"/wc/store/v1/cart/add-item",
		"/wc/store/v1/cart/update-customer",
		"/wc/store/v1/cart/apply-coupon",
		"/wc/store/v1/cart/select-shipping-rate",
	}
	for i, want := range paths {
		if req.Requests[i].Path != want {
			t.Errorf("request[%d].path = %s, want %s", i, req.Requests[i].Path, want)
		}
	}
}

func TestBuildCreateCheckoutBatch(t *testing.T) {
	req := &adapter.CreateCheckoutRequest{
		LineItems: []model.LineItemRequest{
			{ProductID: "60", Quantity: 1},
			{ProductID: "70", Quantity: 2},
		},
		BillingAddress: &model.PostalAddress{
			FirstName:     "Test",
			LastName:      "User",
			StreetAddress: "123 Test St",
			Locality:      "Test City",
			Region:        "TS",
			PostalCode:    "12345",
			Country:       "US",
		},
	}

	batch := BuildCreateCheckoutBatch(req)

	if batch == nil {
		t.Fatal("expected batch, got nil")
	}

	// Should have: 2 add-item + 1 update-customer = 3
	if len(batch.Requests) != 3 {
		t.Errorf("requests = %d, want 3", len(batch.Requests))
	}

	// Verify operation types in order
	expectedPaths := []string{
		"/wc/store/v1/cart/add-item",
		"/wc/store/v1/cart/add-item",
		"/wc/store/v1/cart/update-customer",
	}
	for i, want := range expectedPaths {
		if batch.Requests[i].Path != want {
			t.Errorf("request[%d].path = %s, want %s", i, batch.Requests[i].Path, want)
		}
	}
}

func TestBuildCreateCheckoutBatch_MinimalRequest(t *testing.T) {
	req := &adapter.CreateCheckoutRequest{
		LineItems: []model.LineItemRequest{
			{ProductID: "60", Quantity: 1},
		},
	}

	batch := BuildCreateCheckoutBatch(req)

	// Only add-item, no customer update or coupon
	if len(batch.Requests) != 1 {
		t.Errorf("requests = %d, want 1", len(batch.Requests))
	}
}

func TestBuildCreateCheckoutBatch_NoItems(t *testing.T) {
	req := &adapter.CreateCheckoutRequest{}

	batch := BuildCreateCheckoutBatch(req)

	// No line items = nil batch
	if batch != nil {
		t.Error("expected nil batch for empty request")
	}
}

func TestBuildUpdateCheckoutBatch(t *testing.T) {
	req := &model.CheckoutUpdateRequest{
		LineItems:     []model.LineItemRequest{{ProductID: "123", Quantity: 1}},
		DiscountCodes: []string{"UPDATE10"},
		ShippingAddress: &model.PostalAddress{
			FirstName:     "Ship",
			LastName:      "Test",
			StreetAddress: "456 Ship St",
			Locality:      "Ship City",
			Region:        "SC",
			PostalCode:    "54321",
			Country:       "US",
		},
		FulfillmentOptionID: "flat_rate:1",
	}

	batch := BuildUpdateCheckoutBatch(req)

	if batch == nil {
		t.Fatal("expected batch, got nil")
	}

	// Should have: 1 update-customer + 1 apply-coupon + 1 select-shipping = 3
	if len(batch.Requests) != 3 {
		t.Errorf("requests = %d, want 3", len(batch.Requests))
	}

	// Verify operation types
	expectedPaths := []string{
		"/wc/store/v1/cart/update-customer",
		"/wc/store/v1/cart/apply-coupon",
		"/wc/store/v1/cart/select-shipping-rate",
	}
	for i, want := range expectedPaths {
		if batch.Requests[i].Path != want {
			t.Errorf("request[%d].path = %s, want %s", i, batch.Requests[i].Path, want)
		}
	}
}

func TestBuildUpdateCheckoutBatch_OnlyShipping(t *testing.T) {
	req := &model.CheckoutUpdateRequest{
		LineItems:           []model.LineItemRequest{{ProductID: "123", Quantity: 1}},
		DiscountCodes:       []string{}, // No discounts
		FulfillmentOptionID: "local_pickup:1",
	}

	batch := BuildUpdateCheckoutBatch(req)

	if len(batch.Requests) != 1 {
		t.Errorf("requests = %d, want 1", len(batch.Requests))
	}
	if batch.Requests[0].Path != "/wc/store/v1/cart/select-shipping-rate" {
		t.Errorf("path = %s", batch.Requests[0].Path)
	}
}

func TestBuildUpdateCheckoutBatch_Empty(t *testing.T) {
	// BuildUpdateCheckoutBatch is a convenience function for building simple batches.
	// It doesn't validate required fields - that's done by the adapter.
	req := &model.CheckoutUpdateRequest{
		LineItems:     []model.LineItemRequest{{ProductID: "123", Quantity: 1}},
		DiscountCodes: []string{},
	}

	batch := BuildUpdateCheckoutBatch(req)

	if batch != nil {
		t.Error("expected nil batch for update request with no operations")
	}
}

func TestRemoveCoupon(t *testing.T) {
	b := NewBatch().RemoveCoupon("OLD_COUPON")
	req := b.Build()

	if len(req.Requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(req.Requests))
	}

	op := req.Requests[0]
	if op.Path != "/wc/store/v1/cart/remove-coupon" {
		t.Errorf("path = %s", op.Path)
	}

	var body map[string]string
	json.Unmarshal(op.Body, &body)
	if body["code"] != "OLD_COUPON" {
		t.Errorf("code = %s, want OLD_COUPON", body["code"])
	}
}
