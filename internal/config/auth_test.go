package config_test

import (
	"strings"
	"testing"

	"github.com/bombfork/releaser/internal/config"
)

func TestDefaultRelease_AuthDefaultsToDefaultToken(t *testing.T) {
	d := config.DefaultRelease()
	if d.Auth.Mode != config.AuthModeDefaultToken {
		t.Errorf("Auth.Mode = %q, want %q", d.Auth.Mode, config.AuthModeDefaultToken)
	}
	if d.Auth.App != nil {
		t.Errorf("Auth.App = %+v, want nil", d.Auth.App)
	}
	if d.Auth.Token != nil {
		t.Errorf("Auth.Token = %+v, want nil", d.Auth.Token)
	}
}

func TestRelease_WithDefaults_NormalizesEmptyAuthMode(t *testing.T) {
	got := config.Release{}.WithDefaults()
	if got.Auth.Mode != config.AuthModeDefaultToken {
		t.Errorf("Auth.Mode = %q, want %q", got.Auth.Mode, config.AuthModeDefaultToken)
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
			name: "default_token valid",
			release: config.Release{
				BotIdentity: defaultBot,
				Auth:        config.Auth{Mode: config.AuthModeDefaultToken},
			},
		},
		{
			name: "default_token rejects app",
			release: config.Release{
				BotIdentity: defaultBot,
				Auth: config.Auth{
					Mode: config.AuthModeDefaultToken,
					App:  &config.AuthApp{AppIDVar: "X"},
				},
			},
			wantErr:  true,
			errMatch: "release.auth.app must be unset",
		},
		{
			name: "default_token rejects token",
			release: config.Release{
				BotIdentity: defaultBot,
				Auth: config.Auth{
					Mode:  config.AuthModeDefaultToken,
					Token: &config.AuthToken{Secret: "FOO"},
				},
			},
			wantErr:  true,
			errMatch: "release.auth.token must be unset",
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
