#!/usr/bin/env bash

# This script must be sourced so that the exported paths remain available in
# the caller's shell.  The subshell check works in both Bash and zsh.
if ! (return 0 2>/dev/null); then
    echo "Error: this script must be sourced:" >&2
    echo "  source scripts/0_setup.sh" >&2
    exit 1
fi

_nbwalker_setup_worktree() {
    local project_root="$1"
    local worktree_root="$2"
    local branch="$3"
    local commit="$4"
    local destination="${worktree_root}/${branch}"
    local current_commit

    if ! git -C "${project_root}" cat-file -e "${commit}^{commit}" 2>/dev/null; then
        echo "Error: commit ${commit} for ${branch} does not exist locally." >&2
        echo "Fetch the corresponding branch, then source this script again." >&2
        return 1
    fi

    if [ -e "${destination}" ]; then
        if [ ! -d "${destination}" ] || \
           ! git -C "${destination}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
            echo "Error: ${destination} exists but is not a Git worktree." >&2
            return 1
        fi

        current_commit="$(git -C "${destination}" rev-parse HEAD)" || return 1
        if [ "${current_commit}" != "${commit}" ]; then
            echo "Error: ${destination} is at ${current_commit}, expected ${commit}." >&2
            return 1
        fi

        echo "Already ready: ${branch} (${commit})"
        return 0
    fi

    echo "Creating ${destination} from ${branch} at ${commit} ..."
    git -C "${project_root}" worktree add --detach "${destination}" "${commit}"
}

_nbwalker_setup_main() {
    local git_root

    if ! command -v go >/dev/null 2>&1; then
        echo "Error: Go is not installed or is not available in PATH." >&2
        return 1
    fi

    echo "Go environment:"
    go version || return 1
    echo "  GOROOT=$(go env GOROOT)" || return 1
    echo "  GOPATH=$(go env GOPATH)" || return 1

    # The setup directory is always the directory from which this script is sourced.
    PROJECT_ROOT="$(pwd -P)" || return 1

    if ! git -C "${PROJECT_ROOT}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
        echo "Error: ${PROJECT_ROOT} is not inside a Git repository." >&2
        return 1
    fi

    git_root="$(git -C "${PROJECT_ROOT}" rev-parse --show-toplevel)" || return 1
    if [ "${PROJECT_ROOT}" != "${git_root}" ]; then
        echo "Error: source this script from the repository root: ${git_root}" >&2
        return 1
    fi

    WORKTREE_ROOT="${PROJECT_ROOT}/.AE/worktree"
    mkdir -p "${WORKTREE_ROOT}" || return 1

    _nbwalker_setup_worktree "${PROJECT_ROOT}" "${WORKTREE_ROOT}" \
        "AE-NBWalker" "8837da7dda5591837877d20d4ad51b042c988aad" || return 1
    _nbwalker_setup_worktree "${PROJECT_ROOT}" "${WORKTREE_ROOT}" \
        "AE-SoftWalker" "b2e6c8ebf941756ce881a18b8a8baac6b577ac00" || return 1
    _nbwalker_setup_worktree "${PROJECT_ROOT}" "${WORKTREE_ROOT}" \
        "AE-SnakeByte" "191a5b0d32211eefed14803ce7c20720f5223cc6" || return 1
    _nbwalker_setup_worktree "${PROJECT_ROOT}" "${WORKTREE_ROOT}" \
        "AE-UVM" "2435d3b89d341d076db61c8b6e039734da09ab86" || return 1

    export WORKTREE_ROOT
    export PROJECT_ROOT

    echo "Setup complete. Worktrees are available under ${WORKTREE_ROOT}."
}

if _nbwalker_setup_main; then
    unset -f _nbwalker_setup_main _nbwalker_setup_worktree
    return 0
else
    unset -f _nbwalker_setup_main _nbwalker_setup_worktree
    return 1
fi
