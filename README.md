# releaser

A CLI and companion GitHub Action to automate project releases on GitHub.

The CLI is a single binary (`releaser`) with four subcommands:

| Command | Purpose |
|---|---|
| `releaser init` | Gather project information and write the configuration file at the repository root |
| `releaser config get/set` | Inspect or modify scalar configuration values |
| `releaser config add/rm/list` | Manage entries in list-valued fields (artifacts, targets, version locations) |
| `releaser generate` | Generate the GitHub Actions workflows that drive the release process |
| `releaser release [--dry-run]` | Cut a release. Interactive locally, non-interactive in CI. Idempotent on retry |

## Release model

- Every commit on the default branch since the last release belongs to a pending release.
- A single open pull request tracks the pending release: it bumps the project version in the configured locations and carries the draft release notes.
- Merging the pending-release PR makes the same workflow publish the release: tag, GitHub release, build, asset upload.
- The first commit on the default branch after a release re-opens a new pending-release PR.
- Versions are computed from conventional commits (with a configurable mapping of commit types to bump levels). Manual releases accept user input.
- All steps of the release workflow are idempotent: re-running a failed release only performs what is missing.

A single generated workflow (`.github/workflows/releaser.yml` by default) drives both halves. On every push to the default branch — and on `workflow_dispatch` — a first step inspects the head commit and runs either `releaser release prepare` or `releaser release publish`. The detection signal is the head commit message: a `chore(release): prepare vX.Y.Z` prefix or a default merge-commit `from <owner>/<release-branch>` substring routes to publish; everything else routes to prepare. `workflow_dispatch` exposes a `mode` input (`auto` / `prepare` / `publish`) to override auto-detection when needed.

The workflow pins the action ref it consumes (`uses: bombfork/releaser@<commit-sha> # vX.Y.Z`). Releaser's own workflows and the templates it generates pin every `uses:` reference to a commit SHA — with the tag in a trailing comment — to reduce the blast radius of upstream action compromises. The action-version line is intentionally not in `adapter.version.locations`, because bumping it as part of the release PR would have the next release try to consume itself before publishing. After each release ships, bump the SHA (and comment) by hand to the just-released tag.

### Commit signing

Commits produced by `releaser release prepare` and `releaser bootstrap` are
created through GitHub's Git Data API (blobs → tree → commit → ref). API-created
commits are signed by GitHub's `web-flow` key regardless of the token used to
authenticate, so projects whose default branch requires signed commits accept
the resulting pending-release PR with no additional configuration. There is no
user-facing setting; signing is implicit.

## Installation

```bash
mise use github:bombfork/releaser
```

## GitHub Action

This repository also ships a composite action that downloads the matching released binary and invokes it from a workflow. Action and CLI share a single release; the action ref and the binary version are always the same.

## Scope of v1

Single-project repositories only. The user provides:

- the build command or script that produces the release artifacts,
- one or more globs describing which artifact files to attach to the release (the union of matches is uploaded; duplicates are deduplicated),
- the list of (file path, regex) locations where the project version string lives,
- optionally, custom commit-type → bump-level overrides.

The `artifacts` field is a YAML sequence:

```yaml
adapter:
  type: go
  build:
    command: ./scripts/package.sh
    artifacts:
      - dist/releaser_*.tar.gz
      - dist/checksums.txt
```

Monorepos and stack-specific autodetection adapters are future work.
