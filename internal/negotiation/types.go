// Package negotiation implements UCP discovery negotiation per spec Section 5.
// Transport-agnostic core: handles profile fetching, caching, and capability intersection.
// REST middleware extracts profile URL from UCP-Agent header.
// MCP handlers extract profile URL from meta["ucp-agent"]["profile"] in request params.
package negotiation

import (
	"time"

	"ucp-proxy/internal/model"
)

// AgentProfile represents a fetched agent profile per UCP spec Section 5.5.
// Contains the agent's UCP metadata (capabilities and payment handlers).
type AgentProfile struct {
	UCP model.UCPMetadata `json:"ucp"`

	// Cache metadata - not from wire, set by fetcher
	ProfileURL string    `json:"-"`
	FetchedAt  time.Time `json:"-"`
	ExpiresAt  time.Time `json:"-"`
}

// NegotiatedContext is the result of capability negotiation per spec Section 5.7.
// Stored in http.Request context for REST, passed explicitly for MCP handlers.
// Contains the intersection of business and agent capabilities.
type NegotiatedContext struct {
	// AgentProfileURL is the profile URL from the request (for logging/debugging)
	AgentProfileURL string

	// Version is the negotiated protocol version (business version if compatible)
	Version string

	// Capabilities is the intersection: only capabilities both sides support
	Capabilities map[string][]model.Capability

	// PaymentHandlers is the intersection: only handlers agent can process
	PaymentHandlers map[string][]model.PaymentHandler

	// FetchError is non-nil if profile fetch failed but we're using fallback
	// The caller should include a warning message in the response when set
	FetchError error
}

// contextKey is the type for context values to avoid collisions
type contextKey string

// NegotiationContextKey is the context key for storing NegotiatedContext
const NegotiationContextKey contextKey = "ucp.negotiation"

// UCPAgentRequired is the error code when UCP-Agent header is missing
const UCPAgentRequired = "ucp_agent_required"

// UCPVersionUnsupported is the error code when agent version is too new
const UCPVersionUnsupported = "ucp_version_unsupported"
