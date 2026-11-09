package auth

import "testing"

func TestPasswordHashing(t *testing.T) {
	h := NewHasher(8, 1, 1, 2)
	phc, err := h.Hash("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !h.Verify("correct horse", phc) || h.Verify("wrong", phc) {
		t.Fatal("verify failed")
	}
	phc2, _ := h.Hash("correct horse")
	if phc == phc2 {
		t.Fatal("salts must differ")
	}
	// Parameters come from the stored string, so a differently configured hasher still verifies.
	if !NewHasher(64*1024, 3, 1, 1).Verify("correct horse", phc) {
		t.Fatal("stored parameters must be honoured")
	}
	for _, bad := range []string{"", "plain", "$argon2id$v=19$m=8,t=1,p=1$!!$!!", "$bcrypt$x$y$z$w$v"} {
		if h.Verify("x", bad) {
			t.Errorf("accepted malformed hash %q", bad)
		}
	}
}
