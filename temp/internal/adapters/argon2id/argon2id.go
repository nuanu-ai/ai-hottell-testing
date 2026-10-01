// Package argon2id hashes passwords with argon2id and stores them as PHC strings.
package argon2id

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"

	"git.alva.dev/alva/harness-telemetry/internal/application/auth"
)

// Parameters of new hashes, per RFC 9106.
const (
	// DefaultMemoryKiB is the memory cost of new hashes: 64 MiB.
	DefaultMemoryKiB = 64 * 1024
	timeCost         = 3
	threads          = 4
	saltLen          = 16
	keyLen           = 32
)

var errMalformedHash = errors.New("malformed argon2id hash")

var _ auth.PasswordHasher = (*Hasher)(nil)

// Hasher hashes passwords with argon2id. New hashes take its parameters; Verify takes
// the parameters written in the hash, so old hashes survive a parameter change.
type Hasher struct {
	memoryKiB uint32
}

// New returns a hasher whose new hashes cost memoryKiB of memory: DefaultMemoryKiB in
// the service, less in tests to keep them fast.
func New(memoryKiB uint32) *Hasher {
	return &Hasher{memoryKiB: memoryKiB}
}

// Hash returns the PHC string of password under a fresh random salt:
// $argon2id$v=19$m=<memory>,t=<time>,p=<threads>$<salt>$<key>.
func (h *Hasher) Hash(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, timeCost, h.memoryKiB, threads, keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.memoryKiB, timeCost, threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify reports whether password matches the PHC string hash, comparing in constant
// time. A hash it cannot parse is an error, not a mismatch.
func (h *Hasher) Verify(password, hash string) (bool, error) {
	p, err := parse(hash)
	if err != nil {
		return false, err
	}
	key := argon2.IDKey([]byte(password), p.salt, p.time, p.memory, p.threads, uint32(len(p.key)))
	return subtle.ConstantTimeCompare(key, p.key) == 1, nil
}

type phc struct {
	memory, time uint32
	threads      uint8
	salt, key    []byte
}

// parse reads $argon2id$v=19$m=<memory>,t=<time>,p=<threads>$<salt>$<key>, salt and
// key in unpadded standard base64.
func parse(hash string) (phc, error) {
	fields := strings.Split(hash, "$")
	if len(fields) != 6 || fields[0] != "" || fields[1] != "argon2id" ||
		fields[2] != "v="+strconv.Itoa(argon2.Version) {
		return phc{}, errMalformedHash
	}

	var p phc
	params := strings.Split(fields[3], ",")
	if len(params) != 3 {
		return phc{}, errMalformedHash
	}
	memory, err := param(params[0], "m=", 32)
	if err != nil {
		return phc{}, err
	}
	timeCost, err := param(params[1], "t=", 32)
	if err != nil {
		return phc{}, err
	}
	threads, err := param(params[2], "p=", 8)
	if err != nil {
		return phc{}, err
	}
	p.memory, p.time, p.threads = uint32(memory), uint32(timeCost), uint8(threads)

	if p.salt, err = base64.RawStdEncoding.Strict().DecodeString(fields[4]); err != nil || len(p.salt) == 0 {
		return phc{}, errMalformedHash
	}
	if p.key, err = base64.RawStdEncoding.Strict().DecodeString(fields[5]); err != nil || len(p.key) == 0 {
		return phc{}, errMalformedHash
	}
	return p, nil
}

// param reads a positive decimal of bitSize bits after prefix.
func param(field, prefix string, bitSize int) (uint64, error) {
	digits, ok := strings.CutPrefix(field, prefix)
	if !ok {
		return 0, errMalformedHash
	}
	v, err := strconv.ParseUint(digits, 10, bitSize)
	if err != nil || v == 0 {
		return 0, errMalformedHash
	}
	return v, nil
}
