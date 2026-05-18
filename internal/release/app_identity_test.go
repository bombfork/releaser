package release_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/release"
)

func TestAppBotIdentity_ResolvesFromAppEndpoints(t *testing.T) {
	pemKey := testRSAPEM(t)

	var seenAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("/app", func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, `{"slug":"my-app","name":"My App"}`)
	})
	mux.HandleFunc("/users/my-app[bot]", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, `{"id":987654,"login":"my-app[bot]"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	id, err := release.AppBotIdentity(context.Background(), release.AppBotIdentityEnv{
		AppID:         "1234",
		PrivateKeyPEM: pemKey,
		APIURL:        srv.URL,
	}, srv.Client())
	if err != nil {
		t.Fatalf("AppBotIdentity: %v", err)
	}

	wantName := "my-app[bot]"
	wantEmail := "987654+my-app[bot]@users.noreply.github.com"
	if id.Name != wantName {
		t.Errorf("Name = %q, want %q", id.Name, wantName)
	}
	if id.Email != wantEmail {
		t.Errorf("Email = %q, want %q", id.Email, wantEmail)
	}
	if !strings.HasPrefix(seenAuth, "Bearer ") {
		t.Errorf("expected Bearer JWT on GET /app, got %q", seenAuth)
	}
}

func TestAppBotIdentity_ErrorsWhenAppIDMissing(t *testing.T) {
	_, err := release.AppBotIdentity(context.Background(), release.AppBotIdentityEnv{
		PrivateKeyPEM: "x",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "GH_TKN_APP_ID") {
		t.Fatalf("err = %v, want GH_TKN_APP_ID error", err)
	}
}

func TestAppBotIdentity_ErrorsWhenPrivateKeyMissing(t *testing.T) {
	_, err := release.AppBotIdentity(context.Background(), release.AppBotIdentityEnv{
		AppID: "1234",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "GH_TKN_APP_PRIVATE_KEY") {
		t.Fatalf("err = %v, want GH_TKN_APP_PRIVATE_KEY error", err)
	}
}

func TestAppBotIdentity_PropagatesAPIError(t *testing.T) {
	pemKey := testRSAPEM(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"bad credentials"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := release.AppBotIdentity(context.Background(), release.AppBotIdentityEnv{
		AppID:         "1234",
		PrivateKeyPEM: pemKey,
		APIURL:        srv.URL,
	}, srv.Client())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want unauthorized error", err)
	}
}

// testRSAPEM generates a fresh RSA-2048 private key and returns it
// PEM-encoded. A new key per test keeps fixtures from leaking into the
// repo and avoids any chance of accidental reuse against the real API.
func testRSAPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}
	return string(pem.EncodeToMemory(block))
}
