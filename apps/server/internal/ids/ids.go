// Package ids makes Recall's prefixed random ids (ids.ts).
package ids

import "crypto/rand"

// Alphabet is Crockford-like base32 without i, l, o and u.
const Alphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// Prefixes newId accepts.
const (
	Memory    = "mem"
	Document  = "doc"
	Chunk     = "chk"
	Key       = "key"
	User      = "usr"
	Account   = "acc"
	Member    = "mbr"
	Workspace = "ws"
	Invite    = "inv"
	Connector = "con"
	Person    = "per"
)

// New returns prefix + "_" + 16 characters, each one random byte & 31.
func New(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return FromBytes(prefix, b[:])
}

// FromBytes is New with the random bytes given.
func FromBytes(prefix string, b []byte) string {
	out := make([]byte, 0, len(prefix)+1+len(b))
	out = append(out, prefix...)
	out = append(out, '_')
	for _, x := range b {
		out = append(out, Alphabet[x&31])
	}
	return string(out)
}
