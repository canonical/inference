package providers

import "testing"

func TestRedactURL(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
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
			name:  "invalid URL",
			value: "https://[example.com/v1",
			want:  "<invalid>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RedactURL(tt.value); got != tt.want {
				t.Errorf("RedactURL %q redacted to %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}
