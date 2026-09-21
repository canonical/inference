package providers

import "testing"

func TestRedactURL(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{
			name:  "strip user, token and query",
			value: "https://user:token@example.com/v1?api_key=secret",
			want:  "https://example.com/v1",
		},
		{
			name:  "leave plain URL untouched",
			value: "https://example.com/v1",
			want:  "https://example.com/v1",
		},
		{
			name:    "invalid URL",
			value:   "https://[example.com/v1",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RedactURL(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("RedactURL %q: expected an error, got none", tt.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("RedactURL %q: unexpected error: %v", tt.value, err)
			}
			if got != tt.want {
				t.Errorf("RedactURL %q redacted to %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}
