// Package relay implements the stateless message relay between a user's
// daemon and their phone.
//
// The defining constraint: the relay stores nothing. No transcripts, no
// messages, no session lists, no database. It matches two sockets and forwards
// bytes between them, treating each frame's payload as opaque.
//
// That is what makes the deployment question uninteresting — run the public
// relay, deploy your own, or skip it and use a LAN address. A relay operator
// has nothing to leak, nothing to retain, and nothing to migrate.
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
// never needs to have seen it before. The daemon token itself is never stored
// or transmitted onward — only this hash is used for routing.
func DeriveAccount(daemonToken string) AccountID {
	sum := sha256.Sum256([]byte("agentman-account\x00" + daemonToken))
	return AccountID(hex.EncodeToString(sum[:])[:16])
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
// Grouped in fours rather than at the shard boundary: the split between the
// account shard and the random half is an implementation detail, and drawing
// attention to it would only invite someone to read meaning into the first
// two digits.
func FormatPairingCode(code string) string {
	if len(code) != 8 {
		return code
	}
	return fmt.Sprintf("%s %s", code[:4], code[4:])
}
