# Cleanup ("Aufräumen"), an extension for Prusa Connect Local.
#
#   make core      fetches the Prusa Connect Local source tree into .core
#                  (Go SDK, component library for the browser part)
#   make test      Go tests and the type check of the browser part
#   make ui-build  builds the page bundle into ui/dist (module.yaml at its root,
#                  the bundle under ui/), as the Plugin Marketplace builder does
#   make release   builds the extension into release/
#
# Needs: Go 1.26, Node 22, pnpm 11, git, sha256sum.

# The Prusa Connect Local release this is built and tested against.
CORE_REPO   ?= https://github.com/prusa3d/prusa-connect.git
CORE_COMMIT ?= v0.1.0-beta.1

PNPM ?= pnpm

.PHONY: core test ui-build release clean

core:
	@if [ ! -d .core/.git ] && [ ! -L .core ]; then \
		git clone -q --depth 1 --branch $(CORE_COMMIT) $(CORE_REPO) .core; \
	fi
	cd .core/packages/typescript && CI=true $(PNPM) install --frozen-lockfile && $(PNPM) run build
	cd .core/packages/svelte && CI=true $(PNPM) install --frozen-lockfile && $(PNPM) run build

test:
	go vet ./... && go test -count=1 ./...
	cd ui && CI=true $(PNPM) install --frozen-lockfile && $(PNPM) run check

# The marketplace builder runs this as its bundle-command, in a job of its own
# without secrets. It checks Prusa Connect out into .core itself (core-ref),
# so `core` only clones when .core is missing and always builds the two
# libraries the page needs. The Go program is built by the builder directly.
ui-build: core
	cd ui && CI=true $(PNPM) install --frozen-lockfile && $(PNPM) run build

release:
	sh scripts/pack.sh

clean:
	rm -rf release ui/dist
