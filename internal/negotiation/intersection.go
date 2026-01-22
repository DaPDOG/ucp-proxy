package negotiation

import (
	"context"
	"fmt"

	"golang.org/x/mod/semver"

	"ucp-proxy/internal/model"
)

// Negotiator performs UCP capability negotiation per spec Section 5.7.
type Negotiator struct {
	fetcher         ProfileFetcher
	businessProfile *model.DiscoveryProfile
}

// NewNegotiator creates a negotiator with the given fetcher and business profile.
func NewNegotiator(fetcher ProfileFetcher, businessProfile *model.DiscoveryProfile) *Negotiator {
	return &Negotiator{
		fetcher:         fetcher,
		businessProfile: businessProfile,
	}
}

// Negotiate fetches the agent profile and computes capability intersection.
// Per spec, if agent version > business version, returns VERSION_UNSUPPORTED error.
// If fetch fails but we can degrade gracefully, returns context with FetchError set.
func (n *Negotiator) Negotiate(ctx context.Context, agentProfileURL string) (*NegotiatedContext, error) {
	agentProfile, fetchErr := n.fetcher.Fetch(ctx, agentProfileURL)

	// Fetch completely failed and no fallback possible
	if fetchErr != nil && agentProfile == nil {
		// Return negotiated context with full business profile and warning
		return &NegotiatedContext{
			AgentProfileURL: agentProfileURL,
			Version:         n.businessProfile.UCP.Version,
			Capabilities:    n.businessProfile.UCP.Capabilities,
			PaymentHandlers: n.businessProfile.UCP.PaymentHandlers,
			FetchError:      fetchErr,
		}, nil
	}

	business := n.businessProfile.UCP
	agent := agentProfile.UCP

	// Version validation per spec Section 5.7.1
	// If agent version > business version: unsupported
	if err := validateVersion(business.Version, agent.Version); err != nil {
		return nil, err
	}

	// Compute capability intersection per spec 5.7.3
	caps := intersectCapabilities(business.Capabilities, agent.Capabilities)

	// Compute payment handler intersection
	handlers := intersectPaymentHandlers(business.PaymentHandlers, agent.PaymentHandlers)

	result := &NegotiatedContext{
		AgentProfileURL: agentProfileURL,
		Version:         business.Version, // Business version is canonical
		Capabilities:    caps,
		PaymentHandlers: handlers,
	}

	// Pass through fetch error if we used stale data
	if fetchErr != nil {
		result.FetchError = fetchErr
	}

	return result, nil
}

// validateVersion checks if business can serve agent's requested version.
// UCP versions are YYYY-MM-DD format, compared as strings (lexicographic works).
// Agent can request older or equal version, but not newer than business supports.
func validateVersion(businessVersion, agentVersion string) error {
	// Empty agent version = accept whatever business offers
	if agentVersion == "" {
		return nil
	}

	// UCP uses YYYY-MM-DD format which sorts correctly as strings
	// Agent version > business version means agent wants features we don't have
	if agentVersion > businessVersion {
		return &VersionError{
			Code:            UCPVersionUnsupported,
			Message:         fmt.Sprintf("agent requires version %s, business supports %s", agentVersion, businessVersion),
			AgentVersion:    agentVersion,
			BusinessVersion: businessVersion,
		}
	}

	return nil
}

// VersionError is returned when agent requests unsupported version.
type VersionError struct {
	Code            string
	Message         string
	AgentVersion    string
	BusinessVersion string
}

func (e *VersionError) Error() string {
	return e.Message
}

// intersectCapabilities computes capability intersection per spec 5.7.3.
// Algorithm:
//  1. For each business capability, include if agent has same name
//  2. Prune extensions where `extends` parent is not in intersection
//  3. Repeat pruning until stable
func intersectCapabilities(
	business map[string][]model.Capability,
	agent map[string][]model.Capability,
) map[string][]model.Capability {
	// If agent doesn't declare capabilities, use all business capabilities
	// (agent accepts whatever business offers)
	if len(agent) == 0 {
		return business
	}

	result := make(map[string][]model.Capability)

	// Step 1: Include business capabilities that agent also has
	for name, businessCaps := range business {
		if agentCaps, ok := agent[name]; ok {
			// Intersect versions within capability family
			intersected := intersectCapabilityVersions(businessCaps, agentCaps)
			if len(intersected) > 0 {
				result[name] = intersected
			}
		}
	}

	// Step 2: Prune orphaned extensions (repeat until stable)
	for {
		pruned := pruneOrphanedExtensions(result)
		if !pruned {
			break
		}
	}

	return result
}

// intersectCapabilityVersions finds compatible versions between business and agent.
// For now, we use business capability if agent has any version of same name.
// More sophisticated version negotiation could be added later.
func intersectCapabilityVersions(business, agent []model.Capability) []model.Capability {
	// Simplified: if agent has capability, use business version
	// Future: semver-aware intersection if needed
	if len(agent) > 0 {
		return business
	}
	return nil
}

// pruneOrphanedExtensions removes capabilities whose `extends` parents are all missing.
// For multi-parent extensions, keeps if ANY parent is present.
// Returns true if anything was pruned.
func pruneOrphanedExtensions(caps map[string][]model.Capability) bool {
	pruned := false

	for name, capList := range caps {
		for _, cap := range capList {
			if cap.Extends != nil && cap.Extends.IsExtension() {
				// Check if at least one parent exists in the intersection
				parents := cap.Extends.GetParents()
				hasParent := false
				for _, parent := range parents {
					if _, ok := caps[parent]; ok {
						hasParent = true
						break
					}
				}
				if !hasParent {
					delete(caps, name)
					pruned = true
					break
				}
			}
		}
	}

	return pruned
}

// intersectPaymentHandlers returns handlers that both business offers and agent supports.
func intersectPaymentHandlers(
	business map[string][]model.PaymentHandler,
	agent map[string][]model.PaymentHandler,
) map[string][]model.PaymentHandler {
	// If agent doesn't declare handlers, use all business handlers
	if len(agent) == 0 {
		return business
	}

	result := make(map[string][]model.PaymentHandler)

	for name, businessHandlers := range business {
		if agentHandlers, ok := agent[name]; ok {
			// Agent supports this handler type
			intersected := intersectHandlerVersions(businessHandlers, agentHandlers)
			if len(intersected) > 0 {
				result[name] = intersected
			}
		}
	}

	return result
}

// intersectHandlerVersions finds compatible handler versions.
// Uses semver comparison if versions look like semver, otherwise string equality.
func intersectHandlerVersions(business, agent []model.PaymentHandler) []model.PaymentHandler {
	var result []model.PaymentHandler

	for _, bh := range business {
		for _, ah := range agent {
			if handlersCompatible(bh, ah) {
				result = append(result, bh)
				break
			}
		}
	}

	return result
}

// handlersCompatible checks if business handler is compatible with agent handler.
// Business handler version must be <= agent handler version (agent can handle older formats).
func handlersCompatible(business, agent model.PaymentHandler) bool {
	// Same ID is a basic requirement
	if business.ID != agent.ID {
		return false
	}

	// Check version compatibility
	bv := normalizeVersion(business.Version)
	av := normalizeVersion(agent.Version)

	// If either version is not semver-like, fall back to string comparison
	if !semver.IsValid(bv) || !semver.IsValid(av) {
		// For YYYY-MM-DD format, string comparison works
		return business.Version <= agent.Version
	}

	// semver: business version <= agent version
	return semver.Compare(bv, av) <= 0
}

// normalizeVersion adds "v" prefix if needed for semver parsing.
func normalizeVersion(v string) string {
	if v == "" {
		return "v0.0.0"
	}
	if v[0] != 'v' {
		return "v" + v
	}
	return v
}
