package common

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	MinAccountPasswordLength           = 8
	MaxAccountPasswordLength           = 128
	DefaultRandomAccountPasswordLength = 16
	MinRandomAccountPasswordLength     = 12
	MaxRandomAccountPasswordLength     = 32
	accountPasswordMemory              = 19 * 1024
	accountPasswordTime                = 2
	accountPasswordSaltBytes           = 16
	accountPasswordKeyBytes            = 32

	randomAccountPasswordUpper = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	randomAccountPasswordLower = "abcdefghijklmnopqrstuvwxyz"
	randomAccountPasswordDigit = "0123456789"
	randomAccountPasswordPool  = randomAccountPasswordUpper + randomAccountPasswordLower + randomAccountPasswordDigit
)

var (
	ErrAccountPasswordLength       = errors.New("Password must contain between 8 and 128 characters.")
	ErrAccountPasswordSame         = errors.New("New password must be different from current password")
	ErrPasswordLegacyLimit         = errors.New("Long passwords are unavailable until the password storage upgrade is complete.")
	ErrRandomAccountPasswordLength = errors.New("Random password length must be between 12 and 32 characters.")
)

func cryptoRandAlphabetByte(alphabet string) (byte, error) {
	idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
	if err != nil {
		return 0, err
	}
	return alphabet[idx.Int64()], nil
}

// GenerateRandomAccountPassword generates a cryptographically random alphanumeric
// password (A-Z, a-z, 0-9 only; no symbols) guaranteed to include at least one
// uppercase letter, one lowercase letter, and one digit. Length 0 defaults to 16;
// allowed range is 12..32.
func GenerateRandomAccountPassword(length int) (string, error) {
	if length == 0 {
		length = DefaultRandomAccountPasswordLength
	}
	if length < MinRandomAccountPasswordLength || length > MaxRandomAccountPasswordLength {
		return "", ErrRandomAccountPasswordLength
	}
	buf := make([]byte, length)
	var err error
	if buf[0], err = cryptoRandAlphabetByte(randomAccountPasswordUpper); err != nil {
		return "", fmt.Errorf("generate random account password: %w", err)
	}
	if buf[1], err = cryptoRandAlphabetByte(randomAccountPasswordLower); err != nil {
		return "", fmt.Errorf("generate random account password: %w", err)
	}
	if buf[2], err = cryptoRandAlphabetByte(randomAccountPasswordDigit); err != nil {
		return "", fmt.Errorf("generate random account password: %w", err)
	}
	for i := 3; i < length; i++ {
		if buf[i], err = cryptoRandAlphabetByte(randomAccountPasswordPool); err != nil {
			return "", fmt.Errorf("generate random account password: %w", err)
		}
	}
	for i := length - 1; i > 0; i-- {
		jBig, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return "", fmt.Errorf("shuffle random account password: %w", err)
		}
		j := int(jBig.Int64())
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf), nil
}

// ValidateNewAccountPassword applies only when a user chooses a new password.
// Authentication must continue to accept historical passwords without applying
// the new policy. Do not normalize passwords, including surrounding whitespace.
func ValidateNewAccountPassword(password string) error {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < MinAccountPasswordLength || utf8.RuneCountInString(password) > MaxAccountPasswordLength {
		return ErrAccountPasswordLength
	}
	return nil
}

// HashAccountPassword is for account passwords, not MFA backup codes. The
// temporary bcrypt mode permits rolling out dual-format readers to all nodes
// before enabling Argon2id writes. Existing hashes are never rewritten in bulk.
func HashAccountPassword(password string) (string, error) {
	if err := ValidateNewAccountPassword(password); err != nil {
		return "", err
	}
	switch os.Getenv("ACCOUNT_PASSWORD_HASH_ALGORITHM") {
	case "bcrypt":
		if len(password) > 72 {
			return "", ErrPasswordLegacyLimit
		}
		return Password2Hash(password)
	case "", "argon2id":
	default:
		return "", errors.New("Unsupported account password hashing configuration.")
	}
	salt := make([]byte, accountPasswordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate account password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, accountPasswordTime, accountPasswordMemory, 1, accountPasswordKeyBytes)
	return fmt.Sprintf("$argon2id$v=19$m=19456,t=2,p=1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func validateArgon2AccountPassword(password, encoded string) bool {
	// Bound both plaintext and parameters before invoking a memory-hard KDF.
	// Only the version/parameters emitted by this application are accepted.
	if len(password) > MaxAccountPasswordLength*utf8.UTFMax || len(encoded) > 256 {
		return false
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=19456,t=2,p=1" {
		return false
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) != accountPasswordSaltBytes {
		return false
	}
	expected, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(expected) != accountPasswordKeyBytes {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, accountPasswordTime, accountPasswordMemory, 1, accountPasswordKeyBytes)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}
