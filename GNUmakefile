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
	cd tools && go test -count=1 -timeout=120s ./acctargets/... ./acclanes/...

testacc:
	go test -v -cover -count=1 -tags acceptance -timeout 120m -p=1 ./...

# Each tool is a separate module, so a root-only run never descends into them.
# tools/ is entered only at its command subpackages (acctargets, acclanes): the
# module root also holds tools.go, a build-tagged blank import of copywrite (a
# main package) that typecheck rejects by design and which exists solely to pin
# the dependency.
#
# These lists must match ci.yml's test job and lint matrix. They drifted once
# already — acclanes landed in CI and not here, so `make lint` before pushing
# linted less than CI did and `make test` never ran the tests that guard the
# acceptance matrix.
lint:
	golangci-lint run ./...
	cd tools/generate && golangci-lint run ./...
	cd tools && golangci-lint run ./acctargets/... ./acclanes/...
	# The acceptance suite is build-tagged, so the untagged run above cannot
	# see any of it. errcheck there is not a style preference: an ignored error
	# in an acceptance test is exactly the "silently tolerate a real error to
	# make one pass" the suite's own policy bans. This row drifted out of the
	# makefile once and the omission was found by CI rather than before the
	# push, which is the whole reason the lists are meant to match.
	golangci-lint run --build-tags=acceptance --enable-only=errcheck,ineffassign ./jamfplatform/...

.PHONY: default ingest permmap generate test testacc lint
