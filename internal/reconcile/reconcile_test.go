package reconcile

import (
	"testing"
)

func TestDiffLineItems_EmptyToItems(t *testing.T) {
	// Empty current, items in desired → all adds
	current := []CurrentItem{}
	desired := []DesiredItem{
		{ProductID: "prod-1", Quantity: 2},
		{ProductID: "prod-2", Quantity: 1},
	}

	diff := DiffLineItems(current, desired)

	if len(diff.ToAdd) != 2 {
		t.Errorf("ToAdd = %d, want 2", len(diff.ToAdd))
	}
	if len(diff.ToRemove) != 0 {
		t.Errorf("ToRemove = %d, want 0", len(diff.ToRemove))
	}
	if len(diff.ToUpdate) != 0 {
		t.Errorf("ToUpdate = %d, want 0", len(diff.ToUpdate))
	}
}

func TestDiffLineItems_ItemsToEmpty(t *testing.T) {
	// Items in current, empty desired → all removes
	current := []CurrentItem{
		{ProductID: "prod-1", BackendID: "key-1", Quantity: 2},
		{ProductID: "prod-2", BackendID: "key-2", Quantity: 1},
	}
	desired := []DesiredItem{}

	diff := DiffLineItems(current, desired)

	if len(diff.ToAdd) != 0 {
		t.Errorf("ToAdd = %d, want 0", len(diff.ToAdd))
	}
	if len(diff.ToRemove) != 2 {
		t.Errorf("ToRemove = %d, want 2", len(diff.ToRemove))
	}
	if len(diff.ToUpdate) != 0 {
		t.Errorf("ToUpdate = %d, want 0", len(diff.ToUpdate))
	}

	// Verify backend IDs are preserved for removal
	for _, item := range diff.ToRemove {
		if item.BackendID == "" {
			t.Error("ToRemove item missing BackendID")
		}
	}
}

func TestDiffLineItems_QuantityUpdate(t *testing.T) {
	// Same items, different quantities → updates
	current := []CurrentItem{
		{ProductID: "prod-1", BackendID: "key-1", Quantity: 2},
	}
	desired := []DesiredItem{
		{ProductID: "prod-1", Quantity: 5},
	}

	diff := DiffLineItems(current, desired)

	if len(diff.ToAdd) != 0 {
		t.Errorf("ToAdd = %d, want 0", len(diff.ToAdd))
	}
	if len(diff.ToRemove) != 0 {
		t.Errorf("ToRemove = %d, want 0", len(diff.ToRemove))
	}
	if len(diff.ToUpdate) != 1 {
		t.Errorf("ToUpdate = %d, want 1", len(diff.ToUpdate))
	}

	if diff.ToUpdate[0].OldQuantity != 2 {
		t.Errorf("OldQuantity = %d, want 2", diff.ToUpdate[0].OldQuantity)
	}
	if diff.ToUpdate[0].NewQuantity != 5 {
		t.Errorf("NewQuantity = %d, want 5", diff.ToUpdate[0].NewQuantity)
	}
	if diff.ToUpdate[0].BackendID != "key-1" {
		t.Errorf("BackendID = %s, want key-1", diff.ToUpdate[0].BackendID)
	}
}

func TestDiffLineItems_NoChange(t *testing.T) {
	// Same items, same quantities → no changes
	current := []CurrentItem{
		{ProductID: "prod-1", BackendID: "key-1", Quantity: 2},
	}
	desired := []DesiredItem{
		{ProductID: "prod-1", Quantity: 2},
	}

	diff := DiffLineItems(current, desired)

	if !diff.IsEmpty() {
		t.Error("Expected empty diff for identical items")
	}
}

func TestDiffLineItems_MixedOperations(t *testing.T) {
	// Mix of add, remove, and update
	current := []CurrentItem{
		{ProductID: "prod-1", BackendID: "key-1", Quantity: 2}, // will be removed
		{ProductID: "prod-2", BackendID: "key-2", Quantity: 1}, // will be updated
		{ProductID: "prod-3", BackendID: "key-3", Quantity: 3}, // unchanged
	}
	desired := []DesiredItem{
		{ProductID: "prod-2", Quantity: 5}, // update from 1 to 5
		{ProductID: "prod-3", Quantity: 3}, // no change
		{ProductID: "prod-4", Quantity: 1}, // add
	}

	diff := DiffLineItems(current, desired)

	if len(diff.ToAdd) != 1 {
		t.Errorf("ToAdd = %d, want 1", len(diff.ToAdd))
	}
	if len(diff.ToRemove) != 1 {
		t.Errorf("ToRemove = %d, want 1", len(diff.ToRemove))
	}
	if len(diff.ToUpdate) != 1 {
		t.Errorf("ToUpdate = %d, want 1", len(diff.ToUpdate))
	}

	// Verify add
	if diff.ToAdd[0].ProductID != "prod-4" {
		t.Errorf("ToAdd ProductID = %s, want prod-4", diff.ToAdd[0].ProductID)
	}

	// Verify remove
	if diff.ToRemove[0].ProductID != "prod-1" {
		t.Errorf("ToRemove ProductID = %s, want prod-1", diff.ToRemove[0].ProductID)
	}
	if diff.ToRemove[0].BackendID != "key-1" {
		t.Errorf("ToRemove BackendID = %s, want key-1", diff.ToRemove[0].BackendID)
	}

	// Verify update
	if diff.ToUpdate[0].ProductID != "prod-2" {
		t.Errorf("ToUpdate ProductID = %s, want prod-2", diff.ToUpdate[0].ProductID)
	}
}

func TestDiffLineItems_WithVariants(t *testing.T) {
	// Same product, different variants = different items
	current := []CurrentItem{
		{ProductID: "prod-1", VariantID: "var-a", BackendID: "key-1", Quantity: 1},
	}
	desired := []DesiredItem{
		{ProductID: "prod-1", VariantID: "var-a", Quantity: 1}, // no change
		{ProductID: "prod-1", VariantID: "var-b", Quantity: 2}, // add (different variant)
	}

	diff := DiffLineItems(current, desired)

	if len(diff.ToAdd) != 1 {
		t.Errorf("ToAdd = %d, want 1", len(diff.ToAdd))
	}
	if diff.ToAdd[0].VariantID != "var-b" {
		t.Errorf("ToAdd VariantID = %s, want var-b", diff.ToAdd[0].VariantID)
	}
	if len(diff.ToRemove) != 0 {
		t.Errorf("ToRemove = %d, want 0", len(diff.ToRemove))
	}
	if len(diff.ToUpdate) != 0 {
		t.Errorf("ToUpdate = %d, want 0", len(diff.ToUpdate))
	}
}

func TestDiffDiscounts_EmptyToCodes(t *testing.T) {
	current := []string{}
	desired := []string{"10OFF", "FREESHIP"}

	diff := DiffDiscounts(current, desired)

	if len(diff.ToApply) != 2 {
		t.Errorf("ToApply = %d, want 2", len(diff.ToApply))
	}
	if len(diff.ToRemove) != 0 {
		t.Errorf("ToRemove = %d, want 0", len(diff.ToRemove))
	}
}

func TestDiffDiscounts_CodesToEmpty(t *testing.T) {
	current := []string{"10OFF", "FREESHIP"}
	desired := []string{}

	diff := DiffDiscounts(current, desired)

	if len(diff.ToApply) != 0 {
		t.Errorf("ToApply = %d, want 0", len(diff.ToApply))
	}
	if len(diff.ToRemove) != 2 {
		t.Errorf("ToRemove = %d, want 2", len(diff.ToRemove))
	}
}

func TestDiffDiscounts_Replace(t *testing.T) {
	current := []string{"OLD_CODE"}
	desired := []string{"NEW_CODE"}

	diff := DiffDiscounts(current, desired)

	if len(diff.ToApply) != 1 || diff.ToApply[0] != "NEW_CODE" {
		t.Errorf("ToApply = %v, want [NEW_CODE]", diff.ToApply)
	}
	if len(diff.ToRemove) != 1 || diff.ToRemove[0] != "OLD_CODE" {
		t.Errorf("ToRemove = %v, want [OLD_CODE]", diff.ToRemove)
	}
}

func TestDiffDiscounts_NoChange(t *testing.T) {
	current := []string{"10OFF"}
	desired := []string{"10OFF"}

	diff := DiffDiscounts(current, desired)

	if !diff.IsEmpty() {
		t.Error("Expected empty diff for identical codes")
	}
}

func TestDiffDiscounts_PartialOverlap(t *testing.T) {
	current := []string{"A", "B"}
	desired := []string{"B", "C"}

	diff := DiffDiscounts(current, desired)

	if len(diff.ToApply) != 1 || diff.ToApply[0] != "C" {
		t.Errorf("ToApply = %v, want [C]", diff.ToApply)
	}
	if len(diff.ToRemove) != 1 || diff.ToRemove[0] != "A" {
		t.Errorf("ToRemove = %v, want [A]", diff.ToRemove)
	}
}

func TestFulfillmentChanged(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		desired  string
		expected bool
	}{
		{"empty to value", "", "rate-1", true},
		{"value to different", "rate-1", "rate-2", true},
		{"same value", "rate-1", "rate-1", false},
		{"value to empty", "rate-1", "", false}, // empty desired = no change requested
		{"both empty", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FulfillmentChanged(tt.current, tt.desired)
			if result != tt.expected {
				t.Errorf("FulfillmentChanged(%q, %q) = %v, want %v",
					tt.current, tt.desired, result, tt.expected)
			}
		})
	}
}

func TestLineItemDiff_IsEmpty(t *testing.T) {
	empty := &LineItemDiff{}
	if !empty.IsEmpty() {
		t.Error("Expected empty diff to report IsEmpty=true")
	}

	withAdd := &LineItemDiff{ToAdd: []ItemToAdd{{ProductID: "p1"}}}
	if withAdd.IsEmpty() {
		t.Error("Expected diff with adds to report IsEmpty=false")
	}

	withRemove := &LineItemDiff{ToRemove: []ItemToRemove{{ProductID: "p1"}}}
	if withRemove.IsEmpty() {
		t.Error("Expected diff with removes to report IsEmpty=false")
	}

	withUpdate := &LineItemDiff{ToUpdate: []ItemToUpdate{{ProductID: "p1"}}}
	if withUpdate.IsEmpty() {
		t.Error("Expected diff with updates to report IsEmpty=false")
	}
}
