// Package reconcile provides diff and reconciliation logic for checkout state.
// Used by adapters to compute the delta between current and desired checkout state,
// enabling stateless PUT semantics where the proxy fetches current state, diffs,
// and executes only the necessary mutations.
package reconcile

// LineItemDiff describes the mutations needed to reconcile line items.
// Operations should be applied in order: Remove → Update → Add
// to prevent conflicts (e.g., updating a removed item).
type LineItemDiff struct {
	ToAdd    []ItemToAdd    // Products in desired but not current
	ToRemove []ItemToRemove // Products in current but not desired
	ToUpdate []ItemToUpdate // Products in both with different quantities
}

// ItemToAdd specifies a new item to add to checkout.
type ItemToAdd struct {
	ProductID string // Canonical product identifier
	VariantID string // Optional variant ID
	Quantity  int    // Desired quantity
}

// ItemToRemove specifies an item to remove from checkout.
type ItemToRemove struct {
	ProductID string // Canonical product identifier (for reference)
	BackendID string // Platform-specific ID needed for removal API call
}

// ItemToUpdate specifies a quantity change for an existing item.
type ItemToUpdate struct {
	ProductID   string // Canonical product identifier (for reference)
	BackendID   string // Platform-specific ID needed for update API call
	OldQuantity int    // Current quantity (informational)
	NewQuantity int    // Desired quantity
}

// IsEmpty returns true if no line item changes are needed.
func (d *LineItemDiff) IsEmpty() bool {
	return len(d.ToAdd) == 0 && len(d.ToRemove) == 0 && len(d.ToUpdate) == 0
}

// CurrentItem represents an item in the current checkout state.
// Adapters convert their platform-specific types to this before diffing.
type CurrentItem struct {
	ProductID string // Canonical product identifier (e.g., WooCommerce product ID, Wix catalogItemId)
	BackendID string // Platform-specific line item ID (cart_item_key for Woo, lineItemId for Wix)
	VariantID string // Optional variant identifier
	Quantity  int    // Current quantity
}

// DesiredItem represents an item in the desired checkout state.
// Comes from the client's PUT request.
type DesiredItem struct {
	ProductID string // Canonical product identifier
	VariantID string // Optional variant identifier
	Quantity  int    // Desired quantity
}

// DiffLineItems computes the delta between current and desired line items.
// Matching is by ProductID (canonical), not backend-specific IDs.
//
// Algorithm:
//  1. Build lookup maps for O(1) access
//  2. For each desired item: if exists in current with different qty → update; if not exists → add
//  3. For each current item: if not in desired → remove
func DiffLineItems(current []CurrentItem, desired []DesiredItem) *LineItemDiff {
	diff := &LineItemDiff{}

	// Build maps keyed by ProductID (+ VariantID if present for uniqueness)
	currentByKey := make(map[string]CurrentItem)
	for _, item := range current {
		key := itemKey(item.ProductID, item.VariantID)
		currentByKey[key] = item
	}

	desiredByKey := make(map[string]DesiredItem)
	for _, item := range desired {
		key := itemKey(item.ProductID, item.VariantID)
		desiredByKey[key] = item
	}

	// Find items to add or update
	for key, desired := range desiredByKey {
		if current, exists := currentByKey[key]; exists {
			// Item exists - check if quantity changed
			if current.Quantity != desired.Quantity {
				diff.ToUpdate = append(diff.ToUpdate, ItemToUpdate{
					ProductID:   desired.ProductID,
					BackendID:   current.BackendID, // Use current's backend ID for API call
					OldQuantity: current.Quantity,
					NewQuantity: desired.Quantity,
				})
			}
			// Same quantity = no change needed
		} else {
			// Item doesn't exist in current - add it
			diff.ToAdd = append(diff.ToAdd, ItemToAdd{
				ProductID: desired.ProductID,
				VariantID: desired.VariantID,
				Quantity:  desired.Quantity,
			})
		}
	}

	// Find items to remove (in current but not in desired)
	for key, current := range currentByKey {
		if _, exists := desiredByKey[key]; !exists {
			diff.ToRemove = append(diff.ToRemove, ItemToRemove{
				ProductID: current.ProductID,
				BackendID: current.BackendID,
			})
		}
	}

	return diff
}

// itemKey creates a composite key for matching items.
// Uses ProductID alone if no variant, or ProductID:VariantID if variant present.
func itemKey(productID, variantID string) string {
	if variantID == "" {
		return productID
	}
	return productID + ":" + variantID
}

// DiscountDiff describes the mutations needed to reconcile discount codes.
type DiscountDiff struct {
	ToApply  []string // Codes in desired but not current
	ToRemove []string // Codes in current but not desired
}

// IsEmpty returns true if no discount changes are needed.
func (d *DiscountDiff) IsEmpty() bool {
	return len(d.ToApply) == 0 && len(d.ToRemove) == 0
}

// DiffDiscounts computes the delta between current and desired discount codes.
// Simple set difference: apply codes not currently applied, remove codes not desired.
func DiffDiscounts(currentCodes, desiredCodes []string) *DiscountDiff {
	diff := &DiscountDiff{}

	currentSet := make(map[string]bool)
	for _, code := range currentCodes {
		currentSet[code] = true
	}

	desiredSet := make(map[string]bool)
	for _, code := range desiredCodes {
		desiredSet[code] = true
	}

	// Codes to apply (in desired but not current)
	for code := range desiredSet {
		if !currentSet[code] {
			diff.ToApply = append(diff.ToApply, code)
		}
	}

	// Codes to remove (in current but not desired)
	for code := range currentSet {
		if !desiredSet[code] {
			diff.ToRemove = append(diff.ToRemove, code)
		}
	}

	return diff
}

// FulfillmentChanged returns true if fulfillment selection differs.
// Simple comparison - no complex diffing needed.
func FulfillmentChanged(currentID, desiredID string) bool {
	return desiredID != "" && currentID != desiredID
}
