package config_test

import (
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/config"
)

func TestDefaultRelease_AuthHasNoDefault(t *testing.T) {
	d := config.DefaultRelease()
	if d.Auth.Mode != "" {
		t.Errorf("Auth.Mode = %q, want empty (no default — user must set github_app)", d.Auth.Mode)
	}
	if d.Auth.App != nil {
		t.Errorf("Auth.App = %+v, want nil", d.Auth.App)
	}
}

func TestRelease_WithDefaults_LeavesEmptyAuthMode(t *testing.T) {
	got := config.Release{}.WithDefaults()
	if got.Auth.Mode != "" {
		t.Errorf("Auth.Mode = %q, want empty (no default applied)", got.Auth.Mode)
	}
}

func TestRelease_ValidateAuth(t *testing.T) {
	cases := []struct {
		name     string
		release  config.Release
		wantErr  bool
		errMatch string
	}{
		{
			name: "github_app valid",
			release: config.Release{
				Auth: config.Auth{
					Mode: config.AuthModeGitHubApp,
					App: &config.AuthApp{
						AppIDVar:          "RELEASER_APP_ID",
						InstallationIDVar: "RELEASER_APP_INSTALLATION_ID",
						PrivateKeySecret:  "RELEASER_APP_PRIVATE_KEY",
					},
				},
			},
		},
		{
			name: "github_app missing app block",
			release: config.Release{
				Auth: config.Auth{Mode: config.AuthModeGitHubApp},
			},
			wantErr:  true,
			errMatch: "requires release.auth.app",
		},
		{
			name: "github_app missing private_key_secret",
			release: config.Release{
				Auth: config.Auth{
					Mode: config.AuthModeGitHubApp,
					App: &config.AuthApp{
						AppIDVar:          "X",
						InstallationIDVar: "Y",
					},
				},
			},
			wantErr:  true,
			errMatch: "private_key_secret",
		},
		{
			name: "empty mode is required",
			release: config.Release{
				Auth: config.Auth{},
			},
			wantErr:  true,
			errMatch: "release.auth.mode is required",
		},
		{
			name: "default_token rejected with migration error",
			release: config.Release{
				Auth: config.Auth{Mode: "default_token"},
			},
			wantErr:  true,
			errMatch: "default_token is no longer supported",
		},
		{
			name: "token rejected with migration error",
			release: config.Release{
				Auth: config.Auth{Mode: "token"},
			},
			wantErr:  true,
			errMatch: "removed in v0.14.0",
		},
		{
			name: "unknown mode",
			release: config.Release{
				Auth: config.Auth{Mode: "ssh-key"},
			},
			wantErr:  true,
			errMatch: "not a valid mode",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.release.ValidateAuth()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ValidateAuth() = nil, want error containing %q", tc.errMatch)
				}
				if !strings.Contains(err.Error(), tc.errMatch) {
					t.Errorf("ValidateAuth() = %v, want error containing %q", err, tc.errMatch)
				}
				return
			}
			if err != nil {
				t.Errorf("ValidateAuth() = %v, want nil", err)
			}
		})
	}
}
