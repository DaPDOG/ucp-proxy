# WooCommerce Testing Guide

This guide covers testing the UCP Proxy with a WooCommerce store using Stripe and Braintree in test mode.

> **Prerequisites**: See [woocommerce.md](woocommerce.md) for store setup, configuration, and architecture.

## Quick Start with ucpclient

The fastest way to test is using the `ucpclient` CLI tool:

```bash
# Build ucpclient
make build

# Set your product ID (find in WooCommerce → Products, hover to see ID)
PRODUCT_ID=60

# Run a basic Stripe checkout flow (interactive mode)
./bin/ucpclient -product $PRODUCT_ID

# Run with Braintree instead
./bin/ucpclient -product $PRODUCT_ID -payment braintree

# CI/automation mode (no prompts, minimal output)
./bin/ucpclient -product $PRODUCT_ID -auto -quiet

# Full options: coupon, custom quantity, custom email
./bin/ucpclient -product $PRODUCT_ID -coupon SAVE10 -qty 2 -email buyer@example.com
```

### ucpclient Options

| Flag        | Default                 | Description                                   |
| ----------- | ----------------------- | --------------------------------------------- |
| `-product`  | (required)              | Product ID to add to cart                     |
| `-qty`      | `1`                     | Quantity of product to add                    |
| `-payment`  | `stripe`                | Payment handler: `stripe` or `braintree`      |
| `-email`    | `test@example.com`      | Buyer email address                           |
| `-coupon`   | (empty)                 | Discount code to apply                        |
| `-proxy`    | `http://localhost:8080` | UCP proxy base URL                            |
| `-auto`     | `false`                 | Auto-advance without interactive prompts      |
| `-quiet`    | `false`                 | Minimal output (errors and final result only) |
| `-no-color` | `false`                 | Disable colored output                        |

The tool also respects the `NO_COLOR` environment variable per [no-color.org](https://no-color.org/).

### ucpclient Checkout Flow

The client executes a complete UCP checkout flow:

1. **Create checkout** - POST `/checkout-sessions` with product
2. **Add addresses** - PUT with buyer, shipping, and billing addresses
3. **Get checkout** - GET to retrieve fulfillment options
4. **Select shipping** - PUT with `fulfillment_option_id`
5. **Apply discount** - PUT with `discount_code` (if `-coupon` provided)
6. **Complete payment** - POST `/complete` with payment instrument

## Prerequisites

### Stripe Test Mode Setup

Your WooCommerce store needs Stripe configured in test mode:

1. Get test API keys from [Stripe Dashboard](https://dashboard.stripe.com/test/apikeys) → Developers → API keys
   - Publishable key: `pk_test_...`
   - Secret key: `sk_test_...`

2. Configure in WordPress Admin → WooCommerce → Settings → Payments → Stripe:
   - [x] Enable Test Mode
   - [x] Test Publishable Key
   - [x] Test Secret Key

**Note:** In test mode, no Google Pay merchant ID is required. Stripe handles tokenization and accepts special test payment method tokens.

### Braintree Test Mode Setup

To enable Braintree payments:

1. Create sandbox account at [Braintree Sandbox](https://sandbox.braintreegateway.com)
2. Go to Settings → API → Generate New Tokenization Key
3. Get the tokenization key (format: `sandbox_xxx_merchantid`)
4. Install and activate [WooCommerce Braintree for PayPal](https://wordpress.org/plugins/woo-payment-gateway/) plugin
5. Configure in WordPress Admin → WooCommerce → Settings → Payments → Braintree Credit Card:
   - [x] Enable Braintree Credit Card
   - [x] Sandbox mode
   - Enter Merchant ID, Public Key, Private Key from dashboard

### Proxy Configuration

See [woocommerce.md](woocommerce.md#configuration) for full config options. For testing, configure payment handlers with test-mode credentials:

```json
{
  "merchant": {
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
                "stripe:publishableKey": "pk_test_..."
              }
            }
          }]
        }
      }],
      "com.braintreepayments": [{
        "id": "braintree-card",
        "version": "2026-01-11",
        "spec": "https://ucp.dev/handlers/braintree.card",
        "config": {
          "tokenization_key": "sandbox_xxx_merchantid",
          "environment": "sandbox"
        }
      }]
    }
  }
}
```

Payment handlers are PSP-agnostic configs passed through to agents. The proxy routes payments based on `credential.type` (e.g., `stripe.payment_method`, `braintree.nonce`).

## Running the Proxy

```bash
CONFIG_FILE=config.local-woo.json go run ./cmd/proxy
```

## Test Payment Credentials

### Stripe Test Tokens

| Token                                     | Card                   | Behavior                      |
| ----------------------------------------- | ---------------------- | ----------------------------- |
| `pm_card_visa`                            | Visa 4242...4242       | Succeeds                      |
| `pm_card_visa_debit`                      | Visa Debit             | Succeeds                      |
| `pm_card_mastercard`                      | Mastercard 5555...4444 | Succeeds                      |
| `pm_card_amex`                            | Amex 3782...0005       | Succeeds                      |
| `pm_card_authenticationRequired`          | -                      | Triggers 3DS (escalation)     |
| `pm_card_chargeDeclined`                  | -                      | Declines with error           |
| `pm_card_chargeDeclinedInsufficientFunds` | -                      | Declines (insufficient funds) |

See [Stripe Testing Documentation](https://docs.stripe.com/testing) for the full list.

### Braintree Test Nonces

| Nonce                                | Behavior              |
| ------------------------------------ | --------------------- |
| `fake-valid-nonce`                   | Succeeds              |
| `fake-valid-visa-nonce`              | Succeeds (Visa)       |
| `fake-valid-mastercard-nonce`        | Succeeds (Mastercard) |
| `fake-valid-amex-nonce`              | Succeeds (Amex)       |
| `fake-processor-declined-visa-nonce` | Declines              |
| `fake-luhn-invalid-nonce`            | Fails validation      |
| `fake-consumed-nonce`                | Fails (already used)  |

See [Braintree Testing Documentation](https://developer.paypal.com/braintree/docs/reference/general/testing) for the full list.

## Manual Testing with curl

### Create Checkout

Find a product ID from your WooCommerce store (WooCommerce → Products, hover to see ID).

```bash
PRODUCT_ID=123  # Replace with your product ID

curl -X POST http://localhost:8080/checkout-sessions \
  -H "Content-Type: application/json" \
  -d @- <<EOF | jq .
{
  "line_items": [{"product_id": "$PRODUCT_ID", "quantity": 1}]
}
EOF
```

Save the returned `id` (e.g., `gid://your-store.com/Checkout/456:abc123`).

### Add Shipping Address

```bash
CHECKOUT_ID="gid://your-store.com/Checkout/456:abc123"

curl -X PUT "http://localhost:8080/checkout-sessions/${CHECKOUT_ID}" \
  -H "Content-Type: application/json" \
  -d '{
    "shipping_address": {
      "first_name": "Test",
      "last_name": "Buyer",
      "street_address": "150 Elgin Street",
      "address_locality": "Ottawa",
      "address_region": "ON",
      "postal_code": "K2P 1L4",
      "address_country": "CA"
    },
    "buyer": {
      "email": "test@example.com"
    }
  }' | jq .
```

### Complete with Stripe

```bash
curl -X POST "http://localhost:8080/checkout-sessions/${CHECKOUT_ID}/complete" \
  -H "Content-Type: application/json" \
  -d '{
    "payment": {
      "selected_instrument_id": "instr_1",
      "instruments": [{
        "id": "instr_1",
        "handler_id": "google.pay",
        "type": "card",
        "credential": {
          "type": "stripe.payment_method",
          "token": "pm_card_visa"
        }
      }]
    }
  }' | jq .
```

### Complete with Braintree

```bash
curl -X POST "http://localhost:8080/checkout-sessions/${CHECKOUT_ID}/complete" \
  -H "Content-Type: application/json" \
  -d '{
    "payment": {
      "selected_instrument_id": "instr_1",
      "instruments": [{
        "id": "instr_1",
        "handler_id": "braintree.card",
        "type": "card",
        "credential": {
          "type": "braintree.nonce",
          "token": "fake-valid-nonce"
        }
      }]
    }
  }' | jq .
```

## Expected Responses

### Successful Payment

```json
{
  "status": "completed",
  "id": "gid://your-store.com/Checkout/456:abc123",
  "order_id": "123",
  "order_permalink_url": "https://your-store.com/checkout/order-received/123",
  "currency": "USD",
  "totals": [
    {"type": "total", "amount": 2999}
  ]
}
```

### 3D Secure Required

Using `pm_card_authenticationRequired`:

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

The agent should present `continue_url` to the user for browser authentication.

### Payment Declined

Using `pm_card_chargeDeclined`:

```json
{
  "status": "unrecoverable_errors",
  "messages": [{
    "type": "error",
    "code": "PAYMENT_FAILED",
    "content": "Your card was declined"
  }]
}
```

## Verifying in Payment Dashboards

### Stripe Dashboard

1. Go to [Stripe Dashboard](https://dashboard.stripe.com/test/payments)
2. Ensure test mode toggle is ON (top-right)
3. Find your payment with "Succeeded" status
4. Click to view payment details, including metadata

### Braintree Dashboard

1. Go to [Braintree Sandbox](https://sandbox.braintreegateway.com)
2. Navigate to Transactions
3. Find the transaction by amount or time
4. View transaction details and settlement status

## Testing with MCP Transport

The proxy also supports MCP (Model Context Protocol) for AI agent integration:

```bash
# Initialize MCP session
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "initialize",
    "params": {
      "protocolVersion": "2026-01-11",
      "clientInfo": {"name": "test", "version": "1.0"},
      "capabilities": {}
    }
  }'

# List available tools
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 2,
    "method": "tools/list",
    "params": {}
  }'

# Call create_checkout tool
PRODUCT_ID=123  # Replace with your product ID

curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d @- <<EOF
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "tools/call",
  "params": {
    "name": "create_checkout",
    "arguments": {
      "line_items": [{"product_id": "$PRODUCT_ID", "quantity": 1}]
    }
  }
}
EOF
```

## Troubleshooting

See [woocommerce.md](woocommerce.md#troubleshooting) for detailed troubleshooting. Common test-mode issues:

| Issue | Likely Cause |
|-------|--------------|
| "checkout not found" | Cart expired (~48h) — create new checkout |
| "401 Unauthorized" | Invalid API keys or wrong permissions |
| Payment upstream error | Test mode not enabled, or wrong test keys |
| No shipping options | Shipping zones don't cover test address |
| Braintree fails | Tokenization key mismatch or plugin not configured |

## Notes

- Test tokens like `pm_card_visa` and `fake-valid-nonce` bypass actual wallet flows
- For full Google Pay testing, use a real Android device with a test card in Google Wallet
- Test mode charges don't result in real money movement
- Switch to live keys and disable test mode for production
