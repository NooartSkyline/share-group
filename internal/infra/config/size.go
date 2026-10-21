package config

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ErrInvalidSize is wrapped by every error returned from ParseSize.
var ErrInvalidSize = errors.New("invalid size")

// sizeUnits lists the accepted units from largest to smallest. The order
// matters: "KiB", "MiB" and "GiB" all end in "B", so "B" must be tried last.
var sizeUnits = []struct {
	suffix string
	factor uint64
}{
	{"GiB", 1 << 30},
	{"MiB", 1 << 20},
	{"KiB", 1 << 10},
	{"B", 1},
}

const sizeSyntax = "<digits><unit>, 1-19 digits, unit B, KiB, MiB or GiB (case-sensitive, no spaces), at most 9223372036854775807 bytes"

// ParseSize parses a byte count written as ^[0-9]{1,19}(B|KiB|MiB|GiB)$.
// Units are case-sensitive and binary (1 KiB = 1024 B). The result never
// exceeds math.MaxInt64; larger values are rejected rather than wrapped.
func ParseSize(s string) (int64, error) {
	reject := func(reason string) (int64, error) {
		return 0, fmt.Errorf("%w: %q (%s); accepted: %s", ErrInvalidSize, s, reason, sizeSyntax)
	}

	var factor uint64
	var digits string
	for _, u := range sizeUnits {
		if strings.HasSuffix(s, u.suffix) {
			factor = u.factor
			digits = strings.TrimSuffix(s, u.suffix)
			break
		}
	}
	if factor == 0 {
		return reject("missing or unknown unit")
	}
	if len(digits) == 0 || len(digits) > 19 {
		return reject("number must have 1 to 19 digits")
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return reject("number must contain only digits 0-9")
		}
	}

	// 19 decimal digits always fit in a uint64, so this cannot fail.
	n, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q: %w", ErrInvalidSize, s, err)
	}
	if n > math.MaxInt64/factor {
		return reject("exceeds the maximum size")
	}
	return int64(n * factor), nil
}

// FormatSize renders n using the largest unit that divides it exactly, so
// ParseSize(FormatSize(n)) == n for every n >= 0. Zero is "0B". Negative
// values are not valid sizes and are formatted in bytes with their sign.
func FormatSize(n int64) string {
	if n <= 0 {
		return strconv.FormatInt(n, 10) + "B"
	}
	for _, u := range sizeUnits {
		if uint64(n)%u.factor == 0 {
			return strconv.FormatUint(uint64(n)/u.factor, 10) + u.suffix
		}
	}
	return strconv.FormatInt(n, 10) + "B" // unreachable: the "B" factor is 1
}
