package negotiation

import (
	"testing"
)

func TestParseUCPAgentHeader(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		want    string
		wantErr bool
	}{
		{
			name:   "simple profile",
			header: `profile="https://agent.example/profile"`,
			want:   "https://agent.example/profile",
		},
		{
			name:   "profile with whitespace",
			header: `  profile="https://agent.example/profile"  `,
			want:   "https://agent.example/profile",
		},
		{
			name:   "profile with other params",
			header: `profile="https://agent.example/profile", other="value"`,
			want:   "https://agent.example/profile",
		},
		{
			name:   "profile after other params",
			header: `other="value", profile="https://foo.bar/p"`,
			want:   "https://foo.bar/p",
		},
		{
			name:   "profile with semicolon params ignored",
			header: `profile="https://agent.example/profile";version=1`,
			want:   "https://agent.example/profile",
		},
		{
			name:   "localhost URL",
			header: `profile="http://localhost:9999/profile"`,
			want:   "http://localhost:9999/profile",
		},
		{
			name:   "URL with path",
			header: `profile="https://agent.example.com/v1/agents/123/profile"`,
			want:   "https://agent.example.com/v1/agents/123/profile",
		},
		{
			name:    "empty header",
			header:  "",
			wantErr: true,
		},
		{
			name:    "whitespace only",
			header:  "   ",
			wantErr: true,
		},
		{
			name:    "missing profile key",
			header:  `other="value"`,
			wantErr: true,
		},
		{
			name:    "unquoted value",
			header:  `profile=https://foo.bar`,
			wantErr: true,
		},
		{
			name:    "unterminated quote",
			header:  `profile="https://foo.bar`,
			wantErr: true,
		},
		{
			name:   "escaped quote in URL",
			header: `profile="https://foo.bar/\"path\""`,
			want:   `https://foo.bar/"path"`,
		},
		{
			name:   "escaped backslash",
			header: `profile="https://foo.bar/path\\"`,
			want:   `https://foo.bar/path\`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseUCPAgentHeader(tt.header)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseUCPAgentHeader() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseUCPAgentHeader() = %q, want %q", got, tt.want)
			}
		})
	}
}
