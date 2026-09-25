package accountmodel

import (
	"errors"
	"testing"
)

func TestPasswordRecoveryReviewHash(t *testing.T) {
	called := false
	hash, err := PasswordRecoveryReviewHash(" rejected ", "ignored", func(password string) (string, error) {
		called = true
		return "hashed", nil
	})
	if err != nil || hash != "" || called {
		t.Fatalf("rejected review hash=%q err=%v called=%v, want no hash", hash, err, called)
	}

	hash, err = PasswordRecoveryReviewHash(" RESET ", "newpw", func(password string) (string, error) {
		if password != "newpw" {
			t.Fatalf("hash password = %q, want newpw", password)
		}
		return "hashed-newpw", nil
	})
	if err != nil || hash != "hashed-newpw" {
		t.Fatalf("reset review hash=%q err=%v, want hashed-newpw", hash, err)
	}

	if _, err := PasswordRecoveryReviewHash("reset", " ", func(string) (string, error) {
		t.Fatalf("hash should not be called for blank reset password")
		return "", nil
	}); err == nil {
		t.Fatalf("blank reset password accepted")
	}

	wantErr := errors.New("hash failed")
	if _, err := PasswordRecoveryReviewHash("reset", "newpw", func(string) (string, error) {
		return "", wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("hash error = %v, want %v", err, wantErr)
	}
}
