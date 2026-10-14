default: test lint

# Refresh the private testing/ source specs from a Jamf Platform APIs GitOps
# archive, then regenerate: `make ingest ZIP=~/Downloads/jamf-platform-apis-gitops-vNNNN-*.zip`.
# Specs the SDK holds at an older build are skipped and the hold reported; add
# INGEST_FLAGS=-include-held to take them, or -dry-run to report and write nothing.
ingest:
	cd tools/generate && go run ./ingest -root "$(CURDIR)" -zip "$(ZIP)" $(INGEST_FLAGS)

# Refresh the committed snapshot of the published permissions map, which
# `make test` checks every emitted privilege against (TestScopedPrivilegesUseGAVocabulary).
# Hits the network, so it is an explicit maintainer step and never part of
# generate or CI. Fetches to a temp file first so a dropped connection or a
# failed rename can never truncate or corrupt the tracked snapshot in place.
permmap:
	curl -fsS --remove-on-error https://developer.jamf.com/platform-api/reference/jamf-pro-permissions-map.md \
		-o tools/generate/permissions-map.md.tmp
	mv tools/generate/permissions-map.md.tmp tools/generate/permissions-map.md
	@echo "refreshed tools/generate/permissions-map.md — review its diff, then run: make test"

generate:
	cd tools/generate && go run . -root $(CURDIR)

test:
	go test -v -cover -count=1 -timeout=120s ./...
	cd tools/generate && go test -count=1 -timeout=120s ./...
	cd tools && go test -count=1 -timeout=120s ./acctargets/... ./acclanes/... ./appleschemas/...

# Regenerate both embedded Apple schema tables from apple/device-management:
# appleschemas/profiles/profiles.json (configuration profile payloads) and
# appleschemas/declarations/declarations.json (declarative device management declarations, plus the
# status-item vocabulary that status-subscriptions accepts). One clone, one generator and one commit
# per branch, so the two tables can never be built from different upstream revisions — a legacy
# payload and the declaration wrapping it are validated against the same snapshot.
#
# Deliberately NOT part of `make generate`: it needs network access and a clone, and its upstream is
# not an OpenAPI spec. Review the diff and commit the regenerated tables.
#
# Both tables are the UNION of the release branch and Apple's newest seed (pre-release) branch,
# because Jamf's generative-declarations service tracks seed: while seed_OS_27_0 was open it carried
# 12 configuration declaration types and keys such as siri.settings.AllowSiriAI that release did
# not, all of them already offered in the Jamf UI. A table built from release alone would report
# those as unknown — and an unknown name is an error, not a warning, so that would block working
# configs.
#
# The seed branch is discovered rather than pinned, since its name moves with the OS
# (seed_OS_27_0 -> seed_OS_28_0). Discovery finding NOTHING is expected for part of the year: Apple
# promotes the seed branch into release at OS GA and deletes it, so between that and the next beta
# release is the whole vocabulary — seed_OS_27_0 went this way, release becoming Release-v27.0 on
# 2026-09-18. So it builds from release alone rather than refusing — but only when discovery
# answered and found nothing. A discovery that FAILED is fatal, because a network error that read
# as "no seed" would drop the whole seed vocabulary and report it as withdrawn. Override to pin a branch
# discovery would miss:
#   make apple-schemas SEED_REF=seed_OS_28_0
#
# The tables are rebuilt from the checkouts alone, never merged into what is committed, so they
# carry exactly what Apple declares today and a key Apple withdraws leaves the SDK with it. That is
# the intent — the SDK tracks upstream rather than accumulating a vocabulary Apple has retired. The
# generator still REPORTS every name a regeneration drops, because a withdrawal is the one refresh
# that turns a configuration that validates today into a failing one, and it must reach whoever
# reviews the diff instead of hiding in 6,000 changed lines.
RELEASE_REF ?= release
SEED_REF ?=
apple-schemas:
	@set -e; \
	seed='$(SEED_REF)'; \
	if [ -z "$$seed" ]; then \
		echo "Discovering Apple's newest seed branch..."; \
		heads="$$(git ls-remote --heads https://github.com/apple/device-management.git 'seed_OS_*')" \
			|| { echo "Seed branch discovery failed (git ls-remote exited non-zero); refusing to build from $(RELEASE_REF) alone." >&2; exit 1; }; \
		seed="$$(printf '%s\n' "$$heads" | sed -n 's#.*refs/heads/##p' | sort -V | tail -1)"; \
	fi; \
	if [ -z "$$seed" ]; then \
		echo "No seed_OS_* branch upstream, which is expected between an OS release and"; \
		echo "its next beta. Building from $(RELEASE_REF) alone."; \
	else \
		echo "Using seed branch $$seed"; \
	fi; \
	work="$$(mktemp -d)"; \
	trap 'rm -rf "$$work"' EXIT; \
	newest='$(RELEASE_REF)'; \
	for ref in '$(RELEASE_REF)' $$seed; do \
		echo "Cloning apple/device-management ($$ref)..."; \
		git clone --quiet --depth 1 --branch "$$ref" --filter=blob:none --sparse \
			https://github.com/apple/device-management.git "$$work/$$ref"; \
		git -C "$$work/$$ref" sparse-checkout set mdm/profiles declarative; \
		newest="$$ref"; \
	done; \
	release='$(RELEASE_REF)'; \
	roots="-root $$release=$$work/$$release"; \
	roots="$$roots -commit $$release=$$(git -C "$$work/$$release" rev-parse HEAD)"; \
	if [ -n "$$seed" ]; then \
		roots="$$roots -root $$seed=$$work/$$seed"; \
		roots="$$roots -commit $$seed=$$(git -C "$$work/$$seed" rev-parse HEAD)"; \
	fi; \
	cd tools && go run ./appleschemas $$roots \
		-release "$$(git -C "$$work/$$newest" log -1 --format=%s)" \
		-profiles-out ../appleschemas/profiles/profiles.json \
		-declarations-out ../appleschemas/declarations/declarations.json

testacc:
	go test -v -cover -count=1 -tags acceptance -timeout 120m -p=1 ./...

# Each tool is a separate module, so a root-only run never descends into them.
# tools/ is entered only at its command subpackages (acctargets, acclanes,
# appleschemas): the module root also holds tools.go, a build-tagged blank
# import of copywrite (a main package) that typecheck rejects by design and
# which exists solely to pin the dependency.
#
# These lists must match ci.yml's test job and lint matrix. They drifted once
# already — acclanes landed in CI and not here, so `make lint` before pushing
# linted less than CI did and `make test` never ran the tests that guard the
# acceptance matrix.
lint:
	golangci-lint run ./...
	cd tools/generate && golangci-lint run ./...
	cd tools && golangci-lint run ./acctargets/... ./acclanes/... ./appleschemas/...
	# The acceptance suite is build-tagged, so the untagged run above cannot
	# see any of it. errcheck there is not a style preference: an ignored error
	# in an acceptance test is exactly the "silently tolerate a real error to
	# make one pass" the suite's own policy bans. This row drifted out of the
	# makefile once and the omission was found by CI rather than before the
	# push, which is the whole reason the lists are meant to match.
	golangci-lint run --build-tags=acceptance --enable-only=errcheck,ineffassign ./jamfplatform/...

.PHONY: default ingest permmap generate apple-schemas test testacc lint
