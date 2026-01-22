// Package model defines data structures for UCP protocol and platform APIs.
package model

import (
	"encoding/json"
	"time"
)

// === Root Types ===

// Checkout represents a UCP checkout session.
// This is the primary response type for all checkout operations.
type Checkout struct {
	UCP       UCPMetadata    `json:"ucp"`
	ID        string         `json:"id"`
	Status    CheckoutStatus `json:"status"`
	Currency  string         `json:"currency"`
	LineItems []LineItem     `json:"line_items"`
	Totals    []Total        `json:"totals"`
	Links     []Link         `json:"links"`
	Payment   Payment        `json:"payment"`
	Buyer     *Buyer         `json:"buyer,omitempty"`
	Messages  []Message      `json:"messages,omitempty"`

	// dev.ucp.shopping.discount extension
	Discounts *Discounts `json:"discounts,omitempty"`

	// dev.ucp.shopping.fulfillment extension
	FulfillmentAddress  *PostalAddress      `json:"fulfillment_address,omitempty"`
	FulfillmentOptions  []FulfillmentOption `json:"fulfillment_options,omitempty"`
	FulfillmentOptionID string              `json:"fulfillment_option_id,omitempty"`
	ExpiresAt           *time.Time          `json:"expires_at,omitempty"`
	ContinueURL         string              `json:"continue_url,omitempty"`

	// Order fields - populated after checkout completion
	OrderID           string `json:"order_id,omitempty"`
	OrderPermalinkURL string `json:"order_permalink_url,omitempty"`
}

// FulfillmentOption represents an available fulfillment method per dev.ucp.shopping.fulfillment.
// Required fields: id, type, title, subtotal.
type FulfillmentOption struct {
	ID                      string `json:"id"`
	Type                    string `json:"type"`                                // "shipping", "pickup", "digital"
	Title                   string `json:"title"`                               // Display title, e.g., "Standard Shipping"
	SubTitle                string `json:"sub_title,omitempty"`                 // Additional info, e.g., "Arrives in 4-5 days"
	Carrier                 string `json:"carrier,omitempty"`                   // Carrier name for shipping
	EarliestFulfillmentTime string `json:"earliest_fulfillment_time,omitempty"` // RFC3339 timestamp
	LatestFulfillmentTime   string `json:"latest_fulfillment_time,omitempty"`   // RFC3339 timestamp
	Subtotal                int64  `json:"subtotal"`                            // Cost before tax (cents)
	Tax                     int64  `json:"tax,omitempty"`                       // Tax amount (cents)
	Total                   int64  `json:"total,omitempty"`                     // Total cost (cents)
}

// UCPMetadata contains protocol version and registries for capabilities and handlers.
// Uses registry pattern: maps keyed by reverse-domain name (e.g., "dev.ucp.shopping.checkout").
// Per UCP spec, this structure is used in both discovery profiles and response envelopes,
// with different fields populated for each context.
type UCPMetadata struct {
	Version         string                      `json:"version"`
	Services        map[string][]Service        `json:"services,omitempty"`         // Discovery only
	Capabilities    map[string][]Capability     `json:"capabilities,omitempty"`     // Both discovery and response
	PaymentHandlers map[string][]PaymentHandler `json:"payment_handlers,omitempty"` // Both discovery and response
}

// Service represents a transport binding for a UCP capability.
// Each transport (REST, MCP, etc.) is a separate service entry.
type Service struct {
	Version   string `json:"version"`
	Transport string `json:"transport"`          // "rest", "mcp", "a2a", "embedded"
	Endpoint  string `json:"endpoint,omitempty"` // URL for the transport
	Spec      string `json:"spec,omitempty"`     // URL to human-readable spec
	Schema    string `json:"schema,omitempty"`   // URL to JSON Schema
}

// Capability declares a supported UCP capability using the entity pattern.
// Capabilities are keyed by reverse-domain name in the registry.
type Capability struct {
	Version string        `json:"version"`
	Spec    string        `json:"spec,omitempty"`
	Schema  string        `json:"schema,omitempty"`
	Extends *ExtendsField `json:"extends,omitempty"` // Parent capability(s) for extensions
}

// ExtendsField supports both string and []string for single/multi-parent extensions.
type ExtendsField struct {
	single   string
	multiple []string
}

// UnmarshalJSON handles both "string" and ["string", ...] formats.
func (e *ExtendsField) UnmarshalJSON(data []byte) error {
	// Handle null
	if string(data) == "null" {
		return nil
	}

	// Try string first
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		e.single = s
		e.multiple = nil
		return nil
	}

	// Try array
	var arr []string
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}
	e.single = ""
	e.multiple = arr
	return nil
}

// MarshalJSON outputs string for single parent, array for multiple.
func (e ExtendsField) MarshalJSON() ([]byte, error) {
	if e.single != "" {
		return json.Marshal(e.single)
	}
	if len(e.multiple) > 0 {
		return json.Marshal(e.multiple)
	}
	// Empty - omit via omitempty
	return []byte("null"), nil
}

// GetParents returns all parent capability names as a slice.
func (e ExtendsField) GetParents() []string {
	if e.single != "" {
		return []string{e.single}
	}
	return e.multiple
}

// IsExtension returns true if this capability extends one or more parents.
func (e ExtendsField) IsExtension() bool {
	return e.single != "" || len(e.multiple) > 0
}

// IsZero returns true if no extends value is set (for omitempty support).
func (e ExtendsField) IsZero() bool {
	return e.single == "" && len(e.multiple) == 0
}

// NewSingleExtends creates an ExtendsField with a single parent.
func NewSingleExtends(parent string) *ExtendsField {
	return &ExtendsField{single: parent}
}

// NewMultiExtends creates an ExtendsField with multiple parents.
func NewMultiExtends(parents ...string) *ExtendsField {
	return &ExtendsField{multiple: parents}
}

// === Enums ===

// CheckoutStatus represents the state of a checkout session.
// Values per UCP schema: squirrel/source/schemas/shopping/checkout.json
type CheckoutStatus string

const (
	StatusIncomplete         CheckoutStatus = "incomplete"           // Cart building, missing required data
	StatusReadyForComplete   CheckoutStatus = "ready_for_complete"   // All data present, can call complete
	StatusCompleteInProgress CheckoutStatus = "complete_in_progress" // Payment processing
	StatusCompleted          CheckoutStatus = "completed"            // Order finalized
	StatusCanceled           CheckoutStatus = "canceled"             // Order canceled
	StatusRequiresEscalation CheckoutStatus = "requires_escalation"  // Human intervention needed
)

// MessageSeverity indicates how an error should be handled.
// See: squirrel/source/schemas/shopping/types/message_error.json
type MessageSeverity string

const (
	SeverityRecoverable   MessageSeverity = "recoverable"   // Agent can fix with different input
	SeverityUnrecoverable MessageSeverity = "unrecoverable" // Cannot proceed, need different approach
	SeverityEscalation    MessageSeverity = "escalation"    // Human must intervene
)

// TotalType categorizes different pricing components.
// Per UCP spec: items_discount, subtotal, discount, fulfillment, tax, fee, total
type TotalType string

const (
	TotalTypeItemsDiscount TotalType = "items_discount"
	TotalTypeSubtotal      TotalType = "subtotal"
	TotalTypeDiscount      TotalType = "discount"
	TotalTypeFulfillment   TotalType = "fulfillment"
	TotalTypeTax           TotalType = "tax"
	TotalTypeFee           TotalType = "fee"
	TotalTypeTotal         TotalType = "total"
)

// LinkType categorizes merchant policy links.
type LinkType string

const (
	LinkTypePrivacyPolicy  LinkType = "privacy_policy"
	LinkTypeTermsOfService LinkType = "terms_of_service"
	LinkTypeRefundPolicy   LinkType = "refund_policy"
	LinkTypeShippingPolicy LinkType = "shipping_policy"
	LinkTypeFAQ            LinkType = "faq"
)

// === Line Items ===

// LineItem represents a product in the checkout with pricing.
type LineItem struct {
	ID         string `json:"id"`
	Item       Item   `json:"item"`
	Quantity   int    `json:"quantity"`
	BaseAmount int64  `json:"base_amount"`        // cents, price × quantity before discounts
	Discount   int64  `json:"discount,omitempty"` // cents, line-level discount
	Subtotal   int64  `json:"subtotal"`           // cents, after line discounts
	Total      int64  `json:"total"`              // cents, final line total
	ParentID   string `json:"parent_id,omitempty"`
}

// Item represents product details within a line item.
type Item struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Price    int64  `json:"price"` // cents, unit price
	ImageURL string `json:"image_url,omitempty"`
}

// === Discounts (dev.ucp.shopping.discount extension) ===

// Discounts represents the discount capability extension.
// Contains both input (codes to apply) and output (applied discounts).
type Discounts struct {
	Codes   []string          `json:"codes,omitempty"`   // Input: discount codes to apply
	Applied []AppliedDiscount `json:"applied,omitempty"` // Output: successfully applied discounts
}

// AppliedDiscount represents a discount that was successfully applied.
// Per squirrel spec: code is omitted for automatic discounts.
type AppliedDiscount struct {
	Code        string       `json:"code,omitempty"`        // Discount code (omitted for automatic)
	Title       string       `json:"title"`                 // Human-readable name
	Amount      int64        `json:"amount"`                // Total discount in minor units (cents)
	Automatic   bool         `json:"automatic,omitempty"`   // True if applied automatically
	Method      string       `json:"method,omitempty"`      // "each" or "across"
	Priority    int          `json:"priority,omitempty"`    // Stacking order (1 = first)
	Allocations []Allocation `json:"allocations,omitempty"` // Breakdown per target
}

// Allocation shows where a discount amount was applied.
// Path uses JSONPath format (e.g., "$.line_items[0]").
type Allocation struct {
	Path   string `json:"path"`   // JSONPath to target
	Amount int64  `json:"amount"` // Amount allocated (cents)
}

// === Totals & Links ===

// Total represents a categorized price component.
// All amounts are in minor currency units (cents).
type Total struct {
	Type        TotalType `json:"type"`
	Amount      int64     `json:"amount"` // cents, >= 0
	DisplayText string    `json:"display_text,omitempty"`
}

// Link represents a merchant policy URL.
type Link struct {
	Type  LinkType `json:"type"`
	URL   string   `json:"url"`
	Title string   `json:"title,omitempty"`
}

// === Payment ===

// Payment contains submitted payment instruments.
// Payment handlers are now in ucp.payment_handlers, not here.
type Payment struct {
	Instruments []PaymentInstrument `json:"instruments,omitempty"`
}

// SelectedInstrument returns the first instrument with Selected=true, or nil.
func (p Payment) SelectedInstrument() *PaymentInstrument {
	for i := range p.Instruments {
		if p.Instruments[i].Selected {
			return &p.Instruments[i]
		}
	}
	return nil
}

// PaymentHandler defines a payment collection strategy using the entity pattern.
// Handlers are keyed by reverse-domain name (e.g., "com.stripe") in the registry.
type PaymentHandler struct {
	ID      string      `json:"id"`               // Unique identifier for this handler instance
	Version string      `json:"version"`          // YYYY-MM-DD format
	Spec    string      `json:"spec,omitempty"`   // URL to human-readable spec
	Schema  string      `json:"schema,omitempty"` // URL to JSON Schema
	Config  interface{} `json:"config,omitempty"` // Handler-specific configuration
}

// PaymentInstrument represents a payment method submitted by the buyer.
type PaymentInstrument struct {
	ID             string           `json:"id"`
	HandlerID      string           `json:"handler_id"`
	Type           string           `json:"type"`               // e.g., "card", "tokenized_card"
	Selected       bool             `json:"selected,omitempty"` // Whether this instrument is selected
	Credential     *TokenCredential `json:"credential,omitempty"`
	BillingAddress *PostalAddress   `json:"billing_address,omitempty"`
	Display        interface{}      `json:"display,omitempty"` // Display information (optional)
}

// TokenCredential contains payment token data.
// For Stripe: Type="stripe.payment_method", Token="pm_xxx"
type TokenCredential struct {
	Type  string `json:"type"`
	Token string `json:"token"`
}

// === Address & Buyer ===

// PostalAddress represents a mailing address.
// All fields optional to support international variations.
type PostalAddress struct {
	StreetAddress   string `json:"street_address,omitempty"`
	ExtendedAddress string `json:"extended_address,omitempty"` // apartment, suite, etc.
	Locality        string `json:"address_locality,omitempty"` // city
	Region          string `json:"address_region,omitempty"`   // state/province
	Country         string `json:"address_country,omitempty"`  // ISO 3166-1 alpha-2
	PostalCode      string `json:"postal_code,omitempty"`
	FirstName       string `json:"first_name,omitempty"`
	LastName        string `json:"last_name,omitempty"`
	PhoneNumber     string `json:"phone_number,omitempty"` // E.164 format
}

// Buyer represents the purchasing customer.
type Buyer struct {
	FirstName   string `json:"first_name,omitempty"`
	LastName    string `json:"last_name,omitempty"`
	Email       string `json:"email,omitempty"`
	PhoneNumber string `json:"phone_number,omitempty"`
}

// Context provides provisional buyer signals for localization and personalization.
// Used to determine pricing, currency, availability before full address is known.
// Higher-resolution data (shipping/billing address) supersedes context.
type Context struct {
	AddressCountry string `json:"address_country,omitempty"` // ISO 3166-1 alpha-2 (e.g., "US", "CA")
	AddressRegion  string `json:"address_region,omitempty"`  // State/province (e.g., "CA", "ON")
	PostalCode     string `json:"postal_code,omitempty"`     // Postal/ZIP code
	Intent         string `json:"intent,omitempty"`          // Buyer intent for relevance/personalization
}

// === Messages ===

// Message represents feedback about checkout state.
// Type discriminates between error, warning, and info messages.
// For type="error", severity is REQUIRED per UCP schema.
type Message struct {
	Type        string `json:"type"`                   // "error", "warning", "info"
	Code        string `json:"code,omitempty"`         // e.g., "invalid_coupon", "out_of_stock"
	Content     string `json:"content"`                // Human-readable message
	Path        string `json:"path,omitempty"`         // RFC 9535 JSONPath to field
	ContentType string `json:"content_type,omitempty"` // "plain" or "markdown"
	Severity    string `json:"severity,omitempty"`     // REQUIRED for errors: recoverable|unrecoverable|escalation
}

// NewErrorMessage creates an error message with required severity.
func NewErrorMessage(code, content string, severity MessageSeverity) Message {
	return Message{
		Type:     "error",
		Code:     code,
		Content:  content,
		Severity: string(severity),
	}
}

// NewInfoMessage creates an informational message.
func NewInfoMessage(code, content string) Message {
	return Message{
		Type:    "info",
		Code:    code,
		Content: content,
	}
}

// NewWarningMessage creates a warning message.
// Per UCP spec, warnings are for issues that affect user expectations (e.g., rejected discounts)
// but don't prevent checkout from proceeding.
func NewWarningMessage(code, content string) Message {
	return Message{
		Type:    "warning",
		Code:    code,
		Content: content,
	}
}

// NewWarningMessageWithPath creates a warning message pointing to a specific field.
func NewWarningMessageWithPath(code, content, path string) Message {
	return Message{
		Type:    "warning",
		Code:    code,
		Content: content,
		Path:    path,
	}
}

// === Request Types ===

// CheckoutUpdateRequest contains fields for updating a checkout.
// For full PUT semantics, fields represent the complete desired state.
// Omitted fields are not changed; present fields replace current values.
type CheckoutUpdateRequest struct {
	// LineItems: REQUIRED - the complete desired line items state.
	// Reconciler diffs against current backend state and executes add/remove/update.
	LineItems []LineItemRequest `json:"line_items"`

	ShippingAddress     *PostalAddress `json:"shipping_address,omitempty"`
	BillingAddress      *PostalAddress `json:"billing_address,omitempty"`
	FulfillmentOptionID string         `json:"fulfillment_option_id,omitempty"`

	// DiscountCodes: REQUIRED - the complete desired discount codes.
	// Empty array means no discounts. Reconciler diffs and applies/removes as needed.
	DiscountCodes []string `json:"discount_codes"`

	Buyer   *Buyer   `json:"buyer,omitempty"`
	Context *Context `json:"context,omitempty"` // Provisional localization signals
}

// LineItemRequest specifies a line item in an update request.
// Uses ProductID for matching against current items.
type LineItemRequest struct {
	ProductID string `json:"product_id"`
	VariantID string `json:"variant_id,omitempty"`
	Quantity  int    `json:"quantity"`
}

// CheckoutSubmitRequest contains payment for completing checkout.
type CheckoutSubmitRequest struct {
	Payment Payment `json:"payment"`
}

// === Discovery Profile ===

// DiscoveryProfile is returned by /.well-known/ucp endpoint.
// Advertises available services, capabilities, and payment handlers.
// Uses the same UCPMetadata structure as responses, populated for discovery context.
type DiscoveryProfile struct {
	UCP UCPMetadata `json:"ucp"`
}
