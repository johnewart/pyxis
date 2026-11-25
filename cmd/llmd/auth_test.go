package main

import "testing"

func TestValidateListenAuth(t *testing.T) {
	tests := []struct {
		addr    string
		auth    bool
		wantErr bool
	}{
		{"127.0.0.1:50051", false, false},
		{"127.0.0.1:50051", true, false},
		{"localhost:50051", false, false},
		{"localhost:50051", true, false},
		{"[::1]:50051", false, false},
		{"[::1]:50051", true, false},
		{":50051", false, true},
		{":50051", true, false},
		{"0.0.0.0:50051", false, true},
		{"0.0.0.0:50051", true, false},
	}
	for _, tc := range tests {
		err := validateListenAuth(tc.addr, tc.auth)
		if (err != nil) != tc.wantErr {
			t.Errorf("validateListenAuth(%q, %v) err=%v, wantErr=%v", tc.addr, tc.auth, err, tc.wantErr)
		}
	}
}
