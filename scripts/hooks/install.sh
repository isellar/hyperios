#!/bin/sh
# Installs the repo-tracked git hooks (scripts/hooks/) for this clone by
# pointing core.hooksPath at them. Run once per clone (or via 'just
# hooks-install'). Idempotent.
set -eu

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

git config core.hooksPath scripts/hooks
chmod +x scripts/hooks/post-commit

echo "Installed git hooks from scripts/hooks (core.hooksPath set)."
echo "post-commit will now run 'go test ./...' and auto-push on success."
echo "Set HYPERIOS_SKIP_AUTOPUSH=1 to skip auto-push for a single commit."
