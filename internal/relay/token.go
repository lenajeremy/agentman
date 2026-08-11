// Package relay implements the stateless message relay between a user's
// daemon and their phone.
//
// The defining constraint: the relay persistently stores no transcripts,
// messages, session lists, or accounts. It matches sockets and forwards bytes,
// retaining only live connections and short-lived pairing/last-seen state in
// memory.
//
// This removes persistent transcript storage, but not trust: payloads are not
// end-to-end encrypted and a relay operator can inspect live traffic. Users who
// do not accept that boundary should self-host the relay.
//
// Two things would normally force a database, and both are avoided here:
//
//   - Device credentials. Instead of storing issued tokens, the relay signs
//     them and re-verifies the signature on each connection. See token.go.
//   - User accounts. The account is derived from the daemon's own token, so
//     multiple users share a relay with no registration step and no user table.
package relay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Errors returned by VerifyDeviceToken.
var (
	ErrTokenMalformed = errors.New("relay: malformed token")
	ErrTokenSignature = errors.New("relay: bad token signature")
	ErrTokenExpired   = errors.New("relay: token expired")
)

// DeviceTokenTTL is how long a paired phone stays authorized. Long, because
// re-pairing means physically fetching a code from the Mac, and the token
// grants access only to that one account's live sockets.
const DeviceTokenTTL = 365 * 24 * time.Hour

// AccountID identifies one user's daemon-and-devices group.
type AccountID string

// DeriveAccount computes the account for a daemon token.
//
// Deriving rather than registering is what keeps the relay free of a user
// table: the same daemon token always maps to the same account, and the relay
// never needs to have seen it before. The relay sees the token during bearer
// authentication but does not store or transmit it onward; only this hash is
// used for routing.
func DeriveAccount(daemonToken string) AccountID {
	sum := sha256.Sum256([]byte("agentman-account\x00" + daemonToken))
	// 128 bits keeps cross-account routing collision-resistant even at public
	// relay scale. The earlier 64-bit truncation made a birthday collision a
	// realistic long-term multi-tenant risk.
	return AccountID(hex.EncodeToString(sum[:])[:32])
}

// deviceClaims is the body of a device token.
type deviceClaims struct {
	Account AccountID `json:"acc"`
	// Issued and Expires are unix seconds.
	Issued  int64 `json:"iat"`
	Expires int64 `json:"exp"`
	// Nonce keeps two tokens minted in the same second distinct, so a device
	// can be identified in logs without the relay storing anything.
	Nonce string `json:"jti"`
}

// MintDeviceToken issues a signed token for a paired device.
//
// This is a small HMAC construction rather than a JWT library: the relay is
// both the only issuer and the only verifier, so there is no algorithm
// negotiation to get wrong and no reason to carry the dependency.
func MintDeviceToken(secret string, account AccountID, nonce string) (string, error) {
	if secret == "" {
		return "", errors.New("relay: signing secret is empty")
	}
	now := time.Now()
	claims := deviceClaims{
		Account: account,
		Issued:  now.Unix(),
		Expires: now.Add(DeviceTokenTTL).Unix(),
		Nonce:   nonce,
	}
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(body)
	return encoded + "." + sign(secret, encoded), nil
}

// VerifyDeviceToken checks a token's signature and expiry, returning the
// account it authorizes. Nothing is looked up; the token carries its own proof.
func VerifyDeviceToken(secret, token string) (AccountID, error) {
	if secret == "" {
		return "", ErrTokenSignature
	}
	encoded, signature, ok := strings.Cut(token, ".")
	if !ok || encoded == "" || signature == "" {
		return "", ErrTokenMalformed
	}
	// Constant time: a leaky comparison here would let an attacker discover a
	// valid signature byte by byte.
	if !hmac.Equal([]byte(signature), []byte(sign(secret, encoded))) {
		return "", ErrTokenSignature
	}

	body, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", ErrTokenMalformed
	}
	var claims deviceClaims
	if err := json.Unmarshal(body, &claims); err != nil {
		return "", ErrTokenMalformed
	}
	if claims.Account == "" {
		return "", ErrTokenMalformed
	}
	if time.Now().Unix() > claims.Expires {
		return "", ErrTokenExpired
	}
	return claims.Account, nil
}

func sign(secret, encoded string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(encoded))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// FormatPairingCode renders a code in a readable form.
//
// Split into two groups so a human can transcribe it without losing their
// place. Every digit is random; the grouping carries no account information.
func FormatPairingCode(code string) string {
	if len(code) != pairingCodeDigits {
		return code
	}
	middle := len(code) / 2
	return fmt.Sprintf("%s %s", code[:middle], code[middle:])
}
