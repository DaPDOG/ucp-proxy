package model

import (
	"testing"
)

func TestParseCents(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int64
	}{
		{"whole number", "99.00", 9900},
		{"with cents", "123.45", 12345},
		{"zero", "0.00", 0},
		{"empty string", "", 0},
		{"large value", "1234567.89", 123456789},
		{"no decimals", "100", 10000},
		{"one decimal", "99.9", 9990},
		{"small value", "0.01", 1},
		{"invalid string", "abc", 0},
		{"negative (unusual)", "-10.00", -1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseCents(tt.input)
			if got != tt.want {
				t.Errorf("ParseCents(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseMinorUnits(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int64
	}{
		{"integer string", "8900", 8900},
		{"zero", "0", 0},
		{"empty string", "", 0},
		{"large value", "123456789", 123456789},
		{"negative", "-500", -500},
		{"invalid string", "abc", 0},
		{"with decimal (truncates)", "100.99", 100},
		{"whitespace only", "   ", 0},
		{"very large", "9999999999", 9999999999},
		{"leading zeros", "007", 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseMinorUnits(tt.input)
			if got != tt.want {
				t.Errorf("ParseMinorUnits(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// TestParseCentsVsParseMinorUnits documents the difference between the two functions.
// ParseCents: "99.00" (dollars) -> 9900 (cents)
// ParseMinorUnits: "9900" (already cents) -> 9900 (cents)
func TestParseCentsVsParseMinorUnits(t *testing.T) {
	// Same numeric result, different input format
	dollarsInput := "99.00"
	centsInput := "9900"

	fromDollars := ParseCents(dollarsInput)
	fromCents := ParseMinorUnits(centsInput)

	if fromDollars != fromCents {
		t.Errorf("ParseCents(%q)=%d should equal ParseMinorUnits(%q)=%d",
			dollarsInput, fromDollars, centsInput, fromCents)
	}

	// Verify they handle the same string differently
	sameString := "100"
	asCents := ParseCents(sameString)           // 100 dollars = 10000 cents
	asMinorUnits := ParseMinorUnits(sameString) // 100 cents = 100 cents

	if asCents != 10000 {
		t.Errorf("ParseCents(%q) = %d, want 10000", sameString, asCents)
	}
	if asMinorUnits != 100 {
		t.Errorf("ParseMinorUnits(%q) = %d, want 100", sameString, asMinorUnits)
	}
}
