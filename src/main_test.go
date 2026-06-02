package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClassifyProcessMaster(t *testing.T) {
	role, workerPool, masterConfig := classifyProcess("php-fpm: master process (/etc/php74w/php-fpm.gazprom.conf)")
	if role != "master" {
		t.Fatalf("role = %q, want %q", role, "master")
	}
	if workerPool != "" {
		t.Fatalf("workerPool = %q, want empty", workerPool)
	}
	if masterConfig != "/etc/php74w/php-fpm.gazprom.conf" {
		t.Fatalf("masterConfig = %q, want %q", masterConfig, "/etc/php74w/php-fpm.gazprom.conf")
	}
}

func TestClassifyProcessWorker(t *testing.T) {
	role, workerPool, masterConfig := classifyProcess("php-fpm: pool gazprom-php74w.conf")
	if role != "worker" {
		t.Fatalf("role = %q, want %q", role, "worker")
	}
	if workerPool != "gazprom-php74w.conf" {
		t.Fatalf("workerPool = %q, want %q", workerPool, "gazprom-php74w.conf")
	}
	if masterConfig != "" {
		t.Fatalf("masterConfig = %q, want empty", masterConfig)
	}
}

func TestEscapeLabelValue(t *testing.T) {
	got := escapeLabelValue(`a\b"c
d`)
	want := `a\\b\"c\nd`
	if got != want {
		t.Fatalf("escapeLabelValue = %q, want %q", got, want)
	}
}

func TestAuthMiddlewareDisabled(t *testing.T) {
	prev := appConfig
	t.Cleanup(func() { appConfig = prev })
	appConfig = defaultConfig()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAuthMiddlewareEnabled(t *testing.T) {
	prev := appConfig
	t.Cleanup(func() { appConfig = prev })
	appConfig = defaultConfig()
	appConfig.BasicAuth = BasicAuthConfig{Username: "metrics", Password: "secret"}

	handler := authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("missing auth", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("valid auth", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.SetBasicAuth("metrics", "secret")
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
	})
}

func TestValidateConfigBasicAuthPair(t *testing.T) {
	if err := validateConfig(defaultConfig()); err != nil {
		t.Fatalf("validateConfig(default) returned error: %v", err)
	}

	cfg := defaultConfig()
	cfg.BasicAuth.Username = "metrics"
	if err := validateConfig(cfg); err == nil {
		t.Fatalf("validateConfig should fail when only username is set")
	}
}
