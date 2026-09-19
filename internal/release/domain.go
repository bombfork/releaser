package release

import "os"

// IsCIMode reports whether releaser is running inside GitHub Actions.
// The two execution domains behave differently by design: CI creates
// commits via the GitHub API with the App installation token (signed
// as the App bot), while local runs commit through the user's own git.
func IsCIMode() bool {
	return os.Getenv("GITHUB_ACTIONS") == "true"
}
