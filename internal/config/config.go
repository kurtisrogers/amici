// Package config reads Amici's runtime configuration from the environment.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment names the deployment. It gates exactly two things: whether
// cookies may travel over plain HTTP, and whether the fixtures endpoints
// exist. Nothing about privacy or sanitising changes between environments,
// because a security control you only run in production is a security control
// you have never tested.
type Environment string

const (
	EnvDevelopment Environment = "development"
	EnvTest        Environment = "test"
	EnvProduction  Environment = "production"
)

// Config is the full set of knobs.
type Config struct {
	Env  Environment
	Addr string

	// DatabasePath is the SQLite file. ":memory:" is accepted for tests.
	DatabasePath string

	// SecretKey keys the invite code HMAC. Losing it invalidates outstanding
	// invite codes, which is inconvenient but not dangerous; leaking it lets
	// somebody brute force invite codes offline from a database dump.
	SecretKey []byte

	// BaseURL is used when showing a member the link that goes with their
	// invite code. It never affects routing.
	BaseURL string

	// SecureCookies sets the Secure flag. It is forced on in production.
	SecureCookies bool

	// ReadTimeout and friends bound how long a request may hold a connection.
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration

	// MaxRequestBytes bounds a form submission. The profile canvas is the
	// largest thing a member can send, so this has to clear its limits with
	// room for the rest of the form.
	MaxRequestBytes int64

	// EnableFixtures exposes the endpoints the end-to-end suite uses to put
	// the database into a known state. It refuses to switch on in production.
	EnableFixtures bool

	// TrustProxyHeaders makes the server believe X-Forwarded-For. Only turn
	// this on when something you control is actually in front.
	TrustProxyHeaders bool

	// Mail describes where confirmation and password reset messages go.
	Mail MailConfig
}

// MailConfig is the delivery configuration for Amici's four transactional
// messages.
type MailConfig struct {
	// SMTPAddr is host:port. When it is empty, outside production, messages
	// are recorded in an in-memory outbox instead of being delivered.
	SMTPAddr string
	// From is the address messages come from.
	From string
	// Username and Password authenticate to the relay. Both empty sends
	// unauthenticated, which is normal for a relay on localhost.
	Username string
	Password string
	// AllowPlaintext permits delivery to a relay that does not offer
	// STARTTLS. It has to be asked for, because the messages Amici sends are
	// account recovery links and one read in transit is an account taken
	// over.
	AllowPlaintext bool
}

// Configured reports whether a real mail server is set up.
func (m MailConfig) Configured() bool { return m.SMTPAddr != "" }

// Load reads configuration from the environment and validates it.
func Load() (*Config, error) {
	env := Environment(strings.ToLower(getenv("AMICI_ENV", string(EnvDevelopment))))
	switch env {
	case EnvDevelopment, EnvTest, EnvProduction:
	default:
		return nil, fmt.Errorf("AMICI_ENV must be development, test or production, got %q", env)
	}

	cfg := &Config{
		Env:             env,
		Addr:            getenv("AMICI_ADDR", "127.0.0.1:8080"),
		DatabasePath:    getenv("AMICI_DB", "amici.db"),
		BaseURL:         strings.TrimSuffix(getenv("AMICI_BASE_URL", "http://127.0.0.1:8080"), "/"),
		ReadTimeout:     15 * time.Second,
		WriteTimeout:    30 * time.Second,
		IdleTimeout:     90 * time.Second,
		ShutdownTimeout: 15 * time.Second,
		MaxRequestBytes: 512 * 1024,
	}

	secret := os.Getenv("AMICI_SECRET_KEY")
	switch {
	case secret != "":
		if len(secret) < 32 {
			return nil, errors.New("AMICI_SECRET_KEY needs to be at least 32 characters")
		}
		cfg.SecretKey = []byte(secret)
	case env == EnvProduction:
		return nil, errors.New("AMICI_SECRET_KEY must be set in production, or every outstanding invite code becomes forgeable across restarts")
	default:
		// Outside production a throwaway key is friendlier than a startup
		// error. Restarting invalidates local invite codes, which is fine.
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generate development secret: %w", err)
		}
		cfg.SecretKey = []byte(base64.RawStdEncoding.EncodeToString(key))
	}

	var err error
	if cfg.SecureCookies, err = getbool("AMICI_SECURE_COOKIES", env == EnvProduction); err != nil {
		return nil, err
	}
	if env == EnvProduction && !cfg.SecureCookies {
		return nil, errors.New("AMICI_SECURE_COOKIES cannot be disabled in production")
	}
	if cfg.EnableFixtures, err = getbool("AMICI_ENABLE_FIXTURES", env != EnvProduction); err != nil {
		return nil, err
	}
	if env == EnvProduction && cfg.EnableFixtures {
		return nil, errors.New("AMICI_ENABLE_FIXTURES cannot be enabled in production")
	}
	if cfg.TrustProxyHeaders, err = getbool("AMICI_TRUST_PROXY", false); err != nil {
		return nil, err
	}
	if cfg.MaxRequestBytes, err = getint64("AMICI_MAX_REQUEST_BYTES", cfg.MaxRequestBytes); err != nil {
		return nil, err
	}

	if err := cfg.loadMail(env); err != nil {
		return nil, err
	}

	return cfg, nil
}

// loadMail reads the mail configuration.
//
// Production must have a mail server. Without one, registration cannot confirm
// an address and a forgotten password cannot be recovered, so an account is
// unreachable in both directions and nothing says so: the member waits for an
// email that was never going to arrive. Refusing to start is the honest
// failure, and it happens once, at boot, rather than silently for every member
// who signs up.
func (c *Config) loadMail(env Environment) error {
	c.Mail = MailConfig{
		SMTPAddr: getenv("AMICI_SMTP_ADDR", ""),
		From:     getenv("AMICI_MAIL_FROM", ""),
		Username: os.Getenv("AMICI_SMTP_USERNAME"),
		Password: os.Getenv("AMICI_SMTP_PASSWORD"),
	}

	var err error
	if c.Mail.AllowPlaintext, err = getbool("AMICI_SMTP_ALLOW_PLAINTEXT", false); err != nil {
		return err
	}
	if env == EnvProduction && c.Mail.AllowPlaintext {
		// A relay on a loopback interface is a legitimate arrangement, but it
		// is also indistinguishable from a misconfiguration from in here, and
		// getting it wrong sends password reset links across a network in
		// clear text. Terminate TLS in front of Amici instead.
		return errors.New("AMICI_SMTP_ALLOW_PLAINTEXT cannot be enabled in production")
	}

	if env == EnvProduction {
		if !c.Mail.Configured() {
			return errors.New("AMICI_SMTP_ADDR must be set in production, or nobody can confirm an address or recover a password")
		}
		if c.Mail.From == "" {
			return errors.New("AMICI_MAIL_FROM must be set in production")
		}
	}
	if c.Mail.From == "" {
		c.Mail.From = "amici@localhost"
	}
	return nil
}

// IsProduction reports whether this is the real thing.
func (c *Config) IsProduction() bool { return c.Env == EnvProduction }

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func getbool(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean, got %q", key, raw)
	}
	return v, nil
}

func getint64(key string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", key, raw)
	}
	return v, nil
}
