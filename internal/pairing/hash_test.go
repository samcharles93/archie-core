package pairing

import (
	"testing"
)

func TestHashCodeProducesVerifiableHash(t *testing.T) {
	hashed, err := HashCode("ABCD2345")
	if err != nil {
		t.Fatal(err)
	}
	if len(hashed.Salt) == 0 {
		t.Error("Salt is empty")
	}
	if len(hashed.Hash) == 0 {
		t.Error("Hash is empty")
	}
	if !hashed.Verify("ABCD2345") {
		t.Error("Verify(correct code) = false, want true")
	}
}

func TestHashCodeRejectsWrongCode(t *testing.T) {
	hashed, err := HashCode("ABCD2345")
	if err != nil {
		t.Fatal(err)
	}
	if hashed.Verify("WRONGCOD") {
		t.Error("Verify(wrong code) = true, want false")
	}
}

func TestHashCodeUsesDistinctSaltsPerCall(t *testing.T) {
	// Two hashes of the SAME code must differ  --  otherwise the salt
	// isn't actually random, which defeats its purpose (identical codes
	// would produce identical hashes, leaking equality via a hash
	// comparison / rainbow-table risk).
	h1, err := HashCode("SAMECODE")
	if err != nil {
		t.Fatal(err)
	}
	h2, err := HashCode("SAMECODE")
	if err != nil {
		t.Fatal(err)
	}
	if string(h1.Salt) == string(h2.Salt) {
		t.Error("two calls produced the same salt")
	}
	if string(h1.Hash) == string(h2.Hash) {
		t.Error("two calls produced the same hash for the same code (salt not actually varying the digest)")
	}
	// But each must still verify against its own code.
	if !h1.Verify("SAMECODE") || !h2.Verify("SAMECODE") {
		t.Error("a hash failed to verify against the code it was created from")
	}
}

func TestHashedCodeVerifyIsCaseSensitive(t *testing.T) {
	hashed, err := HashCode("ABCD2345")
	if err != nil {
		t.Fatal(err)
	}
	if hashed.Verify("abcd2345") {
		t.Error("Verify matched a lowercase variant of an uppercase code")
	}
}

func TestHashedCodeVerifyEmptyInputDoesNotPanic(t *testing.T) {
	hashed, err := HashCode("ABCD2345")
	if err != nil {
		t.Fatal(err)
	}
	if hashed.Verify("") {
		t.Error("Verify(\"\") = true, want false")
	}
}

func TestVerifyOnZeroValueHashedCodeDoesNotPanic(t *testing.T) {
	var hashed HashedCode
	if hashed.Verify("anything") {
		t.Error("zero-value HashedCode.Verify should never return true")
	}
}
