#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

failures=0

is_git_work_tree() {
    command -v git >/dev/null 2>&1 && git -C "$repo_root" rev-parse --is-inside-work-tree >/dev/null 2>&1
}

is_hygiene_candidate_file() {
    local path="$1"

    case "$path" in
    scripts/check-release-hygiene.sh)
        return 1
        ;;
    esac

    [[ -f "$path" ]]
}

is_generated_artifact_path() {
    local path="$1"

    case "$path" in
    nncode | \
        */nncode | \
        *.test | \
        */*.test | \
        coverage.out | \
        */coverage.out)
        return 0
        ;;
    esac

    return 1
}

collect_repo_files() {
    if is_git_work_tree; then
        local path

        while IFS= read -r -d '' path; do
            if is_hygiene_candidate_file "$path"; then
                printf './%s\n' "$path"
            fi
        done < <(git -C "$repo_root" ls-files -z --cached --others --exclude-standard)
        return
    fi

    find . \
        -path './.git' -prune -o \
        -path './scripts/check-release-hygiene.sh' -prune -o \
        -type f -print
}

collect_generated_artifacts() {
    if is_git_work_tree; then
        local path

        while IFS= read -r -d '' path; do
            if [[ -f "$path" ]] && is_generated_artifact_path "$path"; then
                printf './%s\n' "$path"
            fi
        done < <(git -C "$repo_root" ls-files -z --cached --others --exclude-standard)
        return
    fi

    find . \
        -path './.git' -prune -o \
        \( -name 'nncode' -o -name '*.test' -o -name 'coverage.out' \) -print
}

print_violation() {
    local title="$1"
    local matches="$2"

    if [[ -n "$matches" ]]; then
        printf 'FAIL: %s\n' "$title"
        printf '%s\n\n' "$matches"
        failures=1
    else
        printf 'PASS: %s\n' "$title"
    fi
}

mapfile -t repo_files < <(collect_repo_files)

home_path_matches=""
if ((${#repo_files[@]} > 0)); then
    home_path_matches="$(grep -nH --binary-files=without-match '/home/' "${repo_files[@]}" || true)"
fi
print_violation "repository-owned files must not contain /home/ paths" "$home_path_matches"

markdown_files=()
for repo_file in "${repo_files[@]}"; do
    if [[ "$repo_file" == *.md ]]; then
        markdown_files+=("$repo_file")
    fi
done
absolute_markdown_matches=""
if ((${#markdown_files[@]} > 0)); then
    absolute_markdown_matches="$(grep -nH -E '\]\((/|file://)[^)]+\)' "${markdown_files[@]}" || true)"
fi
print_violation "Markdown files must not contain absolute local links" "$absolute_markdown_matches"

generated_artifact_matches="$(collect_generated_artifacts | sort -u || true)"
print_violation "repository must not contain generated build artifacts (nncode binary, *.test, coverage.out)" "$generated_artifact_matches"

if ((failures != 0)); then
    exit 1
fi

printf 'Release hygiene checks passed.\n'
