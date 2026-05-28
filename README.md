# container-setup

`container-setup` releases a `setup` binary that is a small Go CLI for container repositories to generate Podman and Docker Compose files and `.env` files from a `setup.yaml` configuration file and template files. It can be used in interactive or non-interactive mode. After the first run it creates a `setup.json` that can be used as input to easily generate updated files when the template files have been updated.

## Contents 📚

- [Support by Strukturpiloten](#support-by-strukturpiloten-)
- [What it does](#what-it-does-)
- [Using a release in a container project](#using-a-release-in-a-container-project-)
- [The setup.yaml model](#the-setupyaml-model-)
- [The example directory](#the-example-directory-)
- [CLI flags](#cli-flags-%EF%B8%8F)
- [Development](#development-%EF%B8%8F)
- [Contribution](#contribution-)
- [Future Plans](#future-plans-)

## Support by Strukturpiloten 🚀

[Strukturpiloten](https://www.strukturpiloten.de/) is a German consulting team working on process optimization, automation, Linux, networks, and DevOps. We like open systems, calm infrastructure, and tools that teams can actually understand after the first coffee.

Need help with containers, deployment workflows, or the next piece of infrastructure work? [Get in touch](https://www.strukturpiloten.de/kontakt).

## What it does ✨

`container-setup` reads a repository-local `setup.yaml` and can:

- ask interactive questions, or run fully from flags and therefore is also suitable for CI/CD pipelines
- validate choices, required values, conditional values, booleans, integers, and paths
- generate passwords once per run
- copy config assets without overwriting local edits unless `--force` is used
- render Go templates such as `compose.yaml.tmpl`
- render and merge `.env` files while protecting selected keys like passwords from being overwritten
- export a `setup.json` state file so the same setup can be re-run after an update to the templates or `setup.yaml` without having to answer all setup questions again

This repository provides a binary file that container projects can use to generate their files. The example directory shows how a consuming project wires `container-setup` into a Podman/Docker Compose setup.

## Using a release in a container project 📦

Manually clone this project into a sub-directory of your project or create a git submodule:

```sh
git submodule add https://github.com/Strukturpiloten/container-setup
```

## The setup.yaml model 📃

A `setup.yaml` file defines the variables being used by the `setup` binary. The main sections are:

- `variables`: prompts and flags, including defaults, choices, required rules, and conditional visibility
- `computed`: values derived from earlier answers
- `passwords`: generated secrets that can be used in templates and `.env` files
- `directories`: paths that should exist before rendering files
- `assets`: folders or files copied from the project into the generated runtime directory
- `env`: `.env` rendering, default file writing, backups, permissions, and protected keys
- `templates`: Go templates rendered to final files such as `compose.yaml`
- `state`: selected values written to `setup.json` for later reuse
- `messages`: final hints printed after a successful run

Templates use Go's `text/template` syntax. The collected variables, computed values, passwords, and runtime helpers are available as template data.

## The example directory 🧪

The [example](example) directory is a reference setup, not a full container app. It shows how a real project wires `container-setup` into a Podman/Docker Compose setup.

[example/setup.yaml](example/setup.yaml) demonstrates the setup contract:

- namespace, service, stage, data directory, database choice, domain, user IDs, SSL, and reverse-proxy prompts
- conditional values such as MariaDB vs. PostgreSQL and local SSL vs. external reverse proxy
- generated database and admin passwords
- runtime directory creation under `DataDir`
- asset copying with exclusions for `.gitignore` and `.tmpl` files
- `.env` generation with protected keys
- template rendering for `compose.yaml` and an nginx config
- `setup.json` export for future re-runs
- a final message with the exact `podman compose` commands to start the generated project

[example/compose.yaml.tmpl](example/compose.yaml.tmpl) is the matching Compose template. It uses normal Compose environment variables plus setup-time template logic such as:

- adding a reverse-proxy network only when `UseReverseProxy` is true
- choosing either a `mariadb` or `postgresql` service
- sharing generated paths and credentials through the rendered `.env` file

The example intentionally keeps the focus on the setup layer. Files referenced by the sample, such as `.env.tmpl`, `configs/`, and nginx templates, live in the consuming container project.

## CLI flags ⚙️

Global flags:

- `--config`: path to the `setup.yaml` file
- `--project-dir`: repository directory used for relative templates and assets
- `--json`: import values from a generated `setup.json`
- `--force`: overwrite copied config assets that already exist
- `--non-interactive`: do not prompt, missing required values become errors
- `--quiet`: suppress command output
- `--version`: print build metadata

Every variable with a `flag` entry in `setup.yaml` becomes a CLI flag too. In the example, that includes flags like `--domain`, `--database-sql`, `--reverse-proxy`, and `--data-dir`.

## Development 🛠️

Run tests:

```sh
go test ./...
```

Install the executable outside this repository:

```sh
go install ./cmd/setup
```

Install the repository-managed Git hooks once per clone:

```sh
./scripts/install-hooks.sh
```

The pre-commit hook checks staged files only:

- staged Go files are reformatted with `gofmt`, re-staged, and then validated with `go vet ./...`
- staged Markdown files are checked with the repository Markdown lint script
- staged YAML files are checked with the repository YAML lint script

Repository layout:

- [cmd/setup/main.go](cmd/setup/main.go): thin CLI entrypoint for the `setup` executable
- [setup.sh](setup.sh): reusable downloader and runner for consuming projects that use this repository as a submodule
- [setup.go](setup.go): importable setup package at the module root
- [setup_test.go](setup_test.go): regression tests for prompts, state import, env merging, and output generation
- [example](example): reference configuration for consuming container projects

## Contribution 🤝

Issues and pull requests are welcome. If you found a rough edge, have a cleaner setup pattern, or want another container project use case covered, open an issue or PR.

## Future Plans 📜

- Add support for Podman Quadlets by using [Podlet](https://github.com/containers/podlet)
- Add support for Kubernetes that can be used by Kubernetes and Podman
