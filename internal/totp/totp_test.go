package totp

import "testing"

// rfcSecret is the RFC 6238 test seed "12345678901234567890" in base32.
const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestCodeAtTimeKnownVectors(t *testing.T) {
	// RFC 6238 Appendix B (SHA1), truncated to the low 6 digits.
	cases := []struct {
		t    int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1234567890, "005924"},
		{2000000000, "279037"},
	}
	for _, tc := range cases {
		got, err := CodeAtTime(rfcSecret, tc.t)
		if err != nil {
			t.Fatalf("CodeAtTime(%d): %v", tc.t, err)
		}
		if got != tc.want {
			t.Errorf("CodeAtTime(%d) = %s, want %s", tc.t, got, tc.want)
		}
	}
}

func TestValidateSkew(t *testing.T) {
	const at = int64(1234567890)
	cur, _ := CodeAtTime(rfcSecret, at)
	prev, _ := CodeAtTime(rfcSecret, at-period)
	next, _ := CodeAtTime(rfcSecret, at+period)

	if !Validate(rfcSecret, cur, at, 1) {
		t.Error("current code should validate")
	}
	if !Validate(rfcSecret, prev, at, 1) {
		t.Error("previous-window code should validate within skew 1")
	}
	if !Validate(rfcSecret, next, at, 1) {
		t.Error("next-window code should validate within skew 1")
	}
	if Validate(rfcSecret, prev, at, 0) {
		t.Error("previous-window code should NOT validate with skew 0")
	}
	if Validate(rfcSecret, "000000", at-1000*period, 1) {
		t.Error("stale code should not validate")
	}
	if Validate(rfcSecret, "12345", at, 1) {
		t.Error("wrong-length code should not validate")
	}
}

func TestNewSecretRoundTrip(t *testing.T) {
	s, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 32 {
		t.Fatalf("expected a 32-char base32 secret, got %d (%q)", len(s), s)
	}
	now := int64(1700000000)
	code, err := CodeAtTime(s, now)
	if err != nil {
		t.Fatal(err)
	}
	if !Validate(s, code, now, 1) {
		t.Error("freshly generated secret should validate its own code")
	}
}

func TestOTPAuthURI(t *testing.T) {
	uri := OTPAuthURI("Budgie", "alice", rfcSecret)
	if want := "otpauth://totp/Budgie:alice?"; uri[:len(want)] != want {
		t.Errorf("unexpected URI prefix: %s", uri)
	}
	for _, sub := range []string{"secret=" + rfcSecret, "issuer=Budgie", "period=30", "digits=6"} {
		if !contains(uri, sub) {
			t.Errorf("URI missing %q: %s", sub, uri)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
