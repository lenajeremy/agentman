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

// An app install that names itself is the same device each time it pairs:
// its pairings share a name. Two installs don't, nor two apps that sent the
// shared "phone".
func TestAnInstallPairedAgainIsTheSameDevice(t *testing.T) {
	const secret = "test-secret"
	name := func(nonce string) string {
		token, err := MintDeviceToken(secret, "acct", nonce)
		if err != nil {
			t.Fatal(err)
		}
		_, pairing, err := VerifyDevice(secret, token)
		if err != nil {
			t.Fatal(err)
		}
		return pairing
	}
	once, again := name("install-6f1c2b9a0d4e4c1b:aaaa"), name("install-6f1c2b9a0d4e4c1b:bbbb")
	if once != again {
		t.Fatalf("the same install paired twice: %q and %q", once, again)
	}
	if other := name("install-0123456789abcdef:aaaa"); other == once {
		t.Fatal("two installs share a name")
	}
	if name("phone:aaaa") == name("phone:bbbb") {
		t.Fatal("two older apps share a name")
	}
	if short := name("install-x:aaaa"); short == name("install-x:bbbb") {
		t.Fatal("a too-short install id was trusted")
	}
}
