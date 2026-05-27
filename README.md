# container-setup

`container-setup` is a reusable Go CLI that bootstraps container projects from a repository-local `setup.yaml` definition.

It reads prompts, flags, defaults, generated passwords, directory creation, copied assets, rendered templates, `.env` merge rules, and `setup.json` state export from the target project's configuration.

The module root contains the importable `setup` package. The installable CLI lives under `cmd/setup`.

## Requirements

- Go 1.22 or newer

## Development

Run tests:

```sh
go test ./...
```

Install the executable outside this repository:

```sh
go install ./cmd/setup
```

Run against a project that contains a `setup.yaml` file:

```sh
go run ./cmd/setup --config /absolute/path/to/setup.yaml
```

Or, from inside a configured project repository:

```sh
go run ./cmd/setup
```

Print build metadata:

```sh
go run ./cmd/setup --version
```

## CI/CD

Pull requests and pushes to `main` run GitHub Actions checks for module tidiness, formatting, `go vet`, race-enabled tests, and a command build. Configure branch protection in GitHub so the CI workflow is required before merging.

GitHub-hosted Alpine runners are not available in Actions, so the workflows intentionally use `ubuntu-latest`. If Alpine compatibility becomes important later, add a Linux container job on top of the Ubuntu runner or use a self-hosted Alpine runner.

Renovate is configured in `.github/renovate.json` to update Go modules, GitHub Actions, and the explicit Go version in `go.mod`. Go versions are restricted to explicit stable patch releases such as `1.26.3`.

The release workflow is manually triggered from GitHub Actions. Provide a Go module SemVer tag such as `v1.0.0`; the workflow validates the tag, runs the same quality checks, builds cross-platform `setup` binaries, embeds the version metadata with `-ldflags`, packages archives, generates SHA-256 checksums, and publishes all of them on a GitHub Release.

Each release includes both direct binary assets and archives. Examples: `setup_v1.0.0_linux_amd64`, `setup_v1.0.0_windows_amd64.exe`, `setup_v1.0.0_linux_amd64.tar.gz`, and `setup_v1.0.0_checksums.txt`. Other repositories can download a direct binary asset and place or rename it as `setup` in their own `deps` directory.

Release versions are not incremented automatically by this setup. The manual workflow uses the SemVer tag you provide. Automatic version bumps can be added later with a release automation tool if you want releases derived from commit history.

## Git Hooks

Install the repository-managed Git hooks once per clone:

```sh
./scripts/install-hooks.sh
```

The pre-commit hook autoformats staged Go files with `gofmt`, re-stages them, and runs `go vet ./...` before the commit is created.

## Repository Layout

- `cmd/setup/main.go`: thin CLI entrypoint for the `setup` executable
- `setup.go`: importable setup package at the module root
- `setup_test.go`: regression tests for prompts, state import, env merging, and output generation
- `go.mod` / `go.sum`: module definition and dependencies

## Notes

- The repository tracks source only. Prefer `go run ./cmd/setup` during development or `go install ./cmd/setup` when you want an installed executable.
- The generated runtime files belong to the consuming project, not to this repository.
