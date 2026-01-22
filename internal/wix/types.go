// Package wix implements the adapter for Wix stores using the eCommerce and Checkout APIs.
//
// Current Implementation (Escalation Flow):
// Wix payment processing requires browser-based checkout. This adapter builds the checkout
// via API (cart, addresses, shipping), then returns `status: requires_escalation` with a
// `continue_url` when all non-payment fields are complete. The buyer completes payment
// on Wix's hosted checkout page. Programmatic payment completion to be added.
//
// Authentication:
// Uses OAuth2 with anonymous visitor tokens (grantType: "anonymous").
// No client_secret needed - only client_id. Tokens expire in 4 hours (14400s).
// Each token represents a unique visitor session tied to cart/checkout state.
package wix

// === OAuth2 Types ===

// OAuthTokenRequest is the request body for anonymous visitor token.
// Wix Headless uses grantType "anonymous" for visitor sessions.
type OAuthTokenRequest struct {
	ClientID  string `json:"clientId"`
	GrantType string `json:"grantType"` // Always "anonymous" for visitor sessions
}

// OAuthTokenResponse contains the OAuth2 token from Wix.
// Access token is valid for 4 hours (14400 seconds).
type OAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`    // "Bearer"
	ExpiresIn    int    `json:"expires_in"`    // 14400 (4 hours)
	RefreshToken string `json:"refresh_token"` // For extending session if needed
}

// OAuthRefreshRequest is used to refresh an expired access token.
type OAuthRefreshRequest struct {
	ClientID     string `json:"clientId"`
	GrantType    string `json:"grantType"` // "refresh_token"
	RefreshToken string `json:"refreshToken"`
}

// === Wix eCommerce API Response Types ===

// WixCart represents a Wix eCommerce cart.
type WixCart struct {
	ID        string         `json:"id"`
	LineItems []WixLineItem  `json:"lineItems"`
	Currency  string         `json:"currency"`
	Totals    *WixCartTotals `json:"totals,omitempty"`
}

// WixCartTotals contains pricing summary for a cart.
// All amounts are strings representing decimal values (e.g., "99.00").
type WixCartTotals struct {
	Subtotal string `json:"subtotal"`
	Total    string `json:"total"`
}

// WixLineItem represents an item in a Wix cart or checkout.
type WixLineItem struct {
	ID                 string            `json:"id,omitempty"`
	CatalogReference   *WixCatalogRef    `json:"catalogReference"`
	Quantity           int               `json:"quantity"`
	ProductName        *WixProductName   `json:"productName,omitempty"`
	Price              *WixPrice         `json:"price,omitempty"`
	Image              *WixImage         `json:"image,omitempty"`
	PhysicalProperties *WixPhysicalProps `json:"physicalProperties,omitempty"`
}

// WixCatalogRef identifies a product in the Wix catalog.
type WixCatalogRef struct {
	CatalogItemID string `json:"catalogItemId"`
	AppID         string `json:"appId"` // Usually "215238eb-22a5-4c36-9e7b-e7c08025e04e" for Wix Stores
}

// WixStoresAppID is the Wix Stores application ID for catalog references.
const WixStoresAppID = "215238eb-22a5-4c36-9e7b-e7c08025e04e"

// WixProductName contains localized product name.
type WixProductName struct {
	Original   string `json:"original,omitempty"`
	Translated string `json:"translated,omitempty"`
}

// WixPrice contains price information.
type WixPrice struct {
	Amount          string `json:"amount"`
	ConvertedAmount string `json:"convertedAmount,omitempty"`
	FormattedAmount string `json:"formattedAmount,omitempty"`
}

// WixImage represents a product image.
type WixImage struct {
	ID     string `json:"id,omitempty"`
	URL    string `json:"url"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// WixPhysicalProps indicates if item requires shipping.
type WixPhysicalProps struct {
	ShippingRequired bool `json:"shippingRequired"`
}

// === Checkout Types ===

// WixCheckout represents a Wix eCommerce checkout.
type WixCheckout struct {
	ID                       string               `json:"id"`
	LineItems                []WixLineItem        `json:"lineItems,omitempty"`
	ShippingInfo             *WixShippingInfo     `json:"shippingInfo,omitempty"`
	BillingInfo              *WixBillingInfo      `json:"billingInfo,omitempty"`
	BuyerInfo                *WixBuyerInfo        `json:"buyerInfo,omitempty"`
	PriceSummary             *WixPriceSummary     `json:"priceSummary,omitempty"`
	Currency                 string               `json:"currency,omitempty"`
	ChannelType              string               `json:"channelType,omitempty"`
	AvailableShippingOptions []WixShippingOption  `json:"availableShippingOptions,omitempty"`
	SelectedShippingOption   *WixSelectedShipping `json:"selectedShippingOption,omitempty"`
	AppliedDiscounts         []WixAppliedDiscount `json:"appliedDiscounts,omitempty"`
}

// WixShippingInfo contains shipping address and details.
type WixShippingInfo struct {
	ShippingDestination          *WixShippingDestination  `json:"shippingDestination,omitempty"`
	SelectedCarrierServiceOption *WixSelectedShipping     `json:"selectedCarrierServiceOption,omitempty"`
	CarrierServiceOptions        []WixCarrierServiceGroup `json:"carrierServiceOptions,omitempty"`
}

// WixCarrierServiceGroup groups shipping options by carrier.
type WixCarrierServiceGroup struct {
	CarrierID       string              `json:"carrierId,omitempty"`
	ShippingOptions []WixShippingOption `json:"shippingOptions,omitempty"`
}

// WixShippingDestination contains the nested address structure for shipping.
// Wix API requires address fields nested under "address", not directly on destination.
type WixShippingDestination struct {
	Address        *WixAddress        `json:"address,omitempty"`
	ContactDetails *WixContactDetails `json:"contactDetails,omitempty"`
}

// WixBillingInfo contains billing address.
type WixBillingInfo struct {
	Address        *WixAddress        `json:"address,omitempty"`
	ContactDetails *WixContactDetails `json:"contactDetails,omitempty"`
}

// WixBuyerInfo contains buyer identity information.
type WixBuyerInfo struct {
	Email     string `json:"email,omitempty"`
	FirstName string `json:"firstName,omitempty"`
	LastName  string `json:"lastName,omitempty"`
	Phone     string `json:"phone,omitempty"`
}

// WixAddress represents a Wix address (street, city, etc).
// ContactDetails are stored separately at the destination/billingInfo level.
// Note: Wix eCommerce API uses "addressLine" (not "addressLine1" or "streetAddress").
// See: https://dev.wix.com/docs/rest/business-solutions/e-commerce/cart/address-object-conversion
type WixAddress struct {
	AddressLine  string `json:"addressLine,omitempty"`  // Primary street address (maps from addressLine1)
	AddressLine2 string `json:"addressLine2,omitempty"` // Apt, suite, etc.
	City         string `json:"city,omitempty"`
	Subdivision  string `json:"subdivision,omitempty"` // State/Province code (e.g., "US-CA")
	Country      string `json:"country,omitempty"`     // ISO 3166-1 alpha-2
	PostalCode   string `json:"postalCode,omitempty"`
}

// WixContactDetails contains name and phone for addresses.
type WixContactDetails struct {
	FirstName string `json:"firstName,omitempty"`
	LastName  string `json:"lastName,omitempty"`
	Phone     string `json:"phone,omitempty"`
}

// WixPriceSummary contains checkout pricing breakdown.
// All amounts are strings representing decimal values.
type WixPriceSummary struct {
	Subtotal *WixPrice `json:"subtotal,omitempty"`
	Shipping *WixPrice `json:"shipping,omitempty"`
	Tax      *WixPrice `json:"tax,omitempty"`
	Discount *WixPrice `json:"discount,omitempty"`
	Total    *WixPrice `json:"total,omitempty"`
}

// WixShippingOption represents an available shipping method.
type WixShippingOption struct {
	Code      string           `json:"code"`
	Title     string           `json:"title"`
	Logistics *WixLogistics    `json:"logistics,omitempty"`
	Cost      *WixShippingCost `json:"cost,omitempty"`
}

// WixShippingCost wraps shipping price (Wix nests it under "price").
type WixShippingCost struct {
	Price *WixPrice `json:"price,omitempty"`
}

// WixLogistics contains delivery time information.
type WixLogistics struct {
	DeliveryTime string `json:"deliveryTime,omitempty"`
}

// WixSelectedShipping represents the selected shipping option.
type WixSelectedShipping struct {
	Code                 string            `json:"code,omitempty"`
	Title                string            `json:"title,omitempty"`
	Cost                 *WixPrice         `json:"cost,omitempty"`
	CarrierServiceOption *WixCarrierOption `json:"carrierServiceOption,omitempty"`
}

// WixCarrierOption contains carrier service details.
type WixCarrierOption struct {
	Code  string `json:"code"`
	Title string `json:"title,omitempty"`
}

// WixAppliedDiscount represents a discount applied to checkout.
type WixAppliedDiscount struct {
	DiscountType string     `json:"discountType,omitempty"`
	LineItemIDs  []string   `json:"lineItemIds,omitempty"`
	Coupon       *WixCoupon `json:"coupon,omitempty"`
	Amount       *WixPrice  `json:"amount,omitempty"`
}

// WixCoupon represents a coupon discount.
type WixCoupon struct {
	ID     string    `json:"id,omitempty"`
	Code   string    `json:"code"`
	Name   string    `json:"name,omitempty"`
	Amount *WixPrice `json:"amount,omitempty"` // Discount amount
}

// === Redirect Session Types ===

// WixRedirectSession represents a redirect session for hosted checkout.
type WixRedirectSession struct {
	ID      string `json:"id,omitempty"`
	FullURL string `json:"fullUrl"`
}

// === API Request Types ===

// WixAddToCartRequest is the request body for adding items to cart.
type WixAddToCartRequest struct {
	LineItems []WixLineItemInput `json:"lineItems"`
}

// WixLineItemInput is used when adding items to cart.
type WixLineItemInput struct {
	CatalogReference *WixCatalogRef `json:"catalogReference"`
	Quantity         int            `json:"quantity"`
}

// WixCreateCheckoutRequest creates a checkout from cart.
type WixCreateCheckoutRequest struct {
	CartID      string `json:"cartId"`
	ChannelType string `json:"channelType,omitempty"` // "WEB", "OTHER_PLATFORM"
}

// WixUpdateCheckoutRequest updates checkout fields.
// CouponCode is at root level, not inside Checkout (per Wix API spec).
type WixUpdateCheckoutRequest struct {
	Checkout   *WixCheckoutUpdate `json:"checkout"`
	CouponCode string             `json:"couponCode,omitempty"`
}

// WixCheckoutUpdate contains fields to update on checkout.
type WixCheckoutUpdate struct {
	ShippingInfo *WixShippingInfo `json:"shippingInfo,omitempty"`
	BillingInfo  *WixBillingInfo  `json:"billingInfo,omitempty"`
	BuyerInfo    *WixBuyerInfo    `json:"buyerInfo,omitempty"`
}

// WixCreateRedirectRequest creates a redirect session.
type WixCreateRedirectRequest struct {
	EcomCheckout *WixEcomCheckoutRef `json:"ecomCheckout"`
	Callbacks    *WixCallbacks       `json:"callbacks,omitempty"`
}

// WixEcomCheckoutRef references a checkout for redirect.
type WixEcomCheckoutRef struct {
	CheckoutID string `json:"checkoutId"`
}

// WixCallbacks contains redirect callback URLs.
type WixCallbacks struct {
	PostFlowURL     string `json:"postFlowUrl,omitempty"`
	ThankYouPageURL string `json:"thankYouPageUrl,omitempty"`
}

// === Checkout Line Item Operations ===

// WixAddToCheckoutRequest adds line items to an existing checkout.
// POST /ecom/v1/checkouts/{checkoutId}/add-to-checkout
type WixAddToCheckoutRequest struct {
	LineItems []WixLineItemInput `json:"lineItems"`
}

// WixRemoveLineItemsRequest removes line items by ID.
// POST /ecom/v1/checkouts/{checkoutId}/remove-line-items
type WixRemoveLineItemsRequest struct {
	LineItemIDs []string `json:"lineItemIds"`
}

// WixUpdateQuantityRequest updates quantities of existing line items.
// POST /ecom/v1/checkouts/{checkoutId}/update-line-items-quantity
type WixUpdateQuantityRequest struct {
	LineItems []WixQuantityUpdate `json:"lineItems"`
}

// WixQuantityUpdate specifies a quantity change for a single line item.
type WixQuantityUpdate struct {
	ID       string `json:"id"`       // Line item ID (from checkout.lineItems[].id)
	Quantity int    `json:"quantity"` // New quantity
}

// === API Response Wrappers ===

// WixCartResponse wraps cart API responses.
type WixCartResponse struct {
	Cart *WixCart `json:"cart"`
}

// WixCheckoutResponse wraps checkout API responses.
type WixCheckoutResponse struct {
	Checkout *WixCheckout `json:"checkout"`
}

// WixRedirectResponse wraps redirect session API responses.
type WixRedirectResponse struct {
	RedirectSession *WixRedirectSession `json:"redirectSession"`
}

// WixShippingOptionsResponse wraps shipping options response.
type WixShippingOptionsResponse struct {
	ShippingOptions []WixShippingOption `json:"shippingOptions"`
}

// WixErrorResponse represents a Wix API error.
type WixErrorResponse struct {
	Message string           `json:"message"`
	Details *WixErrorDetails `json:"details,omitempty"`
}

// WixErrorDetails contains additional error information.
type WixErrorDetails struct {
	ApplicationError *WixApplicationError `json:"applicationError,omitempty"`
}

// WixApplicationError contains Wix-specific error codes.
type WixApplicationError struct {
	Code        string `json:"code"`
	Description string `json:"description,omitempty"`
}
