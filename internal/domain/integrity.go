package domain

import (
	"fmt"
	"strings"
)

// Integrity tells how far the content of a stored file has been checked
// against what the sender had.
//
// verified: the sender's SHA-256 matched the one the server computed.
// unverified: only the server computed a SHA-256 (the sender gave none).
// unknown: no SHA-256 exists, as for files stored before checksums existed.
type Integrity string

const (
	IntegrityVerified   Integrity = "verified"
	IntegrityUnverified Integrity = "unverified"
	IntegrityUnknown    Integrity = "unknown"
)

// EmptySHA256 is the SHA-256 of zero bytes. It is the checksum reported for an
// upload session whose Confirmed_Offset is 0 and for empty files.
const EmptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// sha256HexLen is the length of a SHA-256 digest written as hex.
const sha256HexLen = 64

// NormalizeChecksum validates a client supplied SHA-256 and returns it in the
// canonical lowercase form used for storage and comparison. Exactly 64 hex
// digits are accepted, in either case; anything else, including surrounding
// whitespace or a "sha256:" prefix, is ErrInvalidInput so a typo can never be
// mistaken for a mismatch of file content.
func NormalizeChecksum(s string) (string, error) {
	if len(s) != sha256HexLen {
		return "", fmt.Errorf("checksum must be %d hex characters, got %d: %w", sha256HexLen, len(s), ErrInvalidInput)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return "", fmt.Errorf("checksum has a non-hex character at position %d: %w", i, ErrInvalidInput)
		}
	}
	return strings.ToLower(s), nil
}
