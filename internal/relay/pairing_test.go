package relay

import (
	"strings"
	"testing"
)

// A token names its pairing: the same each time it is checked, different for
// every other pairing of the account, and nothing of the token itself.
func TestATokenNamesItsPairing(t *testing.T) {
	const secret = "test-secret"
	first, err := MintDeviceToken(secret, "acct", "phone:aaaa")
	if err != nil {
		t.Fatal(err)
	}
	second, err := MintDeviceToken(secret, "acct", "phone:bbbb")
	if err != nil {
		t.Fatal(err)
	}
	account, pairing, err := VerifyDevice(secret, first)
	if err != nil || account != "acct" {
		t.Fatalf("account = %q, err = %v", account, err)
	}
	_, sameAgain, _ := VerifyDevice(secret, first)
	if pairing == "" || pairing != sameAgain {
		t.Fatalf("pairing %q, then %q: want the same", pairing, sameAgain)
	}
	if _, other, _ := VerifyDevice(secret, second); other == pairing {
		t.Fatalf("two pairings share %q", pairing)
	}
	if !strings.HasPrefix(pairing, "p") || len(pairing) != 17 || strings.Contains(pairing, "aaaa") {
		t.Fatalf("pairing = %q, want p and 16 hex digits, nothing of the nonce", pairing)
	}
	if _, _, err := VerifyDevice("other-secret", first); err == nil {
		t.Fatal("a token signed with another secret verified")
	}
}
