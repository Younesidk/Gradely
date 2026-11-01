package config

import "testing"

func TestValidateBaseURL(t *testing.T) {
	valid := []string{
		"https://gradly.younesidk.dev",
		"http://localhost:8080",
		"http://127.0.0.1:8088",
		"http://[::1]:8080",
		"https://my-app.example.co.uk",
	}
	for _, raw := range valid {
		if err := validateBaseURL(raw); err != nil {
			t.Errorf("%q: unexpected error: %v", raw, err)
		}
	}

	invalid := []string{
		"",
		"gradly.younesidk.dev",              // no scheme
		"ftp://gradly.younesidk.dev",        // wrong scheme
		"https://gradely.younesidk.dev.###", // stray '#' / trailing dot
		"https://gradly.younesidk.dev # production", // inline comment
		"\"https://gradly.younesidk.dev\"",          // quoted
		"https://gradly.younesidk.dev/app",          // path
		"https://gradly.younesidk.dev?x=1",          // query
		"https://user:pass@gradly.younesidk.dev",    // credentials
		"https://gradly..younesidk.dev",             // empty label
		"https://-gradly.younesidk.dev",             // leading hyphen
		"https://gradly_app.younesidk.dev",          // underscore
		"https://gradly.younesidk.dev:99999",        // bad port
		"https://",                                  // no host
	}
	for _, raw := range invalid {
		if err := validateBaseURL(raw); err == nil {
			t.Errorf("%q: expected an error", raw)
		}
	}
}

func TestFromEnvTrimsTrailingSlash(t *testing.T) {
	t.Setenv("APP_BASE_URL", "https://gradly.younesidk.dev/")
	cfg := FromEnv()
	if cfg.BaseURL != "https://gradly.younesidk.dev" {
		t.Fatalf("BaseURL = %q", cfg.BaseURL)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}
