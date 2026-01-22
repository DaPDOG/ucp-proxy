package woocommerce

import (
	"encoding/json"

	"ucp-proxy/internal/adapter"
	"ucp-proxy/internal/model"
)

// =============================================================================
// BATCH BUILDER
// =============================================================================
//
// WooCommerce Store API supports batching multiple cart operations in a single
// HTTP request via POST /wc/store/v1/batch. This reduces network round-trips
// from O(n) to O(1) for checkout creation and updates.
//
// Batch operations execute sequentially in order. Each operation can succeed
// or fail independently, and the response contains results for all operations.
//
// Example batch request:
//
//	{
//	  "requests": [
//	    {"path": "/cart/add-item", "method": "POST", "body": {"id": 123, "quantity": 1}},
//	    {"path": "/cart/update-customer", "method": "POST", "body": {...}},
//	    {"path": "/cart/apply-coupon", "method": "POST", "body": {"code": "SAVE10"}}
//	  ]
//	}
//
// =============================================================================

// BatchBuilder constructs batch requests for WooCommerce Store API.
// Uses fluent API pattern for readability.
type BatchBuilder struct {
	operations []WooBatchOperation
}

// NewBatch creates a new batch builder.
func NewBatch() *BatchBuilder {
	return &BatchBuilder{
		operations: make([]WooBatchOperation, 0),
	}
}

// AddItem adds a product to the cart.
// path: /wc/store/v1/cart/add-item
func (b *BatchBuilder) AddItem(productID, quantity int) *BatchBuilder {
	body := map[string]int{
		"id":       productID,
		"quantity": quantity,
	}
	bodyJSON, _ := json.Marshal(body)

	b.operations = append(b.operations, WooBatchOperation{
		Path:   "/wc/store/v1/cart/add-item",
		Method: "POST",
		Body:   bodyJSON,
	})
	return b
}

// AddItems adds multiple products to the cart.
func (b *BatchBuilder) AddItems(items []model.LineItemRequest) *BatchBuilder {
	for _, item := range items {
		// ProductID is string in adapter but WooCommerce expects int
		// Conversion happens at batch execution time
		body := map[string]interface{}{
			"id":       item.ProductID,
			"quantity": item.Quantity,
		}
		bodyJSON, _ := json.Marshal(body)

		b.operations = append(b.operations, WooBatchOperation{
			Path:   "/wc/store/v1/cart/add-item",
			Method: "POST",
			Body:   bodyJSON,
		})
	}
	return b
}

// UpdateCustomer sets billing and/or shipping addresses.
// path: /wc/store/v1/cart/update-customer
func (b *BatchBuilder) UpdateCustomer(billing, shipping *model.PostalAddress) *BatchBuilder {
	return b.UpdateCustomerWithBuyer(billing, shipping, nil)
}

// UpdateCustomerWithBuyer sets addresses with buyer info for email/phone injection.
// WooCommerce requires email in billing_address, but UCP has email on Buyer.
// This method merges buyer.email/phone into the billing address.
func (b *BatchBuilder) UpdateCustomerWithBuyer(billing, shipping *model.PostalAddress, buyer *model.Buyer) *BatchBuilder {
	if billing == nil && shipping == nil && buyer == nil {
		return b
	}

	body := make(map[string]interface{})
	if billing != nil {
		wcBilling := AddressFromUCP(billing)
		// Inject buyer email/phone into billing address if available
		if buyer != nil {
			if wcBilling.Email == "" && buyer.Email != "" {
				wcBilling.Email = buyer.Email
			}
			if wcBilling.Phone == "" && buyer.PhoneNumber != "" {
				wcBilling.Phone = buyer.PhoneNumber
			}
		}
		body["billing_address"] = wcBilling
	} else if buyer != nil && buyer.Email != "" {
		// Even without billing address, we need to set email for WooCommerce
		body["billing_address"] = &WooAddress{Email: buyer.Email, Phone: buyer.PhoneNumber}
	}
	if shipping != nil {
		body["shipping_address"] = AddressFromUCP(shipping)
	}

	if len(body) == 0 {
		return b
	}

	bodyJSON, _ := json.Marshal(body)
	b.operations = append(b.operations, WooBatchOperation{
		Path:   "/wc/store/v1/cart/update-customer",
		Method: "POST",
		Body:   bodyJSON,
	})
	return b
}

// ApplyCoupon applies a discount code.
// path: /wc/store/v1/cart/apply-coupon
func (b *BatchBuilder) ApplyCoupon(code string) *BatchBuilder {
	if code == "" {
		return b
	}

	body := map[string]string{"code": code}
	bodyJSON, _ := json.Marshal(body)

	b.operations = append(b.operations, WooBatchOperation{
		Path:   "/wc/store/v1/cart/apply-coupon",
		Method: "POST",
		Body:   bodyJSON,
	})
	return b
}

// RemoveCoupon removes a discount code.
// path: /wc/store/v1/cart/remove-coupon
func (b *BatchBuilder) RemoveCoupon(code string) *BatchBuilder {
	if code == "" {
		return b
	}

	body := map[string]string{"code": code}
	bodyJSON, _ := json.Marshal(body)

	b.operations = append(b.operations, WooBatchOperation{
		Path:   "/wc/store/v1/cart/remove-coupon",
		Method: "POST",
		Body:   bodyJSON,
	})
	return b
}

// SelectShippingRate selects a shipping method for a package.
// path: /wc/store/v1/cart/select-shipping-rate
func (b *BatchBuilder) SelectShippingRate(rateID string, packageID int) *BatchBuilder {
	if rateID == "" {
		return b
	}

	body := map[string]interface{}{
		"rate_id":    rateID,
		"package_id": packageID,
	}
	bodyJSON, _ := json.Marshal(body)

	b.operations = append(b.operations, WooBatchOperation{
		Path:   "/wc/store/v1/cart/select-shipping-rate",
		Method: "POST",
		Body:   bodyJSON,
	})
	return b
}

// RemoveItem removes an item from the cart by its cart item key.
// The key is a hash assigned by WooCommerce (found in cart.items[].key).
// path: /wc/store/v1/cart/remove-item
func (b *BatchBuilder) RemoveItem(cartItemKey string) *BatchBuilder {
	if cartItemKey == "" {
		return b
	}

	body := map[string]string{"key": cartItemKey}
	bodyJSON, _ := json.Marshal(body)

	b.operations = append(b.operations, WooBatchOperation{
		Path:   "/wc/store/v1/cart/remove-item",
		Method: "POST",
		Body:   bodyJSON,
	})
	return b
}

// UpdateItemQuantity updates the quantity of an existing cart item.
// The key is a hash assigned by WooCommerce (found in cart.items[].key).
// path: /wc/store/v1/cart/update-item
func (b *BatchBuilder) UpdateItemQuantity(cartItemKey string, quantity int) *BatchBuilder {
	if cartItemKey == "" {
		return b
	}

	body := map[string]interface{}{
		"key":      cartItemKey,
		"quantity": quantity,
	}
	bodyJSON, _ := json.Marshal(body)

	b.operations = append(b.operations, WooBatchOperation{
		Path:   "/wc/store/v1/cart/update-item",
		Method: "POST",
		Body:   bodyJSON,
	})
	return b
}

// Build returns the batch request ready for execution.
// Returns nil if no operations were added.
func (b *BatchBuilder) Build() *WooBatchRequest {
	if len(b.operations) == 0 {
		return nil
	}
	return &WooBatchRequest{
		Requests: b.operations,
	}
}

// HasOperations returns true if any operations have been added.
func (b *BatchBuilder) HasOperations() bool {
	return len(b.operations) > 0
}

// OperationCount returns the number of operations in the batch.
func (b *BatchBuilder) OperationCount() int {
	return len(b.operations)
}

// =============================================================================
// CONVENIENCE BUILDERS
// =============================================================================

// BuildCreateCheckoutBatch creates a batch for checkout creation.
// Combines: add items + update customer (if addresses).
// Note: Coupons are applied via UpdateCheckout, not during creation.
func BuildCreateCheckoutBatch(req *adapter.CreateCheckoutRequest) *WooBatchRequest {
	b := NewBatch()

	// Add all line items
	b.AddItems(req.LineItems)

	// Update customer with addresses if provided
	b.UpdateCustomer(req.BillingAddress, req.ShippingAddress)

	return b.Build()
}

// BuildUpdateCheckoutBatch creates a batch for checkout updates.
// Combines: update customer (if addresses + buyer) + coupons + shipping rate.
// Buyer email is injected into billing_address for WooCommerce compatibility.
// NOTE: This builds a simple batch for direct updates. Full reconciliation
// is handled by UpdateCheckout in client.go.
func BuildUpdateCheckoutBatch(req *model.CheckoutUpdateRequest) *WooBatchRequest {
	b := NewBatch()

	// Update customer addresses (with buyer info for email injection)
	b.UpdateCustomerWithBuyer(req.BillingAddress, req.ShippingAddress, req.Buyer)

	// Apply coupons if provided
	for _, code := range req.DiscountCodes {
		b.ApplyCoupon(code)
	}

	// Select shipping rate if provided
	if req.FulfillmentOptionID != "" {
		b.SelectShippingRate(req.FulfillmentOptionID, 0) // Package 0 by default
	}

	return b.Build()
}

// =============================================================================
// BATCH HEADER INJECTION
// =============================================================================

// InjectHeaders adds the given headers to all operations in the batch.
// Used to add Cart-Token and Nonce to each sub-operation for authentication.
// WooCommerce batch endpoint requires per-operation headers for auth to work.
func (b *WooBatchRequest) InjectHeaders(headers map[string]string) {
	for i := range b.Requests {
		if b.Requests[i].Headers == nil {
			b.Requests[i].Headers = make(map[string]string)
		}
		for k, v := range headers {
			b.Requests[i].Headers[k] = v
		}
	}
}
