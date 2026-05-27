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

## Repository Layout

- `cmd/setup/main.go`: thin CLI entrypoint for the `setup` executable
- `setup.go`: importable setup package at the module root
- `setup_test.go`: regression tests for prompts, state import, env merging, and output generation
- `go.mod` / `go.sum`: module definition and dependencies

## Notes

- The repository tracks source only. Prefer `go run ./cmd/setup` during development or `go install ./cmd/setup` when you want an installed executable.
- The generated runtime files belong to the consuming project, not to this repository.
