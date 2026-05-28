#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
cd "$repo_root"

venv_dir="$($repo_root/scripts/ensure-lint-venv.sh)"
markdown_files=()

if [ "$#" -gt 0 ]; then
	markdown_files=("$@")
else
	while IFS= read -r -d '' file; do
		markdown_files+=("$file")
	done < <(git ls-files -z -- '*.md' '*.markdown')
fi

if [ "${#markdown_files[@]}" -eq 0 ]; then
	exit 0
fi

"$venv_dir/bin/pymarkdown" -d md013 scan "${markdown_files[@]}"
"$venv_dir/bin/python" "$repo_root/scripts/lint_markdown_fragments.py" "${markdown_files[@]}"