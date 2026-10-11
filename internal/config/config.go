package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

type Config struct {
	Port         string
	BaseURL      *url.URL
	CookieSecure bool
	TrustProxy   bool

	AdminUsername     string
	AdminPasswordHash []byte

	HashKey  []byte
	BlockKey []byte

	TableCodes map[string]int // code -> table number

	DatabaseURL string
}

func envBool(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func Load() (*Config, error) {
	c := &Config{
		Port:         strings.TrimSpace(os.Getenv("PORT")),
		CookieSecure: envBool("COOKIE_SECURE", true),
		TrustProxy:   envBool("TRUST_PROXY", false),
	}
	if c.Port == "" {
		c.Port = "8080"
	}

	base := strings.TrimSpace(os.Getenv("BASE_URL"))
	if base == "" {
		return nil, errors.New("BASE_URL is required")
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("BASE_URL is invalid: %q", base)
	}
	c.BaseURL = u

	c.AdminUsername = os.Getenv("ADMIN_USERNAME")
	if c.AdminUsername == "" {
		return nil, errors.New("ADMIN_USERNAME is required")
	}
	if h := strings.TrimSpace(os.Getenv("ADMIN_PASSWORD_HASH")); h != "" {
		if _, err := bcrypt.Cost([]byte(h)); err != nil {
			return nil, errors.New("ADMIN_PASSWORD_HASH is not a valid bcrypt hash")
		}
		c.AdminPasswordHash = []byte(h)
	} else if p := os.Getenv("ADMIN_PASSWORD"); p != "" {
		if len(p) < 8 {
			return nil, errors.New("ADMIN_PASSWORD must be at least 8 characters")
		}
		h, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		c.AdminPasswordHash = h
	} else {
		return nil, errors.New("set ADMIN_PASSWORD_HASH (preferred) or ADMIN_PASSWORD")
	}

	if c.HashKey, err = base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("SESSION_HASH_KEY"))); err != nil || len(c.HashKey) < 32 {
		return nil, errors.New("SESSION_HASH_KEY must be base64 and decode to at least 32 bytes")
	}
	if c.BlockKey, err = base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("SESSION_BLOCK_KEY"))); err != nil || len(c.BlockKey) != 32 {
		return nil, errors.New("SESSION_BLOCK_KEY must be base64 and decode to exactly 32 bytes")
	}

	c.TableCodes = map[string]int{}
	for _, part := range strings.Split(os.Getenv("TABLE_CODES"), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("TABLE_CODES entry %q must be code:table", part)
		}
		n, err := strconv.Atoi(strings.TrimSpace(kv[1]))
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("TABLE_CODES entry %q has an invalid table number", part)
		}
		code := strings.TrimSpace(kv[0])
		if len(code) < 6 {
			return nil, fmt.Errorf("TABLE_CODES code %q is too short (min 6 chars)", code)
		}
		if _, dup := c.TableCodes[code]; dup {
			return nil, fmt.Errorf("TABLE_CODES contains duplicate code %q", code)
		}
		c.TableCodes[code] = n
	}
	if len(c.TableCodes) == 0 {
		return nil, errors.New("TABLE_CODES must contain at least one code:table pair")
	}

	c.DatabaseURL = os.Getenv("DATABASE_URL")
	if c.DatabaseURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	return c, nil
}

const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// GenerateCodes returns n unique random 6-character alphanumeric codes.
func GenerateCodes(n int) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, n)
	for len(out) < n {
		b := make([]byte, 6)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		for i := range b {
			b[i] = alphabet[int(b[i])%len(alphabet)]
		}
		s := string(b)
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out, nil
}
