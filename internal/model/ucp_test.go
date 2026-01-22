package model

import (
	"testing"
)

func TestNewErrorMessage(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		content  string
		severity MessageSeverity
	}{
		{
			name:     "recoverable error",
			code:     "INVALID_COUPON",
			content:  "Coupon code is expired",
			severity: SeverityRecoverable,
		},
		{
			name:     "unrecoverable error",
			code:     "OUT_OF_STOCK",
			content:  "Product is no longer available",
			severity: SeverityUnrecoverable,
		},
		{
			name:     "escalation error",
			code:     "PAYMENT_HANDOFF",
			content:  "Please complete payment in browser",
			severity: SeverityEscalation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := NewErrorMessage(tt.code, tt.content, tt.severity)

			if msg.Type != "error" {
				t.Errorf("Type = %q, want %q", msg.Type, "error")
			}
			if msg.Code != tt.code {
				t.Errorf("Code = %q, want %q", msg.Code, tt.code)
			}
			if msg.Content != tt.content {
				t.Errorf("Content = %q, want %q", msg.Content, tt.content)
			}
			if msg.Severity != string(tt.severity) {
				t.Errorf("Severity = %q, want %q", msg.Severity, tt.severity)
			}
		})
	}
}

func TestNewInfoMessage(t *testing.T) {
	msg := NewInfoMessage("SHIPPING_UPDATE", "Estimated delivery: 3-5 days")

	if msg.Type != "info" {
		t.Errorf("Type = %q, want %q", msg.Type, "info")
	}
	if msg.Code != "SHIPPING_UPDATE" {
		t.Errorf("Code = %q, want %q", msg.Code, "SHIPPING_UPDATE")
	}
	if msg.Content != "Estimated delivery: 3-5 days" {
		t.Errorf("Content = %q, want %q", msg.Content, "Estimated delivery: 3-5 days")
	}
	if msg.Severity != "" {
		t.Errorf("Severity should be empty for info messages, got %q", msg.Severity)
	}
}

func TestNewWarningMessage(t *testing.T) {
	msg := NewWarningMessage("COUPON_PARTIAL", "Coupon applied to eligible items only")

	if msg.Type != "warning" {
		t.Errorf("Type = %q, want %q", msg.Type, "warning")
	}
	if msg.Code != "COUPON_PARTIAL" {
		t.Errorf("Code = %q, want %q", msg.Code, "COUPON_PARTIAL")
	}
	if msg.Content != "Coupon applied to eligible items only" {
		t.Errorf("Content = %q, want %q", msg.Content, "Coupon applied to eligible items only")
	}
	if msg.Path != "" {
		t.Errorf("Path should be empty, got %q", msg.Path)
	}
}

func TestNewWarningMessageWithPath(t *testing.T) {
	msg := NewWarningMessageWithPath("QUANTITY_ADJUSTED", "Reduced to available stock", "$.line_items[0].quantity")

	if msg.Type != "warning" {
		t.Errorf("Type = %q, want %q", msg.Type, "warning")
	}
	if msg.Code != "QUANTITY_ADJUSTED" {
		t.Errorf("Code = %q, want %q", msg.Code, "QUANTITY_ADJUSTED")
	}
	if msg.Content != "Reduced to available stock" {
		t.Errorf("Content = %q, want %q", msg.Content, "Reduced to available stock")
	}
	if msg.Path != "$.line_items[0].quantity" {
		t.Errorf("Path = %q, want %q", msg.Path, "$.line_items[0].quantity")
	}
}

func TestPayment_SelectedInstrument(t *testing.T) {
	tests := []struct {
		name    string
		payment Payment
		wantID  string
		wantNil bool
	}{
		{
			name: "finds selected instrument",
			payment: Payment{
				Instruments: []PaymentInstrument{
					{ID: "instr_1", Type: "card", Selected: false},
					{ID: "instr_2", Type: "google_pay", Selected: true},
					{ID: "instr_3", Type: "card", Selected: false},
				},
			},
			wantID:  "instr_2",
			wantNil: false,
		},
		{
			name: "returns first selected when multiple selected",
			payment: Payment{
				Instruments: []PaymentInstrument{
					{ID: "instr_1", Type: "card", Selected: true},
					{ID: "instr_2", Type: "google_pay", Selected: true},
				},
			},
			wantID:  "instr_1",
			wantNil: false,
		},
		{
			name: "returns nil when none selected",
			payment: Payment{
				Instruments: []PaymentInstrument{
					{ID: "instr_1", Type: "card", Selected: false},
				},
			},
			wantNil: true,
		},
		{
			name: "returns nil when no instruments",
			payment: Payment{
				Instruments: nil,
			},
			wantNil: true,
		},
		{
			name:    "returns nil for empty payment",
			payment: Payment{},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.payment.SelectedInstrument()

			if tt.wantNil {
				if got != nil {
					t.Errorf("SelectedInstrument() = %+v, want nil", got)
				}
				return
			}

			if got == nil {
				t.Fatal("SelectedInstrument() = nil, want non-nil")
			}
			if got.ID != tt.wantID {
				t.Errorf("SelectedInstrument().ID = %q, want %q", got.ID, tt.wantID)
			}
		})
	}
}

// TestPayment_SelectedInstrument_ReturnsPointerToOriginal verifies that the returned
// pointer references the actual instrument in the slice, not a copy.
func TestPayment_SelectedInstrument_ReturnsPointerToOriginal(t *testing.T) {
	payment := Payment{
		Instruments: []PaymentInstrument{
			{ID: "instr_1", Type: "card", Selected: true},
		},
	}

	selected := payment.SelectedInstrument()
	if selected == nil {
		t.Fatal("SelectedInstrument() = nil, want non-nil")
	}

	// Modify through the pointer
	selected.Type = "modified"

	// Verify the original slice was modified
	if payment.Instruments[0].Type != "modified" {
		t.Error("SelectedInstrument() should return pointer to original, not a copy")
	}
}
