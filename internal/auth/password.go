package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

func ValidatePassword(password string) error {
	if len([]rune(password)) < 4 {
		return fmt.Errorf("password must contain at least 4 characters")
	}
	if len(password) > 1024 {
		return fmt.Errorf("password must contain no more than 1024 characters")
	}
	return nil
}

func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=3,p=2$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

func VerifyPassword(encoded, password string) error {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return fmt.Errorf("invalid password hash")
	}
	var memory uint64
	var iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil || memory == 0 || iterations == 0 || parallelism == 0 || memory > 1<<20 {
		return fmt.Errorf("invalid password hash parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 16 {
		return fmt.Errorf("invalid password hash salt")
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) != 32 {
		return fmt.Errorf("invalid password hash")
	}
	actual := argon2.IDKey([]byte(password), salt, uint32(iterations), uint32(memory), parallelism, uint32(len(expected)))
	if subtle.ConstantTimeCompare(actual, expected) != 1 {
		return fmt.Errorf("invalid password")
	}
	return nil
}
