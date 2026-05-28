#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
cd "$repo_root"

venv_dir="$($repo_root/scripts/ensure-lint-venv.sh)"
yaml_files=()

if [ "$#" -gt 0 ]; then
	yaml_files=("$@")
else
	while IFS= read -r -d '' file; do
		yaml_files+=("$file")
	done < <(git ls-files -z -- '*.yml' '*.yaml')
fi

if [ "${#yaml_files[@]}" -eq 0 ]; then
	exit 0
fi

"$venv_dir/bin/yamllint" --strict --config-file "$repo_root/.yamllint.yml" "${yaml_files[@]}"