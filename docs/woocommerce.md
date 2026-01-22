# WooCommerce Adapter

This guide covers setup and configuration for using UCP Proxy with WooCommerce stores.

## Prerequisites

### WooCommerce Store API

The adapter uses the [WooCommerce Store API](https://github.com/woocommerce/woocommerce/blob/trunk/plugins/woocommerce/src/StoreApi/docs/README.md), which is included in WooCommerce 6.9+. For older versions, install the [WooCommerce Blocks](https://wordpress.org/plugins/woo-gutenberg-products-block/) plugin.

Verify the Store API is available:
```bash
curl https://your-store.com/wc/store/v1/cart
# Should return cart JSON (possibly empty)
```

### REST API Credentials

Generate WooCommerce REST API keys:
1. WordPress Admin → WooCommerce → Settings → Advanced → REST API
2. Add Key → Read/Write permissions
3. Save the Consumer Key (`ck_xxx`) and Consumer Secret (`cs_xxx`)

### Stripe Gateway (Optional)

For Google Pay support, install and configure [WooCommerce Stripe Gateway](https://wordpress.org/plugins/woocommerce-gateway-stripe/):
1. WordPress Admin → WooCommerce → Settings → Payments → Stripe
2. Enable "Payment Request Buttons" for Google Pay/Apple Pay
3. Note your Stripe Publishable Key (`pk_xxx`)

## Configuration

### Config File (Recommended)

Create a config file for local development:

```bash
cp config.example.json config.local-woo.json
```

Edit `config.local-woo.json`:
```json
{
  "port": "8080",
  "environment": "development",
  "log_level": "debug",
  "adapter_type": "woocommerce",
  "merchant_id": "my-woo-store",
  "merchant": {
    "store_url": "https://shop.example.com",
    "api_key": "ck_1234567890abcdef",
    "api_secret": "cs_abcdef1234567890",
    "policy_links": {
      "privacy_policy": "https://shop.example.com/privacy",
      "terms_of_service": "https://shop.example.com/terms"
    },
    "payment_handlers": {
      "com.google.pay": [{
        "id": "gpay-checkout",
        "version": "2026-01-11",
        "spec": "https://ucp.dev/handlers/google.pay",
        "config": {
          "allowed_payment_methods": [{
            "type": "CARD",
            "parameters": {
              "allowed_auth_methods": ["PAN_ONLY", "CRYPTOGRAM_3DS"],
              "allowed_card_networks": ["VISA", "MASTERCARD", "AMEX", "DISCOVER"]
            },
            "tokenization_specification": {
              "type": "PAYMENT_GATEWAY",
              "parameters": {
                "gateway": "stripe",
                "stripe:version": "2020-08-27",
                "stripe:publishableKey": "pk_test_xxx"
              }
            }
          }]
        }
      }]
    },
    "escalation": {
      "product_ids": [124, 456],
      "custom_fields": ["_requires_disclaimer", "_age_restricted"]
    }
  }
}
```

Run:
```bash
CONFIG_FILE=config.local-woo.json go run ./cmd/proxy
```

### Environment Variables (Alternative)

| Variable              | Required | Description                                         |
| --------------------- | -------- | --------------------------------------------------- |
| `CONFIG_FILE`         | No       | Path to JSON config file (skips other env vars)     |
| `MERCHANT_ID`         | Yes*     | Unique identifier (used for logging, secret lookup) |
| `ADAPTER_TYPE`        | Yes*     | Must be `woocommerce`                               |
| `MERCHANT_STORE_URL`  | Yes*     | Full URL to WooCommerce store                       |
| `MERCHANT_API_KEY`    | Yes*     | WooCommerce Consumer Key (`ck_xxx`)                 |
| `MERCHANT_API_SECRET` | Yes*     | WooCommerce Consumer Secret (`cs_xxx`)              |
| `MERCHANT_NAME`       | No       | Display name for the merchant                       |
| `POLICY_LINKS`        | No       | JSON object of policy URLs                          |
| `PAYMENT_HANDLERS`    | No       | JSON object of payment handler configs              |

*Not required if using `CONFIG_FILE`.

### Production (GCP Secret Manager)

Store merchant configuration as a JSON secret:

```json
{
  "store_url": "https://shop.example.com",
  "api_key": "ck_live_xxx",
  "api_secret": "cs_live_xxx",
  "merchant_name": "Example Store",
  "policy_links": {
    "privacy_policy": "https://shop.example.com/privacy",
    "terms_of_service": "https://shop.example.com/terms",
    "refund_policy": "https://shop.example.com/refunds"
  },
  "payment_handlers": {
    "com.google.pay": [{
      "id": "gpay-checkout",
      "version": "2026-01-11",
      "spec": "https://ucp.dev/handlers/google.pay",
      "config": {
        "allowed_payment_methods": [{
          "type": "CARD",
          "parameters": {
            "allowed_auth_methods": ["PAN_ONLY", "CRYPTOGRAM_3DS"],
            "allowed_card_networks": ["VISA", "MASTERCARD"]
          },
          "tokenization_specification": {
            "type": "PAYMENT_GATEWAY",
            "parameters": {
              "gateway": "stripe",
              "stripe:publishableKey": "pk_live_xxx"
            }
          }
        }]
      }
    }]
  }
}
```

Create the secret:
```bash
echo '{"store_url":"..."}' | gcloud secrets create my-woo-store --data-file=-
```

Run in production:
```bash
export ENVIRONMENT=production
export GCP_PROJECT=my-gcp-project
export MERCHANT_ID=my-woo-store
export ADAPTER_TYPE=woocommerce

go run ./cmd/proxy
```

## How It Works

### Architecture Overview

```
┌──────────────┐        ┌──────────────┐        ┌──────────────────────┐
│   AI Agent   │  UCP   │  UCP Proxy   │  Store │    WooCommerce       │
│              │◄──────►│              │◄──────►│                      │
│              │        │  WooCommerce │   API  │  ┌────────────────┐  │
│              │        │   Adapter    │        │  │  Store API     │  │
└──────────────┘        └──────────────┘        │  │  /wc/store/v1  │  │
                                                │  └────────────────┘  │
                                                │  ┌────────────────┐  │
                                                │  │ Stripe Plugin  │  │
                                                │  │ (payments)     │  │
                                                │  └────────────────┘  │
                                                └──────────────────────┘
```

### Cart-Token Flow

WooCommerce Store API uses a `Cart-Token` header to maintain cart state without cookies. The proxy encodes this token in the checkout ID, enabling stateless operation.

```
┌─────────┐                    ┌───────────┐                    ┌─────────────┐
│  Agent  │                    │   Proxy   │                    │ WooCommerce │
└────┬────┘                    └─────┬─────┘                    └──────┬──────┘
     │                               │                                 │
     │  create_checkout              │                                 │
     │  {line_items: [...]}          │                                 │
     │──────────────────────────────►│                                 │
     │                               │                                 │
     │                               │  POST /wc/store/v1/cart/add-item
     │                               │─────────────────────────────────►
     │                               │                                 │
     │                               │  Cart-Token: abc123             │
     │                               │◄─────────────────────────────────
     │                               │                                 │
     │                               │  POST /wc/store/v1/checkout     │
     │                               │  Cart-Token: abc123             │
     │                               │─────────────────────────────────►
     │                               │                                 │
     │                               │  {order_id: 456, ...}           │
     │                               │◄─────────────────────────────────
     │                               │                                 │
     │  checkout.id =                │                                 │
     │  "gid://shop.com/456:abc123"  │                                 │
     │◄──────────────────────────────│                                 │
     │                               │                                 │
     │  update_checkout              │                                 │
     │  {id: "gid://.../456:abc123"} │                                 │
     │──────────────────────────────►│                                 │
     │                               │                                 │
     │                               │  (extracts abc123 from ID)      │
     │                               │  POST /wc/store/v1/checkout     │
     │                               │  Cart-Token: abc123             │
     │                               │─────────────────────────────────►
```

The checkout ID format `gid://{domain}/Checkout/{order_id}:{cart_token}` encodes the cart token, so subsequent requests can authenticate with WooCommerce without the proxy maintaining any state.

### WooCommerce Store API Endpoints Used

| Endpoint                                 | Method | Purpose                                |
| ---------------------------------------- | ------ | -------------------------------------- |
| `/wc/store/v1/cart/add-item`             | POST   | Add products to cart                   |
| `/wc/store/v1/cart/apply-coupon`         | POST   | Apply discount codes                   |
| `/wc/store/v1/cart/select-shipping-rate` | POST   | Select shipping method                 |
| `/wc/store/v1/checkout`                  | GET    | Get checkout state                     |
| `/wc/store/v1/checkout`                  | POST   | Create/update checkout, submit payment |

## Payment Handling

### Supported Payment Methods

| Handler      | Description            | Requirements                    |
| ------------ | ---------------------- | ------------------------------- |
| `google.pay` | Google Pay via Stripe  | Stripe plugin + publishable key |
| `redirect`   | Browser-based fallback | None (always available)         |

### Google Pay Flow (Stripe)

When an agent provides a Stripe PaymentMethod token from Google Pay:

```json
{
  "payment": {
    "selected_instrument_id": "instr_1",
    "instruments": [{
      "id": "instr_1",
      "handler_id": "google.pay",
      "type": "card",
      "credential": {
        "type": "stripe.payment_method",
        "token": "pm_1234567890"
      }
    }]
  }
}
```

The adapter passes this to WooCommerce Stripe:
```json
{
  "payment_method": "stripe",
  "payment_data": [
    {"key": "wc-stripe-payment-method", "value": "pm_1234567890"},
    {"key": "wc-stripe-is-deferred-intent", "value": "true"}
  ]
}
```

### 3D Secure Handling

When 3DS authentication is required, WooCommerce returns a redirect URL:

```json
{
  "payment_result": {
    "payment_status": "pending",
    "redirect_url": "https://hooks.stripe.com/3d_secure/..."
  }
}
```

The proxy returns this as an escalation:
```json
{
  "status": "requires_escalation",
  "continue_url": "https://hooks.stripe.com/3d_secure/...",
  "messages": [{
    "type": "info",
    "code": "3DS_REQUIRED",
    "content": "Payment requires 3D Secure authentication"
  }]
}
```

The agent should present `continue_url` to the user for completing authentication in a browser.

### Product Escalation

Some products require browser checkout due to legal disclaimers, age verification, or custom input fields. Configure escalation triggers in the merchant config:

```json
{
  "escalation": {
    "product_ids": [124, 456],
    "custom_fields": ["_requires_disclaimer", "_age_restricted"]
  }
}
```

| Field | Description |
|-------|-------------|
| `product_ids` | Product IDs that always require browser checkout |
| `custom_fields` | Meta keys that trigger escalation when present on cart item |

When a cart contains matching products:
- **Create/Get/Update**: Returns `status: requires_escalation` with full cart data (informational)
- **Complete**: Blocks payment processing, returns escalation response

```json
{
  "status": "requires_escalation",
  "continue_url": "https://shop.example.com/checkout-link/?products=124:1",
  "messages": [{
    "type": "error",
    "code": "ESCALATION_REQUIRED",
    "content": "Products require additional buyer input: Custom Map. Complete checkout in browser.",
    "severity": "escalation"
  }]
}
```

The `continue_url` uses WooCommerce's shareable checkout URL format, pre-populating the cart with the same products.

**Note**: `custom_fields` requires the merchant to expose product meta via the `woocommerce_store_api_add_to_cart_data` PHP filter. `product_ids` works without any merchant-side changes.

### Redirect Fallback

If the payment method isn't supported or the agent requests the `redirect` handler, the checkout returns:

```json
{
  "status": "requires_escalation",
  "continue_url": "https://shop.example.com/checkout/order-pay/456/?key=wc_order_xxx",
  "messages": [{
    "type": "info",
    "code": "ESCALATION_REQUIRED",
    "content": "Payment requires browser checkout"
  }]
}
```

## Troubleshooting

### Authentication Errors

**Symptom**: `401 Unauthorized` or `403 Forbidden`

**Causes**:
1. Invalid API keys - verify `ck_` and `cs_` values
2. Wrong permissions - keys need Read/Write access
3. HTTPS required - WooCommerce may require HTTPS for API access

**Test credentials**:
```bash
curl -u ck_xxx:cs_xxx https://your-store.com/wc/store/v1/cart
```

### Cart-Token Issues

**Symptom**: `checkout not found` on get/update operations

**Causes**:
1. Cart expired - WooCommerce carts expire after ~48 hours
2. Cart cleared - user may have completed checkout via web
3. Token invalid - malformed checkout ID

**Debug**: Check if the cart token is being extracted correctly from the checkout ID.

### Shipping Errors

**Symptom**: Shipping options not appearing or selection fails

**Causes**:
1. Shipping zones not configured for the address
2. Product marked as "virtual" (no shipping required)
3. Missing or invalid shipping address

**Check WooCommerce**:
- WooCommerce → Settings → Shipping → Shipping zones
- Ensure zones cover the target countries

### Payment Errors

**Symptom**: Payment fails with upstream error

**Causes**:
1. Stripe not configured as payment gateway
2. Payment method token expired
3. Card declined by Stripe

**Check WooCommerce**:
- WooCommerce → Settings → Payments → Stripe → Enable
- Enable "Payment Request Buttons" for Google Pay

### Store API Not Found

**Symptom**: `404 Not Found` on `/wc/store/v1/*`

**Causes**:
1. WooCommerce version < 6.9 without Blocks plugin
2. Permalinks set to "Plain" (breaks REST API routing)

**Fix**:
- Update WooCommerce, or install WooCommerce Blocks
- WordPress → Settings → Permalinks → any option except "Plain"

## API Mapping Reference

### UCP → WooCommerce Field Mapping

| UCP Field                 | WooCommerce Field                        |
| ------------------------- | ---------------------------------------- |
| `line_items[].item.id`    | `items[].id`                             |
| `line_items[].item.title` | `items[].name`                           |
| `line_items[].item.price` | `items[].prices.price` (parsed to cents) |
| `totals[].amount`         | `totals.total_*` (parsed to cents)       |
| `shipping_address`        | `shipping_address`                       |
| `billing_address`         | `billing_address`                        |
| `buyer.email`             | `billing_address.email`                  |

### Status Mapping

| WooCommerce Status | UCP Status              |
| ------------------ | ----------------------- |
| `checkout-draft`   | `not_ready_for_payment` |
| `pending`          | `ready_for_payment`     |
| `processing`       | `completed`             |
| `on-hold`          | `requires_escalation`   |
| `completed`        | `completed`             |
| `cancelled`        | `canceled`              |
| `failed`           | `unrecoverable_errors`  |
