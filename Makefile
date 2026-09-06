# Task runner for the gate that guards this repository's history.
#
# capsize itself builds with plain `go build ./...`; this file exists because the
# pre-push gate needs installing and proving, and both of those should be one
# command rather than a paragraph in CONTRIBUTING.md.

.PHONY: help init identity test-hook

help:          ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-12s %s\n", $$1, $$2}'

init:          ## Step 1 for any clone: install the pre-push gate
	@git config core.hooksPath .githooks
	@echo "core.hooksPath = $$(git config --get core.hooksPath)"
	@command -v gitleaks >/dev/null 2>&1 \
		|| { echo "gitleaks is not installed. The pre-push gate fails closed without it: brew install gitleaks"; exit 1; }
	@echo "pre-push gate installed"

identity:      ## Run the pre-push gate over all of this repository's history
	@./.githooks/pre-push --all-history

test-hook:     ## Prove the gate still rejects each thing it claims to reject
	@./.githooks/selftest.sh
