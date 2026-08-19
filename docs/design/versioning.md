# Versioning & Deprecation Policy

> Status: effective since v0.3.0
> Scope: ARES Runtime's public Go API surface (packages under `internal/` consumed
> by `cmd/`, `sdk/`, `api/`), CLI commands, HTTP endpoints, and the config file
> schema.

## Version numbers

- Follow [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html):
  `MAJOR.MINOR.PATCH`.
- **Single source of truth**: the `VERSION` file at the repository root.
  `make build` / `make build-all` / `make install-cli` read it and inject it via
  `-X main.version=$(VERSION)`, so `ares version` reports the real build version.
- Release override: `make build TAG_VERSION=v0.4.0` (a git tag may feed the same
  value in CI).
- `git tag` marks releases only; untagged dev builds report the `VERSION` content
  (e.g. `0.3.0`).

## Change classification

| Category | Examples | Version impact |
|---|---|---|
| Breaking | Removing/renaming exported symbols, HTTP path changes, config field removal, CLI flag removal | MAJOR (or MINOR while 0.x) |
| Feature | New exported symbols/endpoints/config fields (defaults stay compatible) | MINOR |
| Fix | Bug fixes, internal refactors, docs | PATCH |

> While 0.x: MINOR may contain breaking changes, but they **must** be flagged
> with a `**Breaking**` prefix in the CHANGELOG `[Unreleased]` section.

## Deprecation flow

1. **Mark**: keep the deprecated symbol but add a `Deprecated:` comment naming the
   replacement and the removal version
   (`Deprecated: use X instead. Will be removed in v0.5.0.`).
2. **Transition**: keep it for at least one MINOR release (deprecate now → remove
   at the next MINOR).
3. **Remove**: record `**Breaking**: removed Y (deprecated since v0.3.0)` in the
   CHANGELOG of the removal version.
4. **HTTP endpoints**: a deprecated endpoint keeps its response body and sets a
   `Deprecation` response header with the removal version; prefer new paths, and
   redirect (301) old paths during the transition.

## Config schema compatibility

- New fields must have defaults; old configs (missing fields) must keep parsing
  (guaranteed by the `ares_config` validator).
- Removed fields are deprecated in `[Unreleased]` first, removed at the next MINOR.
- Environment variables (`ARES_*`) follow the same rules as YAML fields; deprecating
  an `ARES_*` var must update the docs in the same change.

## CHANGELOG discipline

- Every user-visible change must be recorded in `CHANGELOG.md` under
  `[Unreleased]` (Keep a Changelog format).
- Categories: `### Added` / `### Changed` / `### Fixed` / `### Breaking`.
- On release, rename `[Unreleased]` to `[X.Y.Z] - YYYY-MM-DD` and open a new
  `[Unreleased]`.
