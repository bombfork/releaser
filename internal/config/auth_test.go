package config_test

import (
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/config"
)

func TestDefaultRelease_AuthHasNoDefault(t *testing.T) {
	d := config.DefaultRelease()
	if d.Auth.Mode != "" {
		t.Errorf("Auth.Mode = %q, want empty (no default — user must pick github_app or token)", d.Auth.Mode)
	}
	if d.Auth.App != nil {
		t.Errorf("Auth.App = %+v, want nil", d.Auth.App)
	}
	if d.Auth.Token != nil {
		t.Errorf("Auth.Token = %+v, want nil", d.Auth.Token)
	}
}

func TestRelease_WithDefaults_LeavesEmptyAuthMode(t *testing.T) {
	got := config.Release{}.WithDefaults()
	if got.Auth.Mode != "" {
		t.Errorf("Auth.Mode = %q, want empty (no default applied)", got.Auth.Mode)
	}
}

func TestRelease_ValidateAuth(t *testing.T) {
	customBot := config.BotIdentity{
		Name:  "myorg-releaser[bot]",
		Email: "12345+myorg-releaser[bot]@users.noreply.github.com",
	}
	defaultBot := config.DefaultRelease().BotIdentity

	cases := []struct {
		name     string
		release  config.Release
		wantErr  bool
		errMatch string
	}{
		{
			name: "github_app valid",
			release: config.Release{
				BotIdentity: defaultBot,
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
				BotIdentity: defaultBot,
				Auth:        config.Auth{Mode: config.AuthModeGitHubApp},
			},
			wantErr:  true,
			errMatch: "requires release.auth.app",
		},
		{
			name: "github_app missing private_key_secret",
			release: config.Release{
				BotIdentity: defaultBot,
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
			name: "github_app rejects token block",
			release: config.Release{
				BotIdentity: defaultBot,
				Auth: config.Auth{
					Mode:  config.AuthModeGitHubApp,
					App:   &config.AuthApp{AppIDVar: "X", InstallationIDVar: "Y", PrivateKeySecret: "Z"},
					Token: &config.AuthToken{Secret: "FOO"},
				},
			},
			wantErr:  true,
			errMatch: "release.auth.token must be unset",
		},
		{
			name: "github_app rejects bot_identity override",
			release: config.Release{
				BotIdentity: customBot,
				Auth: config.Auth{
					Mode: config.AuthModeGitHubApp,
					App:  &config.AuthApp{AppIDVar: "X", InstallationIDVar: "Y", PrivateKeySecret: "Z"},
				},
			},
			wantErr:  true,
			errMatch: "bot_identity must not be set",
		},
		{
			name: "token valid",
			release: config.Release{
				BotIdentity: customBot,
				Auth: config.Auth{
					Mode:  config.AuthModeToken,
					Token: &config.AuthToken{Secret: "RELEASER_GH_TOKEN"},
				},
			},
		},
		{
			name: "token missing secret",
			release: config.Release{
				BotIdentity: customBot,
				Auth:        config.Auth{Mode: config.AuthModeToken},
			},
			wantErr:  true,
			errMatch: "requires release.auth.token.secret",
		},
		{
			name: "token rejects app block",
			release: config.Release{
				BotIdentity: customBot,
				Auth: config.Auth{
					Mode:  config.AuthModeToken,
					App:   &config.AuthApp{AppIDVar: "X"},
					Token: &config.AuthToken{Secret: "FOO"},
				},
			},
			wantErr:  true,
			errMatch: "release.auth.app must be unset",
		},
		{
			name: "token requires explicit bot identity",
			release: config.Release{
				BotIdentity: defaultBot,
				Auth: config.Auth{
					Mode:  config.AuthModeToken,
					Token: &config.AuthToken{Secret: "FOO"},
				},
			},
			wantErr:  true,
			errMatch: "bot_identity must be set explicitly",
		},
		{
			name: "empty mode is required",
			release: config.Release{
				BotIdentity: defaultBot,
				Auth:        config.Auth{},
			},
			wantErr:  true,
			errMatch: "release.auth.mode is required",
		},
		{
			name: "default_token rejected with migration error",
			release: config.Release{
				BotIdentity: defaultBot,
				Auth:        config.Auth{Mode: "default_token"},
			},
			wantErr:  true,
			errMatch: "default_token is no longer supported",
		},
		{
			name: "unknown mode",
			release: config.Release{
				BotIdentity: defaultBot,
				Auth:        config.Auth{Mode: "ssh-key"},
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
