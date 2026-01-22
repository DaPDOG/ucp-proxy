# Wix Adapter

This guide covers setup and configuration for using UCP Proxy with Wix eCommerce stores.

## Overview

The Wix adapter builds checkout via API. Payment currently completes on Wix's hosted checkout page (escalation flow). Programmatic payment completion to be added.

```
Agent builds checkout → status: requires_escalation → Buyer completes payment in browser
```

## Prerequisites

### Wix Headless OAuth App

1. Create or access a Wix site with eCommerce enabled
2. Go to **Developer Tools** → **OAuth Apps** → **Create New**
3. Create an OAuth app with these permissions:
   - `wix.ecom.cart.read`
   - `wix.ecom.cart.update`
   - `wix.ecom.checkout.read`
   - `wix.ecom.checkout.update`
4. Copy the **Client ID** (no secret needed for anonymous visitor flow)

### Products

Ensure your store has published products. The adapter uses Wix catalog product IDs (UUIDs like `36e83a27-b544-4408-aff6-e0c72cf04233`).

### Shipping Zones

Configure shipping in **Store Settings** → **Shipping & Fulfillment**:
- Add shipping regions that cover your target countries
- Set shipping rates for each region

## Configuration

### Config File (Recommended)

Create a config file for local development:

```json
{
  "port": "8080",
  "environment": "development",
  "log_level": "debug",
  "adapter_type": "wix",
  "merchant_id": "my-wix-store",
  "merchant": {
    "store_url": "https://your-site.wixsite.com/your-store",
    "store_domain": "your-site.wixsite.com",
    "merchant_name": "My Store",
    "wix_client_id": "your-oauth-client-id",
    "policy_links": {
      "privacy_policy": "https://your-site.wixsite.com/your-store/privacy",
      "terms_of_service": "https://your-site.wixsite.com/your-store/terms"
    }
  }
}
```

Run:
```bash
CONFIG_FILE=config.wix.local.json go run ./cmd/proxy
```

### Environment Variables (Alternative)

| Variable                | Required | Description                                     |
| ----------------------- | -------- | ----------------------------------------------- |
| `CONFIG_FILE`           | No       | Path to JSON config file (skips other env vars) |
| `MERCHANT_ID`           | Yes*     | Unique identifier (used for logging)            |
| `ADAPTER_TYPE`          | Yes*     | Must be `wix`                                   |
| `MERCHANT_STORE_URL`    | Yes*     | Full URL to Wix store                           |
| `MERCHANT_STORE_DOMAIN` | Yes*     | Store domain (e.g., `your-site.wixsite.com`)    |
| `WIX_CLIENT_ID`         | Yes*     | OAuth app client ID                             |
| `MERCHANT_NAME`         | No       | Display name for the merchant                   |

*Not required if using `CONFIG_FILE`.

### Production (GCP Secret Manager)

Store merchant configuration as a JSON secret:

```json
{
  "store_url": "https://your-site.wixsite.com/your-store",
  "store_domain": "your-site.wixsite.com",
  "wix_client_id": "your-oauth-client-id",
  "merchant_name": "My Store",
  "policy_links": {
    "privacy_policy": "https://your-site.wixsite.com/your-store/privacy"
  }
}
```

Create the secret:
```bash
echo '{"store_url":"..."}' | gcloud secrets create my-wix-store --data-file=-
```

## How It Works

### Architecture Overview

```
┌──────────────┐        ┌──────────────┐        ┌──────────────────────┐
│   AI Agent   │  UCP   │  UCP Proxy   │  Wix   │    Wix Platform      │
│              │◄──────►│              │◄──────►│                      │
│              │        │  Wix         │  API   │  ┌────────────────┐  │
│              │        │  Adapter     │        │  │ eCommerce API  │  │
└──────────────┘        └──────────────┘        │  │ /v1/checkout   │  │
                                                │  └────────────────┘  │
                                                │  ┌────────────────┐  │
                                                │  │ Hosted Checkout│  │
                                                │  │ (payment)      │  │
                                                │  └────────────────┘  │
                                                └──────────────────────┘
```

### OAuth Anonymous Flow

Wix Headless uses anonymous visitor tokens for cart/checkout operations:

```
┌─────────┐                    ┌───────────┐                    ┌─────────────┐
│  Agent  │                    │   Proxy   │                    │     Wix     │
└────┬────┘                    └─────┬─────┘                    └──────┬──────┘
     │                               │                                 │
     │  create_checkout              │                                 │
     │  {line_items: [...]}          │                                 │
     │──────────────────────────────►│                                 │
     │                               │                                 │
     │                               │  POST /oauth2/token             │
     │                               │  {clientId, grantType:anonymous}│
     │                               │─────────────────────────────────►
     │                               │                                 │
     │                               │  {access_token: "abc123"}       │
     │                               │◄─────────────────────────────────
     │                               │                                 │
     │                               │  POST /v1/carts                 │
     │                               │  Authorization: Bearer abc123   │
     │                               │─────────────────────────────────►
     │                               │                                 │
     │                               │  {cart: {id: "cart_xyz"}}       │
     │                               │◄─────────────────────────────────
     │                               │                                 │
     │                               │  POST /v1/checkouts             │
     │                               │  {cartId: "cart_xyz"}           │
     │                               │─────────────────────────────────►
     │                               │                                 │
     │  checkout.id =                │  {checkout: {id: "chk_456"}}    │
     │  "gid://wix.domain/chk_456:abc123"                              │
     │◄──────────────────────────────│◄─────────────────────────────────
```

The OAuth token is embedded in the checkout ID for stateless operation.

### Checkout ID Format

```
gid://wix.{site_domain}/Checkout/{checkout_id}:{session_token}
```

Example:
```
gid://wix.your-site.wixsite.com/Checkout/abc123:eyJhbGciOiJSUzI1NiIs...
```

This encoding allows the proxy to authenticate subsequent requests without storing session state.

### Security Model: Session-Scoped Tokens

The token embedded in the checkout ID is a **session-scoped secret key** for this specific cart/checkout. It functions like a capability token with intentionally limited scope:

**What the token CAN do:**
- Read/modify the cart it created
- Read/modify the checkout it created
- Create additional carts under this session

**What the token CANNOT do:**
- Access other visitors' carts or checkouts
- Access any user account data
- Perform admin operations on the store

**Token properties:**
- Expires after 4 hours
- Tied to anonymous visitor session (not a user account)
- Scoped to eCommerce cart/checkout permissions only

**Implications:**
- Checkout IDs should be treated as sensitive (they contain the session key)
- Leaking a checkout ID allows modification of *that* checkout only
- No access to other users' data or broader store resources
- Similar security model to cart session tokens in traditional e-commerce

This design enables stateless proxy operation while maintaining security through scope limitation rather than token secrecy.

### Wix API Endpoints Used

| Endpoint                            | Method | Purpose                             |
| ----------------------------------- | ------ | ----------------------------------- |
| `/oauth2/token`                     | POST   | Get anonymous visitor token         |
| `/ecom/v1/carts`                    | POST   | Create cart                         |
| `/ecom/v1/carts/{id}/addToCart`     | POST   | Add items to cart                   |
| `/ecom/v1/checkouts`                | POST   | Create checkout from cart           |
| `/ecom/v1/checkouts/{id}`           | GET    | Get checkout state                  |
| `/ecom/v1/checkouts/{id}`           | PATCH  | Update checkout (address, shipping) |
| `/redirects-api/v1/redirectSession` | POST   | Create redirect session for payment |

## Payment Handling

### Browser Handoff (Current Implementation)

The Wix adapter returns `requires_escalation` when checkout is ready for payment. Programmatic payment completion to be added.

```json
{
  "status": "requires_escalation",
  "continue_url": "https://www.wix.com/checkout/...",
  "messages": [{
    "type": "error",
    "code": "PAYMENT_HANDOFF",
    "content": "Checkout is ready. Please complete payment on the merchant checkout page.",
    "severity": "escalation"
  }]
}
```

The agent should present the `continue_url` to the buyer. The URL opens a Wix-hosted checkout page with:
- Cart items pre-populated
- Shipping address filled in
- Shipping option selected
- Only payment step remaining

### Why Escalation?

Current implementation uses browser handoff because Wix's payment processing requires:
1. Browser-based payment method selection
2. Client-side payment gateway integration
3. Wix's checkout completion flow

Programmatic payment completion is planned for a future release.

### Payment Handlers

| Handler    | Description                | Behavior         |
| ---------- | -------------------------- | ---------------- |
| `redirect` | Browser-based Wix checkout | Always escalates |

## Status Transitions

```
┌────────────┐      ┌────────────┐      ┌─────────────────────┐
│ incomplete │ ───► │ incomplete │ ───► │ requires_escalation │
│ (no items) │      │ (building) │      │   (ready, no pay)   │
└────────────┘      └────────────┘      └─────────────────────┘
                                                  │
                                                  ▼
                                        Browser completes payment
                                                  │
                                                  ▼
                                        ┌─────────────────────┐
                                        │     completed       │
                                        │ (order in Wix)      │
                                        └─────────────────────┘
```

The proxy determines status based on checkout completeness:
- Missing line items → `incomplete`
- Missing buyer email → `incomplete`
- Missing shipping address (for physical items) → `incomplete`
- Missing shipping selection → `incomplete`
- All non-payment fields present → `requires_escalation`

## Troubleshooting

### Authentication Errors

**Symptom**: `401 Unauthorized` or token errors

**Causes**:
1. Invalid OAuth client ID
2. Missing eCommerce permissions on OAuth app
3. Token expired (4-hour lifetime)

**Fix**: Verify client ID and permissions in Wix Developer Tools → OAuth Apps.

### "Adding items to cart" Error

**Symptom**: Cart creation fails

**Causes**:
1. Invalid product ID (not in catalog)
2. Product not published
3. Product out of stock

**Debug**: Use Wix API directly to verify product exists:
```bash
curl -s "https://www.wixapis.com/stores/v1/products/$PRODUCT_ID" \
  -H "Authorization: Bearer $TOKEN"
```

### No Shipping Options

**Symptom**: `fulfillment_options` empty after adding address

**Causes**:
1. No shipping zones cover the address country/region
2. Products marked as digital/download-only
3. Store shipping not configured

**Fix**: Check **Store Settings** → **Shipping & Fulfillment** in Wix dashboard.

### Invalid Coupon

**Symptom**: `INVALID_COUPON` error on discount code

**Causes**:
1. Coupon expired or inactive
2. Order doesn't meet minimum purchase
3. Coupon usage limit reached
4. Coupon not applicable to products in cart

**Fix**: Verify coupon in **Marketing & SEO** → **Coupons**.

### Checkout ID Parse Error

**Symptom**: `invalid Wix checkout ID format`

**Causes**:
1. Malformed gid:// URL
2. Missing or corrupted access token
3. Using wrong adapter for checkout ID

**Debug**: Checkout ID should match pattern:
```
gid://wix.{domain}/Checkout/{id}:{token}
```

## API Mapping Reference

For detailed field mappings, see `internal/wix/transform.go`. Key gotchas:

**Address format** — Wix uses `addressLine` (not `streetAddress`) and `subdivision` in `"{COUNTRY}-{STATE}"` format (e.g., `"US-CA"` not `"CA"`).

**Status mapping:**
- Missing required fields → `incomplete`
- All fields present, no payment → `requires_escalation`
- After browser completion → `completed`
