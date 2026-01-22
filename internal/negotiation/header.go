package negotiation

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dunglas/httpsfv"
)

// ParseUCPAgentHeader extracts the profile URL from UCP-Agent header.
// Format: profile="https://agent.example/profile" (RFC 8941 Dictionary).
//
// Examples:
//   - profile="https://agent.example/profile" → https://agent.example/profile
//   - profile="https://foo.bar/p";version=1  → https://foo.bar/p (params ignored)
//
// Returns error if header is empty, malformed, or missing profile key.
func ParseUCPAgentHeader(header string) (string, error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", errors.New("empty UCP-Agent header")
	}

	dict, err := httpsfv.UnmarshalDictionary([]string{header})
	if err != nil {
		return "", fmt.Errorf("invalid UCP-Agent header: %w", err)
	}

	member, ok := dict.Get("profile")
	if !ok {
		return "", errors.New("profile key not found in UCP-Agent header")
	}

	item, ok := member.(httpsfv.Item)
	if !ok {
		return "", errors.New("profile value must be an item")
	}

	url, ok := item.Value.(string)
	if !ok {
		return "", errors.New("profile value must be a string")
	}

	return url, nil
}
