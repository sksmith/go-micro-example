// Package secrets selects where credential-bearing env vars come from
// at process startup (DSN-006). The Go process always reads secrets via
// os.Getenv (so SEC-004's viper.BindEnv plumbing keeps working
// unchanged); the provider's job is to populate those env vars from
// somewhere appropriate to the runtime:
//
//   - In dev / CI, secrets come from the shell environment or a
//     gitignored .env file. The EnvProvider verifies presence and
//     does nothing else.
//   - In Kubernetes, a Vault Agent sidecar renders secrets to a shared
//     volume (default /vault/secrets/). The FileProvider reads each
//     file and exports its content as the matching env var.
//
// See docs/adr/0001-secrets-management.md for the architectural
// decision and the rationale for the Vault Agent injector pattern.
package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvVar is the canonical env-var name a secret resolves to. Pre-fixed
// with GME_ to match SEC-004's viper binding contract.
type EnvVar string

// These are env-var *names*, not credential values; gosec G101's
// substring matcher fires on "PASS" without distinguishing.
const (
	DBUser        EnvVar = "GME_DB_USER"
	DBPass        EnvVar = "GME_DB_PASS" // #nosec G101 -- env var name, not a credential
	RabbitMQUser  EnvVar = "GME_RABBITMQ_USER"
	RabbitMQPass  EnvVar = "GME_RABBITMQ_PASS" // #nosec G101 -- env var name, not a credential
	JWTSigningKey EnvVar = "GME_JWT_SIGNING_KEY"
)

// Required is the closed set of secrets the application needs to start.
// Listed centrally so the startup health check can fail fast if any
// are missing — SEC-004's "Add a startup health check" criterion.
var Required = []EnvVar{
	DBUser,
	DBPass,
	RabbitMQUser,
	RabbitMQPass,
	// JWTSigningKey is intentionally NOT in Required: the auth.NewSigner
	// strict flag handles its own prod-only enforcement, and non-prod
	// profiles tolerate an ephemeral key.
}

// Provider populates the process environment with secret values so the
// rest of the codebase keeps reading via os.Getenv. Implementations
// must be idempotent and safe to call once at startup.
type Provider interface {
	// Load resolves and exports each requested secret as an env var.
	// Returns an error containing every missing / unreadable secret;
	// the caller should fail fast on a non-nil result.
	Load(required []EnvVar) error
}

// LoadFromEnv selects a Provider based on the GME_SECRETS_PROVIDER env
// var and runs Load against the project-wide Required list. This is
// the entry point cmd/main.go should call once at startup, before
// config.Load.
//
// Provider selection:
//
//   - "" or "env" — EnvProvider. Default for local dev.
//   - "file"      — FileProvider rooted at GME_SECRETS_DIR (defaults
//     to /vault/secrets). Use with the Vault Agent
//     injector sidecar pattern.
//
// Anything else is an error.
func LoadFromEnv() error {
	provider, err := selectProvider()
	if err != nil {
		return err
	}
	return provider.Load(Required)
}

func selectProvider() (Provider, error) {
	switch strings.ToLower(os.Getenv("GME_SECRETS_PROVIDER")) {
	case "", "env":
		return EnvProvider{}, nil
	case "file":
		dir := os.Getenv("GME_SECRETS_DIR")
		if dir == "" {
			dir = "/vault/secrets"
		}
		return FileProvider{Dir: dir}, nil
	default:
		return nil, fmt.Errorf("unknown GME_SECRETS_PROVIDER %q (want \"env\" or \"file\")", os.Getenv("GME_SECRETS_PROVIDER"))
	}
}

// EnvProvider verifies that each required secret is already in the
// process environment. It does not write to the environment because it
// has nothing new to write — secrets came from the shell or a .env
// file before the process started.
type EnvProvider struct{}

func (EnvProvider) Load(required []EnvVar) error {
	var missing []string
	for _, v := range required {
		if os.Getenv(string(v)) == "" {
			missing = append(missing, string(v))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("env-provider: missing required secrets: %s", strings.Join(missing, ", "))
	}
	return nil
}

// FileProvider reads secrets from files in a directory and exports each
// as the matching env var. It is the right pairing for Vault Agent
// injector, which renders templates into a tmpfs volume. Filenames
// match the env-var name lowercased with leading underscores trimmed —
// e.g. GME_DB_PASS resolves to <Dir>/gme_db_pass.
type FileProvider struct {
	Dir string
}

func (f FileProvider) Load(required []EnvVar) error {
	var missing []string
	var unreadable []string
	for _, v := range required {
		path := filepath.Join(f.Dir, fileNameFor(v))
		// #nosec G304 -- path is f.Dir (operator-controlled, not user
		// input) joined with a hard-coded env-var name from the
		// closed Required set. There is no caller-controlled path
		// component.
		data, err := os.ReadFile(path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			missing = append(missing, path)
			continue
		case err != nil:
			unreadable = append(unreadable, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		// Trim a single trailing newline — Vault templates routinely
		// end with one — but preserve any deliberate whitespace inside
		// the secret.
		value := strings.TrimRight(string(data), "\n")
		if err := os.Setenv(string(v), value); err != nil {
			return fmt.Errorf("file-provider: setenv %s: %w", v, err)
		}
	}
	if len(missing) > 0 || len(unreadable) > 0 {
		var msg strings.Builder
		msg.WriteString("file-provider:")
		if len(missing) > 0 {
			fmt.Fprintf(&msg, " missing files: %s;", strings.Join(missing, ", "))
		}
		if len(unreadable) > 0 {
			fmt.Fprintf(&msg, " unreadable files: %s;", strings.Join(unreadable, "; "))
		}
		return errors.New(strings.TrimRight(msg.String(), ";"))
	}
	return nil
}

func fileNameFor(v EnvVar) string {
	return strings.ToLower(string(v))
}
