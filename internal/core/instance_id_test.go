package core

import "testing"

func TestInstanceBase58(t *testing.T) {
	for _, test := range []struct {
		raw  []byte
		want string
	}{
		{[]byte{0}, "1"}, {[]byte{0, 0, 1}, "112"}, {[]byte("Hello World"), "JxF12TrwUP45BMd"},
	} {
		if got := encodeInstanceID(test.raw); got != test.want {
			t.Fatalf("Base58(%x) = %q, want %q", test.raw, got, test.want)
		}
	}
	if newInstanceID() != NewManager().InstanceID() {
		t.Fatal("instance ID must remain stable within the process")
	}
}
