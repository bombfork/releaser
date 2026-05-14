# releaser

A CLI and companion GitHub Action to automate project releases on GitHub.

The CLI is a single binary (`releaser`) with four subcommands:

| Command | Purpose |
|---|---|
| `releaser init` | Gather project information and write the configuration file at the repository root |
| `releaser config get/set` | Inspect or modify the configuration |
| `releaser generate` | Generate the GitHub Actions workflows that drive the release process |
| `releaser release [--dry-run]` | Cut a release. Interactive locally, non-interactive in CI. Idempotent on retry |

## Release model

- Every commit on the default branch since the last release belongs to a pending release.
- A single open pull request tracks the pending release: it bumps the project version in the configured locations and carries the draft release notes.
- Merging the pending-release PR triggers the release workflow, which tags, publishes a GitHub release, builds the artifacts, and attaches them.
- The first commit on the default branch after a release re-opens a new pending-release PR.
- Versions are computed from conventional commits (with a configurable mapping of commit types to bump levels). Manual releases accept user input.
- All steps of the release workflow are idempotent: re-running a failed release only performs what is missing.

## Installation

```bash
mise use github:bombfork/releaser
```

## GitHub Action

This repository also ships a composite action that downloads the matching released binary and invokes it from a workflow. Action and CLI share a single release; the action ref and the binary version are always the same.

## Scope of v1

Single-project repositories only. The user provides:

- the build command or script that produces the release artifacts,
- a glob describing which artifact files to attach to the release,
- the list of (file path, regex) locations where the project version string lives,
- optionally, custom commit-type → bump-level overrides.

Monorepos and stack-specific autodetection adapters are future work.
