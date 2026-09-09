package main

import "testing"

func TestListenAddress(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		port    string
		want    string
		wantErr bool
	}{
		{name: "defaults", want: "127.0.0.1:8000"},
		{name: "configured", host: "::1", port: "9000", want: "[::1]:9000"},
		{name: "invalid port", port: "invalid", wantErr: true},
		{name: "port out of range", port: "65536", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(bindHostEnvVar, tt.host)
			t.Setenv(bindPortEnvVar, tt.port)

			got, err := listenAddress()
			if (err != nil) != tt.wantErr {
				t.Fatalf("listenAddress() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("listenAddress() = %q, want %q", got, tt.want)
			}
		})
	}
}
