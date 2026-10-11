// Package wg implements the Sixter private overlay network: WireGuard key
// management, peer configuration and interface control.
//
// Why an overlay at all: an application scaled across machines still has ONE
// database, and that database usually sits on a host with no public address.
// The app's config says 127.0.0.1:5432 — correct on the database host, and
// meaningless anywhere else. The overlay gives every machine in a fleet a
// stable private address that works identically whether the peer is in the
// next rack or another continent.
//
// Design constraints, in order of importance:
//
//  1. A private key NEVER leaves the machine that generated it. The gateway
//     coordinates public keys and addresses only; it cannot decrypt peer
//     traffic even if fully compromised. This is the whole reason to use
//     WireGuard rather than terminating tunnels centrally.
//  2. Traffic goes peer-to-peer. The gateway introduces machines; it does not
//     carry their packets. A database round trip must not cross a relay.
//  3. Identity survives restarts. Keys are persisted, so a reboot doesn't
//     silently orphan a node from the mesh.
package wg

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/curve25519"
)

// Key is a Curve25519 key in WireGuard's canonical base64 form.
type Key string

// GenerateKey returns a new (private, public) pair.
//
// The clamping below is part of the X25519 specification, not an optimisation:
// clearing the low three bits and setting bit 254 forces the scalar into the
// correct subgroup and prevents small-subgroup attacks. WireGuard expects
// clamped keys; an unclamped one produces a key that appears to work and then
// fails to handshake against some peers.
func GenerateKey() (priv Key, pub Key, err error) {
	var p [32]byte
	if _, err := rand.Read(p[:]); err != nil {
		return "", "", fmt.Errorf("wg: entropy: %w", err)
	}
	p[0] &= 248
	p[31] &= 127
	p[31] |= 64

	pubBytes, err := curve25519.X25519(p[:], curve25519.Basepoint)
	if err != nil {
		return "", "", fmt.Errorf("wg: derive public key: %w", err)
	}
	return Key(base64.StdEncoding.EncodeToString(p[:])),
		Key(base64.StdEncoding.EncodeToString(pubBytes)), nil
}

// PublicFrom derives the public key for an existing private key, so a machine
// that already has an identity never needs to re-register a new one.
func PublicFrom(priv Key) (Key, error) {
	raw, err := base64.StdEncoding.DecodeString(string(priv))
	if err != nil || len(raw) != 32 {
		return "", fmt.Errorf("wg: malformed private key")
	}
	pub, err := curve25519.X25519(raw, curve25519.Basepoint)
	if err != nil {
		return "", fmt.Errorf("wg: derive public key: %w", err)
	}
	return Key(base64.StdEncoding.EncodeToString(pub)), nil
}

// Valid reports whether k is a well-formed 32-byte base64 key.
//
// Peer keys arrive over the network and are written into a config file that
// configures the kernel — they are untrusted input and must be validated
// before they get anywhere near `wg setconf`.
func (k Key) Valid() bool {
	raw, err := base64.StdEncoding.DecodeString(string(k))
	return err == nil && len(raw) == 32
}

// LoadOrCreateKey returns this machine's persistent private key, generating it
// on first use. Stored 0600: anyone who can read it can impersonate this
// machine on the overlay.
func LoadOrCreateKey(path string) (priv Key, pub Key, err error) {
	if b, rerr := os.ReadFile(path); rerr == nil {
		k := Key(strings.TrimSpace(string(b)))
		if k.Valid() {
			p, derr := PublicFrom(k)
			if derr == nil {
				return k, p, nil
			}
		}
		// A corrupt key file must not be silently reused — regenerate, which
		// costs one re-registration rather than a mesh that never handshakes.
	}

	priv, pub, err = GenerateKey()
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(path, []byte(priv), 0600); err != nil {
		return "", "", fmt.Errorf("wg: persist key: %w", err)
	}
	return priv, pub, nil
}
