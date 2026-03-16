package cli

import (
	"strings"
	"testing"
)

func TestValidateLoopbackListenAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		addr    string
		wantErr string
	}{
		{name: "ipv4 loopback", addr: "127.0.0.1:9000"},
		{name: "localhost", addr: " localhost:9000 "},
		{name: "ipv6 loopback", addr: "[::1]:9000"},
		{name: "wildcard ipv4", addr: "0.0.0.0:9000", wantErr: "loopback host"},
		{name: "wildcard ipv6", addr: "[::]:9000", wantErr: "loopback host"},
		{name: "public hostname", addr: "example.com:9000", wantErr: "loopback host"},
		{name: "missing port", addr: "127.0.0.1", wantErr: "parse --listen"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateLoopbackListenAddress(tt.addr)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateLoopbackListenAddress(%q) error = %v", tt.addr, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateLoopbackListenAddress(%q) error = nil, want %q", tt.addr, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateLoopbackListenAddress(%q) error = %v, want substring %q", tt.addr, err, tt.wantErr)
			}
		})
	}
}
