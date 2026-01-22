# UCP Proxy

A stateless proxy that enables AI agents to complete purchases on your e-commerce store via the [Universal Commerce Protocol (UCP)](https://ucp.dev). Your existing store remains the system-of-work and system-of-truth for products, inventory, shipping, discounting, ..., taxes, and everything else. The UCP proxy acts as a transparent translation layer, allowing agents to interact with your existing store via UCP protocol.

```
┌─────────────────┐         ┌───────────────────┐         ┌─────────────────┐
│    AI Agent     │   UCP   │    UCP Proxy      │ Native  │   E-commerce    │
│                 │ ◄─────► │                   │ ◄─────► │    Platform     │
│  Google Gemini, │  REST   │  - Stateless      │   API   │                 │
│  custom agent   │   or    │  - Per-tenant     │         │  WooCommerce    │
│                 │   MCP   │  - Multi-platform │         │  Wix, <custom>  │
└─────────────────┘         └───────────────────┘         └─────────────────┘
```

## How It Works

The proxy exposes a UCP checkout API that agents use to build and complete orders, e.g.:

1. Agent: `create_checkout({ line_items, buyer, shipping_address })`
   - Proxy:  Creates checkout on platform, returns checkout state
2. Agent: `update_checkout({ fulfillment_option_id, discount_code })`
   - Proxy:  Selects shipping option, applies coupon, recalculates totals
3. Agent: `complete_checkout({ payment: { instrument, credential } })`
   - Proxy:  Submits payment via platform's payment handler (Braintree, Stripe, etc.)
   - Result: `completed | requires_escalation`

If [escalation](https://ucp.dev/specification/checkout/#overview:~:text=for%20physical%20goods.-,Checkout%20Status%20Lifecycle,-The%20checkout%20status) is required, the proxy returns a `continue_url` to the buyer where they can finalize the transaction.

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                              UCP Proxy                                  │
│                                                                         │
│  ┌────────────────────────────────────────────────────────────────────┐ │
│  │                         Transport Layer                            │ │
│  │   ┌──────────────────┐              ┌──────────────────┐           │ │
│  │   │     REST API     │              │    MCP Server    │           │ │
│  │   │ /checkout-sessions              │    /mcp (tools)  │           │ │
│  │   │  ↳ UCP-Agent hdr │              │  ↳ meta.ucp-agent│           │ │
│  │   └──────────────────┘              └──────────────────┘           │ │
│  └────────────────────────────────────────────────────────────────────┘ │
│                                   │                                     │
│                                   ▼                                     │
│  ┌────────────────────────────────────────────────────────────────────┐ │
│  │                    Capability Negotiation                          │ │
│  │                                                                    │ │
│  │   Extract profile URL → Fetch agent profile → Intersect caps       │ │
│  │   Business ∩ Agent = Negotiated context (stored in request)        │ │
│  └────────────────────────────────────────────────────────────────────┘ │
│                                   │                                     │
│                                   ▼                                     │
│  ┌────────────────────────────────────────────────────────────────────┐ │
│  │                         Handler Layer                              │ │
│  │   Create │ Get │ Update │ Complete │ Cancel  (Checkout operations) │ │
│  └────────────────────────────────────────────────────────────────────┘ │
│                                   │                                     │
│                                   ▼                                     │
│  ┌────────────────────────────────────────────────────────────────────┐ │
│  │                       Adapter Interface                            │ │
│  │                                                                    │ │
│  │   ┌──────────────┐   ┌──────────────┐   ┌──────────────┐           │ │
│  │   │ WooCommerce  │   │     Wix      │   │   Custom     │           │ │
│  │   │   Adapter    │   │   Adapter    │   │   Adapter    │           │ │
│  │   └──────────────┘   └──────────────┘   └──────────────┘           │ │
│  └────────────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────┘
```

**Key design decisions:**

- **Stateless**: session tokens encoded in checkout IDs. No server-side session storage.
- **Per-tenant**: one proxy instance per merchant. Scales to zero when idle.
- **Multi-transport**: same checkout logic exposed via REST and MCP (Model Context Protocol).
- **Pluggable adapters**: each platform adapter translates UCP → native API calls.
- **Capability negotiation**: agents provide their profile URL, proxy intersects capabilities.

## API Overview

| Endpoint                                | Description                                           |
| --------------------------------------- | ----------------------------------------------------- |
| `GET /.well-known/ucp`                  | Discovery profile (capabilities, transport endpoints) |
| `POST /checkout-sessions`               | Create checkout from line items                       |
| `GET /checkout-sessions/{id}`           | Get current checkout state                            |
| `PUT /checkout-sessions/{id}`           | Update checkout (full state replacement)              |
| `POST /checkout-sessions/{id}/complete` | Submit payment and finalize order                     |
| `DELETE /checkout-sessions/{id}`        | Cancel checkout                                       |
| `POST /mcp`                             | MCP transport (JSON-RPC over HTTP)                    |

**Capability Negotiation:** All checkout operations require agent profile for capability intersection:
- **REST**: `UCP-Agent: profile="https://agent.example/profile"` header
- **MCP**: `meta.ucp-agent.profile` field in request params

See [Development Guide](docs/development.md#capability-negotiation-ucp-spec-section-5) for details.

## Quick Start

```bash
# Demo mode (in-memory, no external dependencies)
MERCHANT_ID=demo ADAPTER_TYPE=demo go run ./cmd/proxy

# Test discovery (no auth required)
curl http://localhost:8080/.well-known/ucp

# Create checkout (requires UCP-Agent header)
curl -X POST http://localhost:8080/checkout-sessions \
  -H "Content-Type: application/json" \
  -H 'UCP-Agent: profile="https://your-agent.example/profile"' \
  -d '{"line_items": [{"product_id": "123", "quantity": 1}]}'
```

## Documentation

- **[Development Guide](docs/development.md)** — Running locally, building adapters, design patterns
- **[WooCommerce](docs/woocommerce.md)** — Setup, configuration, Stripe/Braintree integration
- **[Wix](docs/wix.md)** — OAuth setup, browser handoff flow

## License
MIT
