# Wix Testing Guide

This guide covers testing the UCP Proxy with a Wix eCommerce store.

> **Prerequisites**: See [wix.md](wix.md) for OAuth setup, configuration, and architecture.

## Quick Start with ucpclient

The fastest way to test is using the `ucpclient` CLI tool:

```bash
# Build ucpclient
make build

# Start proxy with Wix config
CONFIG_FILE=config.wix.local.json go run ./cmd/proxy

# Set your product ID (see "Finding Product IDs" below)
PRODUCT_ID=36e83a27-b544-4408-aff6-e0c72cf04233

# In another terminal - run a checkout flow
./bin/ucpclient -product $PRODUCT_ID

# With coupon code
./bin/ucpclient -product $PRODUCT_ID -coupon 10OFF

# CI/automation mode (no prompts, minimal output)
./bin/ucpclient -product $PRODUCT_ID -coupon 10OFF -auto -quiet
```

### ucpclient Options

| Flag        | Default                 | Description                                   |
| ----------- | ----------------------- | --------------------------------------------- |
| `-product`  | (required)              | Wix product catalog ID                        |
| `-qty`      | `1`                     | Quantity of product to add                    |
| `-email`    | `test@example.com`      | Buyer email address                           |
| `-coupon`   | (empty)                 | Discount code to apply                        |
| `-proxy`    | `http://localhost:8080` | UCP proxy base URL                            |
| `-auto`     | `false`                 | Auto-advance without interactive prompts      |
| `-quiet`    | `false`                 | Minimal output (errors and final result only) |
| `-no-color` | `false`                 | Disable colored output                        |
| `-payment`  | `stripe`                | Payment handler (ignored - always escalates) |

### Expected Flow

Payment completes via browser handoff, so the flow ends with `requires_escalation`:

1. **Create checkout** - POST `/checkout-sessions` with product
2. **Add addresses** - PUT with buyer, shipping, and billing addresses
3. **Get checkout** - GET to retrieve fulfillment options
4. **Select shipping** - PUT with `fulfillment_option_id`
5. **Apply discount** - PUT with `discount_code` (if `-coupon` provided)
6. **Complete payment** - Returns `requires_escalation` with `continue_url`

The `continue_url` opens Wix's hosted checkout page where the buyer completes payment.

## Finding Product IDs

1. Go to **Store Products** in your Wix dashboard
2. Click on a product
3. The product ID is in the URL: `product/{PRODUCT_ID}/edit`

Or use the Wix API to list products:
```bash
# Get OAuth token first
TOKEN=$(curl -s -X POST "https://www.wixapis.com/oauth2/token" \
  -H "Content-Type: application/json" \
  -d '{"clientId":"YOUR_CLIENT_ID","grantType":"anonymous"}' \
  | jq -r '.access_token')

# List products
curl -s "https://www.wixapis.com/stores/v1/products/query" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"query":{}}' | jq '.products[] | {id, name}'
```

## Proxy Configuration

See [wix.md](wix.md#configuration) for full config options.

## Running the Proxy

```bash
CONFIG_FILE=config.wix.local.json go run ./cmd/proxy
```

## Manual Testing with curl

### Create Checkout

```bash
PRODUCT_ID=36e83a27-b544-4408-aff6-e0c72cf04233  # Replace with your product ID

curl -X POST http://localhost:8080/checkout-sessions \
  -H "Content-Type: application/json" \
  -d @- <<EOF | jq .
{
  "line_items": [{"product_id": "$PRODUCT_ID", "quantity": 1}]
}
EOF
```

Save the returned `id` for subsequent requests.

### Add Shipping Address

```bash
CHECKOUT_ID="gid://wix.your-site.wixsite.com/Checkout/abc123:token..."

curl -X PUT "http://localhost:8080/checkout-sessions/$CHECKOUT_ID" \
  -H "Content-Type: application/json" \
  -d '{
    "buyer": {
      "email": "test@example.com",
      "first_name": "Jane",
      "last_name": "Forager",
      "phone_number": "+14155551234"
    },
    "shipping_address": {
      "first_name": "Jane",
      "last_name": "Forager",
      "street_address": "123 Forest Lane",
      "address_locality": "Portland",
      "address_region": "OR",
      "postal_code": "97201",
      "address_country": "US"
    }
  }' | jq .
```

### Select Shipping Option

Get available options from the previous response's `fulfillment_options`, then:

```bash
SHIPPING_OPTION_ID=b6408cd3-c8f0-48f1-92f6-bd4b7cedf9a4  # From fulfillment_options[].id

curl -X PUT "http://localhost:8080/checkout-sessions/$CHECKOUT_ID" \
  -H "Content-Type: application/json" \
  -d @- <<EOF | jq .
{"fulfillment_option_id": "$SHIPPING_OPTION_ID"}
EOF
```

### Apply Discount Code

```bash
curl -X PUT "http://localhost:8080/checkout-sessions/$CHECKOUT_ID" \
  -H "Content-Type: application/json" \
  -d '{"discount_code": "10OFF"}' | jq .
```

### Get Final Checkout State

```bash
curl "http://localhost:8080/checkout-sessions/$CHECKOUT_ID" | jq .
```

## Expected Responses

### Checkout with Escalation

When all required fields are present (buyer, address, shipping selected):

```json
{
  "id": "gid://wix.your-site.wixsite.com/Checkout/abc123:token...",
  "status": "requires_escalation",
  "continue_url": "https://www.wix.com/checkout/...",
  "currency": "USD",
  "line_items": [
    {
      "id": "00000000-0000-0000-0000-000000000001",
      "item": {"id": "<product_id>", "title": "Nautical Chart", "price": 8500},
      "quantity": 1,
      "subtotal": 8500
    }
  ],
  "totals": [
    {"type": "subtotal", "amount": 8500},
    {"type": "discount", "amount": 1000},
    {"type": "fulfillment", "amount": 1500},
    {"type": "total", "amount": 9000}
  ],
  "messages": [
    {
      "type": "error",
      "code": "PAYMENT_HANDOFF",
      "content": "Checkout is ready. Please complete payment on the merchant checkout page.",
      "severity": "escalation"
    }
  ]
}
```

The agent should present the `continue_url` to the buyer to complete payment on Wix's hosted checkout page.

## Discount Codes

Create discount codes in your Wix dashboard:
1. Go to **Marketing & SEO** → **Coupons**
2. Create a coupon with code like `10OFF` for $10 off
3. Set minimum order amount if desired

## Troubleshooting

See [wix.md](wix.md#troubleshooting) for detailed troubleshooting. Common issues:

| Issue | Likely Cause |
|-------|--------------|
| "adding items to cart" error | Invalid product ID, unpublished, or out of stock |
| No shipping options | Shipping zones don't cover address, or digital-only products |
| "INVALID_COUPON" | Coupon expired, usage limit reached, or minimum not met |
| OAuth token errors | Token expired (4h lifetime) — create new checkout |

## Notes

- Current implementation always returns `requires_escalation` — payment completes on Wix's hosted page
- The `continue_url` opens a pre-filled checkout page on Wix's domain
- Each checkout creates a fresh OAuth token embedded in the checkout ID
