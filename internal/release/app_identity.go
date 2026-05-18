package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// AppBotIdentityEnv carries the environment variables the App identity
// lookup reads at runtime. They mirror what `action.yml` injects in
// github_app mode (compatible with bombfork/gh-token-go's defaults).
//
// Exposed as a struct (rather than reading os.Getenv directly) so tests
// can drive the lookup without environment mutation.
type AppBotIdentityEnv struct {
	AppID         string // GH_TKN_APP_ID
	PrivateKeyPEM string // GH_TKN_APP_PRIVATE_KEY
	APIURL        string // GH_TKN_API_URL (defaults to https://api.github.com)
}

// ReadAppBotIdentityEnv returns the lookup inputs from the standard
// gh-token-go env vars.
func ReadAppBotIdentityEnv() AppBotIdentityEnv {
	return AppBotIdentityEnv{
		AppID:         os.Getenv("GH_TKN_APP_ID"),
		PrivateKeyPEM: os.Getenv("GH_TKN_APP_PRIVATE_KEY"),
		APIURL:        os.Getenv("GH_TKN_API_URL"),
	}
}

// AppBotIdentity resolves the git author/committer identity for a
// GitHub App installation. It signs a JWT with the App's PEM key,
// fetches the App slug via GET /app, then resolves the bot user's
// numeric ID via GET /users/<slug>[bot]. The bot user ID — NOT the App
// ID — is the leading number in the noreply email GitHub recognizes.
//
// httpClient is optional; when nil, http.DefaultClient with a 10s
// timeout is used. The function is intended to be called once per CLI
// invocation; callers should cache the result into cfg.Release.BotIdentity.
func AppBotIdentity(ctx context.Context, env AppBotIdentityEnv, httpClient *http.Client) (Identity, error) {
	if env.AppID == "" {
		return Identity{}, errors.New("GH_TKN_APP_ID is not set")
	}
	if env.PrivateKeyPEM == "" {
		return Identity{}, errors.New("GH_TKN_APP_PRIVATE_KEY is not set")
	}
	apiURL := env.APIURL
	if apiURL == "" {
		apiURL = "https://api.github.com"
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	appIDInt, err := strconv.Atoi(env.AppID)
	if err != nil {
		return Identity{}, fmt.Errorf("parse GH_TKN_APP_ID: %w", err)
	}
	jwtToken, err := signAppJWT(env.PrivateKeyPEM, appIDInt)
	if err != nil {
		return Identity{}, fmt.Errorf("sign app jwt: %w", err)
	}

	slug, err := fetchAppSlug(ctx, httpClient, apiURL, jwtToken)
	if err != nil {
		return Identity{}, fmt.Errorf("fetch app slug: %w", err)
	}
	botLogin := slug + "[bot]"
	botUserID, err := fetchBotUserID(ctx, httpClient, apiURL, botLogin)
	if err != nil {
		return Identity{}, fmt.Errorf("fetch bot user id for %s: %w", botLogin, err)
	}
	return Identity{
		Name:  botLogin,
		Email: fmt.Sprintf("%d+%s@users.noreply.github.com", botUserID, botLogin),
	}, nil
}

// signAppJWT mints a short-lived JWT signed with the App's PEM key.
// The 5-minute expiry follows GitHub's recommendation (max 10 minutes).
func signAppJWT(pemKey string, appID int) (string, error) {
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iat": now.Add(-30 * time.Second).Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
		"iss": appID,
	})
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(pemKey))
	if err != nil {
		return "", fmt.Errorf("parse PEM: %w", err)
	}
	signed, err := tok.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	return signed, nil
}

func fetchAppSlug(ctx context.Context, c *http.Client, apiURL, jwtToken string) (string, error) {
	var resp struct {
		Slug string `json:"slug"`
	}
	if err := getJSON(ctx, c, apiURL+"/app", "Bearer "+jwtToken, &resp); err != nil {
		return "", err
	}
	if resp.Slug == "" {
		return "", errors.New("GET /app returned empty slug")
	}
	return resp.Slug, nil
}

func fetchBotUserID(ctx context.Context, c *http.Client, apiURL, botLogin string) (int64, error) {
	var resp struct {
		ID int64 `json:"id"`
	}
	if err := getJSON(ctx, c, apiURL+"/users/"+botLogin, "", &resp); err != nil {
		return 0, err
	}
	if resp.ID == 0 {
		return 0, fmt.Errorf("GET /users/%s returned zero id", botLogin)
	}
	return resp.ID, nil
}

// getJSON issues a GET and decodes the JSON response. authHeader is the
// full value to send under Authorization (e.g. "Bearer <jwt>") — leave
// empty for unauthenticated requests.
func getJSON(ctx context.Context, c *http.Client, url, authHeader string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, body)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
