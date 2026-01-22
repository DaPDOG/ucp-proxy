package model

import (
	"math"
	"strconv"
)

// ParseCents converts decimal string amounts (dollars) to cents (int64).
// Use for APIs that return amounts in major currency units (e.g., "99.00" = $99.00).
// Shared utility used by all platform transforms for consistent money handling.
// Handles edge cases: empty strings, missing decimals, large values.
// Examples: "99.00" → 9900, "1234.56" → 123456, "" → 0
func ParseCents(s string) int64 {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	// math.Round handles both positive and negative numbers correctly
	return int64(math.Round(f * 100))
}

// ParseMinorUnits converts string amounts already in minor units to int64.
// Use for APIs that return amounts in minor currency units (e.g., "8900" = 8900 cents = $89.00).
// WooCommerce Store API uses this format for all price fields.
// Examples: "8900" → 8900, "123456" → 123456, "" → 0
func ParseMinorUnits(s string) int64 {
	if s == "" {
		return 0
	}
	// Parse as float to handle potential decimal values, then truncate
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int64(f)
}
