package wix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ucp-proxy/internal/model"
	"ucp-proxy/internal/transport"
)

// =============================================================================
// WIX API CLIENT
// =============================================================================
//
// Wix Headless eCommerce uses OAuth2 for authentication:
//   1. Exchange client_id for anonymous access token
//   2. Use access_token in Authorization header for API calls
//   3. Refresh token when expired using refresh_token
//
// Session Management:
// Anonymous tokens represent visitor sessions. Each token is tied to a cart/checkout.
// For stateless operation, we embed both access_token and refresh_token in checkout ID.
// =============================================================================

const (
	// wixBaseURL is the base URL for Wix APIs.
	wixBaseURL = "https://www.wixapis.com"

	// API paths (validated against live Wix Headless API)
	pathOAuthToken      = "/oauth2/token"
	pathCartCurrent     = "/ecom/v1/carts/current"
	pathAddToCart       = "/ecom/v1/carts/current/add-to-cart"
	pathCreateCheckout  = "/ecom/v1/carts/current/create-checkout"
	pathCheckouts       = "/ecom/v1/checkouts"
	pathRedirectSession = "/redirect-session/v1/redirect-session"

	userAgent = "UCP-Proxy/1.0"
)

// Client is the Wix API HTTP client.
// Uses OAuth2 with anonymous visitor tokens for authentication.
type Client struct {
	httpClient *http.Client
	clientID   string // OAuth app client ID (no secret needed for anonymous flow)
}

// NewClient creates a new Wix API client.
// clientID is the OAuth app client ID from Wix Headless settings.
func NewClient(clientID string) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport.NewChromeTransport(30 * time.Second),
		},
		clientID: clientID,
	}
}

// === OAuth Session Management ===

// GetAnonymousToken obtains an anonymous visitor access token.
// This token represents a unique visitor session tied to cart/checkout state.
// Tokens are valid for 4 hours (14400 seconds).
func (c *Client) GetAnonymousToken(ctx context.Context) (*OAuthTokenResponse, error) {
	body := &OAuthTokenRequest{
		ClientID:  c.clientID,
		GrantType: "anonymous",
	}

	req, err := c.newTokenRequest(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("creating token request: %w", err)
	}

	var resp OAuthTokenResponse
	if err := c.do(req, &resp); err != nil {
		return nil, fmt.Errorf("getting anonymous token: %w", err)
	}

	if resp.AccessToken == "" {
		return nil, fmt.Errorf("empty access token from OAuth")
	}

	return &resp, nil
}

// RefreshToken refreshes an expired access token.
// Returns new access/refresh token pair.
func (c *Client) RefreshToken(ctx context.Context, refreshToken string) (*OAuthTokenResponse, error) {
	body := &OAuthRefreshRequest{
		ClientID:     c.clientID,
		GrantType:    "refresh_token",
		RefreshToken: refreshToken,
	}

	req, err := c.newTokenRequest(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("creating refresh request: %w", err)
	}

	var resp OAuthTokenResponse
	if err := c.do(req, &resp); err != nil {
		return nil, fmt.Errorf("refreshing token: %w", err)
	}

	return &resp, nil
}

// newTokenRequest creates OAuth token endpoint request.
// Separate from newRequest since token endpoint doesn't need auth header.
func (c *Client) newTokenRequest(ctx context.Context, body interface{}) (*http.Request, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling token request: %w", err)
	}

	url := wixBaseURL + pathOAuthToken
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	return req, nil
}

// === Cart Operations ===

// GetCurrentCart retrieves the current cart for the session.
func (c *Client) GetCurrentCart(ctx context.Context, accessToken string) (*WixCart, error) {
	req, err := c.newRequest(ctx, http.MethodGet, pathCartCurrent, nil, accessToken)
	if err != nil {
		return nil, fmt.Errorf("creating cart request: %w", err)
	}

	var resp WixCartResponse
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}

	return resp.Cart, nil
}

// AddToCart adds line items to the current cart.
// Creates the cart if it doesn't exist.
func (c *Client) AddToCart(ctx context.Context, accessToken string, items []WixLineItemInput) (*WixCart, error) {
	body := &WixAddToCartRequest{
		LineItems: items,
	}

	req, err := c.newRequest(ctx, http.MethodPost, pathAddToCart, body, accessToken)
	if err != nil {
		return nil, fmt.Errorf("creating add-to-cart request: %w", err)
	}

	var resp WixCartResponse
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}

	return resp.Cart, nil
}

// === Checkout Operations ===

// CreateCheckoutFromCart creates a checkout from the current cart.
// Uses the cart associated with the current access token session.
// Returns the full checkout details after creation.
func (c *Client) CreateCheckoutFromCart(ctx context.Context, accessToken string) (*WixCheckout, error) {
	// create-checkout works on current cart (tied to session token)
	body := map[string]string{
		"channelType": "OTHER_PLATFORM",
	}

	req, err := c.newRequest(ctx, http.MethodPost, pathCreateCheckout, body, accessToken)
	if err != nil {
		return nil, fmt.Errorf("creating checkout request: %w", err)
	}

	// create-checkout returns {"checkoutId": "..."} not the full checkout
	var resp struct {
		CheckoutID string `json:"checkoutId"`
	}
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}

	if resp.CheckoutID == "" {
		return nil, fmt.Errorf("empty checkout ID from create-checkout")
	}

	// Fetch the full checkout details
	return c.GetCheckout(ctx, accessToken, resp.CheckoutID)
}

// GetCheckout retrieves a checkout by ID.
func (c *Client) GetCheckout(ctx context.Context, accessToken, checkoutID string) (*WixCheckout, error) {
	path := pathCheckouts + "/" + checkoutID

	req, err := c.newRequest(ctx, http.MethodGet, path, nil, accessToken)
	if err != nil {
		return nil, fmt.Errorf("creating get checkout request: %w", err)
	}

	var resp WixCheckoutResponse
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}

	return resp.Checkout, nil
}

// UpdateCheckout updates a checkout with new information.
// couponCode is optional - pass empty string to skip.
func (c *Client) UpdateCheckout(ctx context.Context, accessToken, checkoutID string, update *WixCheckoutUpdate, couponCode string) (*WixCheckout, error) {
	path := pathCheckouts + "/" + checkoutID

	body := &WixUpdateCheckoutRequest{
		Checkout:   update,
		CouponCode: couponCode,
	}

	req, err := c.newRequest(ctx, http.MethodPatch, path, body, accessToken)
	if err != nil {
		return nil, fmt.Errorf("creating update checkout request: %w", err)
	}

	var resp WixCheckoutResponse
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}

	return resp.Checkout, nil
}

// GetShippingOptions retrieves available shipping options for a checkout.
func (c *Client) GetShippingOptions(ctx context.Context, accessToken, checkoutID string) ([]WixShippingOption, error) {
	path := pathCheckouts + "/" + checkoutID + "/shipping-options"

	req, err := c.newRequest(ctx, http.MethodGet, path, nil, accessToken)
	if err != nil {
		return nil, fmt.Errorf("creating shipping options request: %w", err)
	}

	var resp WixShippingOptionsResponse
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}

	return resp.ShippingOptions, nil
}

// === Checkout Line Item Operations (for reconciliation) ===

// AddToCheckout adds line items to an existing checkout.
// Used by reconciler when client's desired state includes new items.
// POST /ecom/v1/checkouts/{checkoutId}/add-to-checkout
func (c *Client) AddToCheckout(ctx context.Context, accessToken, checkoutID string, items []WixLineItemInput) (*WixCheckout, error) {
	path := pathCheckouts + "/" + checkoutID + "/add-to-checkout"

	body := &WixAddToCheckoutRequest{
		LineItems: items,
	}

	req, err := c.newRequest(ctx, http.MethodPost, path, body, accessToken)
	if err != nil {
		return nil, fmt.Errorf("creating add-to-checkout request: %w", err)
	}

	var resp WixCheckoutResponse
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}

	return resp.Checkout, nil
}

// RemoveLineItems removes line items from checkout by their IDs.
// Used by reconciler when client's desired state excludes current items.
// POST /ecom/v1/checkouts/{checkoutId}/remove-line-items
func (c *Client) RemoveLineItems(ctx context.Context, accessToken, checkoutID string, lineItemIDs []string) (*WixCheckout, error) {
	path := pathCheckouts + "/" + checkoutID + "/remove-line-items"

	body := &WixRemoveLineItemsRequest{
		LineItemIDs: lineItemIDs,
	}

	req, err := c.newRequest(ctx, http.MethodPost, path, body, accessToken)
	if err != nil {
		return nil, fmt.Errorf("creating remove-line-items request: %w", err)
	}

	var resp WixCheckoutResponse
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}

	return resp.Checkout, nil
}

// UpdateLineItemsQuantity updates quantities of existing line items.
// Used by reconciler when client's desired state has different quantities.
// POST /ecom/v1/checkouts/{checkoutId}/update-line-items-quantity
func (c *Client) UpdateLineItemsQuantity(ctx context.Context, accessToken, checkoutID string, updates []WixQuantityUpdate) (*WixCheckout, error) {
	path := pathCheckouts + "/" + checkoutID + "/update-line-items-quantity"

	body := &WixUpdateQuantityRequest{
		LineItems: updates,
	}

	req, err := c.newRequest(ctx, http.MethodPost, path, body, accessToken)
	if err != nil {
		return nil, fmt.Errorf("creating update-line-items-quantity request: %w", err)
	}

	var resp WixCheckoutResponse
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}

	return resp.Checkout, nil
}

// === Redirect Session ===

// CreateRedirectSession creates a redirect session for Wix hosted checkout.
// Returns the full URL where the buyer should be directed to complete payment.
func (c *Client) CreateRedirectSession(ctx context.Context, accessToken, checkoutID string, callbacks *WixCallbacks) (*WixRedirectSession, error) {
	body := &WixCreateRedirectRequest{
		EcomCheckout: &WixEcomCheckoutRef{
			CheckoutID: checkoutID,
		},
		Callbacks: callbacks,
	}

	req, err := c.newRequest(ctx, http.MethodPost, pathRedirectSession, body, accessToken)
	if err != nil {
		return nil, fmt.Errorf("creating redirect session request: %w", err)
	}

	var resp WixRedirectResponse
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}

	return resp.RedirectSession, nil
}

// === HTTP Helpers ===

// newRequest creates an HTTP request with OAuth Bearer token authentication.
// accessToken is required for all API calls (except the token endpoint).
func (c *Client) newRequest(ctx context.Context, method, path string, body interface{}, accessToken string) (*http.Request, error) {
	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshaling request: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	url := wixBaseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", "Bearer "+accessToken)

	return req, nil
}

// do executes the request and decodes the response.
func (c *Client) do(req *http.Request, result interface{}) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return model.NewUpstreamError("Wix", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	// Handle error responses
	if resp.StatusCode >= 400 {
		return c.parseError(resp.StatusCode, body)
	}

	// Decode success response
	if result != nil && len(body) > 0 {
		if err := json.Unmarshal(body, result); err != nil {
			return fmt.Errorf("parsing response: %w", err)
		}
	}

	return nil
}

// parseError converts Wix API errors to model.APIError.
func (c *Client) parseError(statusCode int, body []byte) error {
	var wixErr WixErrorResponse
	json.Unmarshal(body, &wixErr) // Best effort parse

	// Extract error code if available
	code := ""
	if wixErr.Details != nil && wixErr.Details.ApplicationError != nil {
		code = wixErr.Details.ApplicationError.Code
	}

	switch statusCode {
	case 401:
		return model.NewUnauthorizedError("Wix authentication failed")
	case 403:
		return model.NewUnauthorizedError("Wix access denied")
	case 404:
		return model.NewNotFoundError("resource")
	case 429:
		return model.NewRateLimitError("Wix")
	case 400:
		msg := wixErr.Message
		if msg == "" {
			msg = "invalid request"
		}
		// Check for specific validation errors
		if strings.Contains(msg, "coupon") || code == "INVALID_COUPON" {
			return model.NewValidationError("discount_code", msg)
		}
		return model.NewValidationError("request", msg)
	default:
		return model.NewUpstreamError("Wix",
			fmt.Errorf("status %d: %s", statusCode, wixErr.Message))
	}
}
