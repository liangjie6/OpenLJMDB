.PHONY: build backend-build test check release web-build

# Root-level convenience targets for the complete application.
build backend-build:
	$(MAKE) -C backend build

test:
	$(MAKE) -C backend test

check:
	$(MAKE) -C backend check

release:
	$(MAKE) -C backend release

web-build:
	npm --prefix web run build
