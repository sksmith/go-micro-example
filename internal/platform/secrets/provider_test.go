package secrets_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sksmith/go-micro-example/internal/platform/secrets"
)

func TestEnvProviderPasses(t *testing.T) {
	t.Setenv("GME_TEST_PRESENT", "value")
	if err := (secrets.EnvProvider{}).Load([]secrets.EnvVar{"GME_TEST_PRESENT"}); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestEnvProviderReportsMissing(t *testing.T) {
	// Prefix unlikely to exist in CI
	const v = "GME_TEST_DOES_NOT_EXIST"
	os.Unsetenv(v)
	err := (secrets.EnvProvider{}).Load([]secrets.EnvVar{secrets.EnvVar(v)})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), v) {
		t.Errorf("error %q does not name the missing var", err)
	}
}

func TestFileProviderReadsAndExports(t *testing.T) {
	dir := t.TempDir()
	const v = "GME_TEST_FROM_FILE"
	if err := os.WriteFile(filepath.Join(dir, "gme_test_from_file"), []byte("rendered-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv(v, "") // ensure unset before
	os.Unsetenv(v)
	t.Cleanup(func() { os.Unsetenv(v) })

	if err := (secrets.FileProvider{Dir: dir}).Load([]secrets.EnvVar{secrets.EnvVar(v)}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := os.Getenv(v); got != "rendered-value" {
		t.Errorf("env got=%q want=%q (trailing newline should be stripped)", got, "rendered-value")
	}
}

func TestFileProviderPreservesInternalWhitespace(t *testing.T) {
	dir := t.TempDir()
	const v = "GME_TEST_INTERNAL_WS"
	// Inner whitespace and a trailing newline; only the trailing
	// newline should be stripped.
	if err := os.WriteFile(filepath.Join(dir, "gme_test_internal_ws"), []byte("a b\nc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Unsetenv(v) })
	if err := (secrets.FileProvider{Dir: dir}).Load([]secrets.EnvVar{secrets.EnvVar(v)}); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(v); got != "a b\nc" {
		t.Errorf("env got=%q want=%q", got, "a b\nc")
	}
}

func TestFileProviderReportsMissing(t *testing.T) {
	dir := t.TempDir()
	err := (secrets.FileProvider{Dir: dir}).Load([]secrets.EnvVar{"GME_DB_PASS", "GME_DB_USER"})
	if err == nil {
		t.Fatal("expected error for missing files")
	}
	for _, want := range []string{"gme_db_pass", "gme_db_user"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention missing file %q", err, want)
		}
	}
}

func TestFileProviderReportsUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows file ACLs don't honor 0o000 the way POSIX permissions do")
	}
	if os.Getuid() == 0 {
		t.Skip("running as root; permission errors don't apply")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "gme_db_pass")
	if err := os.WriteFile(target, []byte("v"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(target, 0o600) }) // let TempDir clean up
	err := (secrets.FileProvider{Dir: dir}).Load([]secrets.EnvVar{"GME_DB_PASS"})
	if err == nil {
		t.Fatal("expected error reading 0000-perm file")
	}
	if !strings.Contains(err.Error(), "unreadable") {
		t.Errorf("error %q does not classify as unreadable", err)
	}
}

func TestLoadFromEnvSelectsByEnvVar(t *testing.T) {
	t.Run("default is env-provider", func(t *testing.T) {
		t.Setenv("GME_SECRETS_PROVIDER", "")
		for _, v := range secrets.Required {
			t.Setenv(string(v), "x")
		}
		if err := secrets.LoadFromEnv(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("file mode requires the directory's contents", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("GME_SECRETS_PROVIDER", "file")
		t.Setenv("GME_SECRETS_DIR", dir)
		err := secrets.LoadFromEnv()
		if err == nil {
			t.Fatal("expected error from empty dir")
		}
		if !strings.Contains(err.Error(), "file-provider") {
			t.Errorf("error %q does not identify the failing provider", err)
		}
	})

	t.Run("unknown provider is rejected", func(t *testing.T) {
		t.Setenv("GME_SECRETS_PROVIDER", "kerblam")
		err := secrets.LoadFromEnv()
		if err == nil || !strings.Contains(err.Error(), "unknown") {
			t.Errorf("expected unknown-provider error, got %v", err)
		}
	})
}

func TestLoadFromEnvIsErrorTyped(t *testing.T) {
	// Sanity check that callers can wrap and inspect.
	t.Setenv("GME_SECRETS_PROVIDER", "kerblam")
	err := secrets.LoadFromEnv()
	if err == nil {
		t.Fatal("expected error")
	}
	wrapped := wrap(err)
	if !errors.Is(wrapped, err) {
		t.Errorf("errors.Is failed across one level of wrapping")
	}
}

type wrapper struct{ inner error }

func (w wrapper) Error() string { return "wrapped: " + w.inner.Error() }
func (w wrapper) Unwrap() error { return w.inner }

func wrap(err error) error { return wrapper{inner: err} }
