# pgxotel

[![CI](https://github.com/b0r1ssh/pgxotel/actions/workflows/go-test.yml/badge.svg)](https://github.com/b0r1ssh/pgxotel/actions/workflows/go-test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/b0r1ssh/pgxotel.svg)](https://pkg.go.dev/github.com/b0r1ssh/pgxotel)
[![codecov](https://codecov.io/gh/b0r1ssh/pgxotel/graph/badge.svg?token=7ZA7WM97TV)](https://codecov.io/gh/b0r1ssh/pgxotel)

## Release workflow for contributors

Merging a pull request into `main` automatically creates a Git tag and a
GitHub release. Before merging, use at most one of these labels to select the
semantic version increment:

- `patch`: bug fixes and other backward-compatible changes (`v1.2.3` to
	`v1.2.4`).
- `minor`: new backward-compatible functionality (`v1.2.3` to `v1.3.0`).
- `major`: breaking API changes (`v1.2.3` to `v2.0.0`).

When no release label is present, the workflow uses `patch`. Do not combine
`patch`, `minor`, and `major` labels on one pull request; the release workflow
will fail if more than one is present.

Use the `skip-release` label for documentation, CI, or other changes that do
not need a new module version. When a pull request with this label is merged,
the publish workflow skips the release entirely.
