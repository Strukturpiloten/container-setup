#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
venv_dir="$repo_root/.cache/lint-venv"
project_file="$repo_root/pyproject.toml"
lock_file="$repo_root/uv.lock"
lint_project_dir="$repo_root/.cache/lint-project"
stamp_file="$venv_dir/.lint-project.sha256"

if ! command -v uv >/dev/null 2>&1; then
	echo "uv is required to bootstrap the lint environment." >&2
	exit 1
fi

if [ ! -f "$project_file" ]; then
	echo "Missing lint project file: $project_file" >&2
	exit 1
fi

if [ ! -f "$lock_file" ]; then
	echo "Missing lock file: $lock_file" >&2
	exit 1
fi

hash_file() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
		return
	fi

	shasum -a 256 "$1" | awk '{print $1}'
}

combined_hash() {
	if command -v sha256sum >/dev/null 2>&1; then
		cat "$@" | sha256sum | awk '{print $1}'
		return
	fi

	cat "$@" | shasum -a 256 | awk '{print $1}'
}

project_hash="$(combined_hash "$project_file" "$lock_file")"

if [ ! -x "$venv_dir/bin/python" ]; then
	uv venv --allow-existing --python python3 "$venv_dir" >&2
fi

installed_hash=""
if [ -f "$stamp_file" ]; then
	installed_hash="$(cat "$stamp_file")"
fi

if [ "$installed_hash" != "$project_hash" ]; then
	mkdir -p "$lint_project_dir"
	cp "$project_file" "$lint_project_dir/pyproject.toml"
	cp "$lock_file" "$lint_project_dir/uv.lock"
	env VIRTUAL_ENV="$venv_dir" PATH="$venv_dir/bin:$PATH" UV_LINK_MODE=copy \
		uv sync --project "$lint_project_dir" --active --frozen --no-install-project >&2
	printf '%s\n' "$project_hash" > "$stamp_file"
fi

printf '%s\n' "$venv_dir"