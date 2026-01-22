package negotiation

import (
	"testing"

	"ucp-proxy/internal/model"
)

func TestValidateVersion(t *testing.T) {
	tests := []struct {
		name            string
		businessVersion string
		agentVersion    string
		wantErr         bool
		errCode         string
	}{
		{
			name:            "equal versions",
			businessVersion: "2026-01-11",
			agentVersion:    "2026-01-11",
			wantErr:         false,
		},
		{
			name:            "agent older version",
			businessVersion: "2026-01-11",
			agentVersion:    "2025-06-01",
			wantErr:         false,
		},
		{
			name:            "agent newer version",
			businessVersion: "2026-01-11",
			agentVersion:    "2027-01-01",
			wantErr:         true,
			errCode:         UCPVersionUnsupported,
		},
		{
			name:            "empty agent version",
			businessVersion: "2026-01-11",
			agentVersion:    "",
			wantErr:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateVersion(tt.businessVersion, tt.agentVersion)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateVersion() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				verErr, ok := err.(*VersionError)
				if !ok {
					t.Errorf("expected VersionError, got %T", err)
					return
				}
				if verErr.Code != tt.errCode {
					t.Errorf("error code = %s, want %s", verErr.Code, tt.errCode)
				}
			}
		})
	}
}

func TestIntersectCapabilities(t *testing.T) {
	tests := []struct {
		name     string
		business map[string][]model.Capability
		agent    map[string][]model.Capability
		want     map[string][]model.Capability
	}{
		{
			name: "exact match",
			business: map[string][]model.Capability{
				"dev.ucp.shopping.checkout": {{Version: "2026-01-11"}},
			},
			agent: map[string][]model.Capability{
				"dev.ucp.shopping.checkout": {{Version: "2026-01-11"}},
			},
			want: map[string][]model.Capability{
				"dev.ucp.shopping.checkout": {{Version: "2026-01-11"}},
			},
		},
		{
			name: "partial overlap",
			business: map[string][]model.Capability{
				"dev.ucp.shopping.checkout":    {{Version: "2026-01-11"}},
				"dev.ucp.shopping.fulfillment": {{Version: "2026-01-11"}},
			},
			agent: map[string][]model.Capability{
				"dev.ucp.shopping.checkout": {{Version: "2026-01-11"}},
			},
			want: map[string][]model.Capability{
				"dev.ucp.shopping.checkout": {{Version: "2026-01-11"}},
			},
		},
		{
			name: "no overlap",
			business: map[string][]model.Capability{
				"dev.ucp.shopping.checkout": {{Version: "2026-01-11"}},
			},
			agent: map[string][]model.Capability{
				"dev.ucp.shopping.fulfillment": {{Version: "2026-01-11"}},
			},
			want: map[string][]model.Capability{},
		},
		{
			name: "empty agent accepts all",
			business: map[string][]model.Capability{
				"dev.ucp.shopping.checkout":    {{Version: "2026-01-11"}},
				"dev.ucp.shopping.fulfillment": {{Version: "2026-01-11"}},
			},
			agent: map[string][]model.Capability{},
			want: map[string][]model.Capability{
				"dev.ucp.shopping.checkout":    {{Version: "2026-01-11"}},
				"dev.ucp.shopping.fulfillment": {{Version: "2026-01-11"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := intersectCapabilities(tt.business, tt.agent)
			if len(got) != len(tt.want) {
				t.Errorf("intersectCapabilities() len = %d, want %d", len(got), len(tt.want))
				return
			}
			for key := range tt.want {
				if _, ok := got[key]; !ok {
					t.Errorf("missing capability %s", key)
				}
			}
		})
	}
}

func TestPruneOrphanedExtensions(t *testing.T) {
	tests := []struct {
		name       string
		caps       map[string][]model.Capability
		wantPruned bool
		wantLen    int
	}{
		{
			name: "no extensions",
			caps: map[string][]model.Capability{
				"dev.ucp.shopping.checkout": {{Version: "2026-01-11"}},
			},
			wantPruned: false,
			wantLen:    1,
		},
		{
			name: "extension with parent present",
			caps: map[string][]model.Capability{
				"dev.ucp.shopping.checkout": {{Version: "2026-01-11"}},
				"dev.ucp.shopping.discount": {{Version: "2026-01-11", Extends: model.NewSingleExtends("dev.ucp.shopping.checkout")}},
			},
			wantPruned: false,
			wantLen:    2,
		},
		{
			name: "orphaned extension",
			caps: map[string][]model.Capability{
				"dev.ucp.shopping.discount": {{Version: "2026-01-11", Extends: model.NewSingleExtends("dev.ucp.shopping.checkout")}},
			},
			wantPruned: true,
			wantLen:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pruneOrphanedExtensions(tt.caps)
			if got != tt.wantPruned {
				t.Errorf("pruneOrphanedExtensions() = %v, want %v", got, tt.wantPruned)
			}
			if len(tt.caps) != tt.wantLen {
				t.Errorf("caps len = %d, want %d", len(tt.caps), tt.wantLen)
			}
		})
	}
}

func TestIntersectPaymentHandlers(t *testing.T) {
	tests := []struct {
		name     string
		business map[string][]model.PaymentHandler
		agent    map[string][]model.PaymentHandler
		wantLen  int
	}{
		{
			name: "exact match",
			business: map[string][]model.PaymentHandler{
				"com.stripe": {{ID: "stripe-1", Version: "2026-01-01"}},
			},
			agent: map[string][]model.PaymentHandler{
				"com.stripe": {{ID: "stripe-1", Version: "2026-01-01"}},
			},
			wantLen: 1,
		},
		{
			name: "no match",
			business: map[string][]model.PaymentHandler{
				"com.stripe": {{ID: "stripe-1", Version: "2026-01-01"}},
			},
			agent: map[string][]model.PaymentHandler{
				"com.paypal": {{ID: "paypal-1", Version: "2026-01-01"}},
			},
			wantLen: 0,
		},
		{
			name: "empty agent accepts all",
			business: map[string][]model.PaymentHandler{
				"com.stripe": {{ID: "stripe-1", Version: "2026-01-01"}},
				"com.paypal": {{ID: "paypal-1", Version: "2026-01-01"}},
			},
			agent:   map[string][]model.PaymentHandler{},
			wantLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := intersectPaymentHandlers(tt.business, tt.agent)
			if len(got) != tt.wantLen {
				t.Errorf("intersectPaymentHandlers() len = %d, want %d", len(got), tt.wantLen)
			}
		})
	}
}

func TestHandlersCompatible(t *testing.T) {
	tests := []struct {
		name     string
		business model.PaymentHandler
		agent    model.PaymentHandler
		want     bool
	}{
		{
			name:     "same ID and version",
			business: model.PaymentHandler{ID: "stripe-1", Version: "2026-01-01"},
			agent:    model.PaymentHandler{ID: "stripe-1", Version: "2026-01-01"},
			want:     true,
		},
		{
			name:     "different ID",
			business: model.PaymentHandler{ID: "stripe-1", Version: "2026-01-01"},
			agent:    model.PaymentHandler{ID: "stripe-2", Version: "2026-01-01"},
			want:     false,
		},
		{
			name:     "business older version (compatible)",
			business: model.PaymentHandler{ID: "stripe-1", Version: "2025-01-01"},
			agent:    model.PaymentHandler{ID: "stripe-1", Version: "2026-01-01"},
			want:     true,
		},
		{
			name:     "business newer version (incompatible)",
			business: model.PaymentHandler{ID: "stripe-1", Version: "2027-01-01"},
			agent:    model.PaymentHandler{ID: "stripe-1", Version: "2026-01-01"},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := handlersCompatible(tt.business, tt.agent)
			if got != tt.want {
				t.Errorf("handlersCompatible() = %v, want %v", got, tt.want)
			}
		})
	}
}
