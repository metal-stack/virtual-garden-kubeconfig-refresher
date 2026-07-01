CGO_ENABLED := 1

SHA := $(shell git rev-parse --short=8 HEAD)
GITVERSION := $(shell git describe --long --all)
# gnu date format iso-8601 is parsable with Go RFC3339
BUILDDATE := $(shell date --iso-8601=seconds)
VERSION := $(or ${VERSION},$(shell git describe --tags --exact-match 2> /dev/null || git symbolic-ref -q --short HEAD || git rev-parse --short HEAD))

LINKMODE := -linkmode external -extldflags '-static -s -w' \
		 -X 'github.com/metal-stack/v.Version=$(VERSION)' \
		 -X 'github.com/metal-stack/v.Revision=$(GITVERSION)' \
		 -X 'github.com/metal-stack/v.GitSHA1=$(SHA)' \
		 -X 'github.com/metal-stack/v.BuildDate=$(BUILDDATE)'

.PHONY: build
build:
	go build \
		-tags 'osusergo netgo static_build' \
		-ldflags \
		"$(LINKMODE)" \
		-o bin/virtual-garden-kubeconfig-refresher \
		github.com/metal-stack/virtual-garden-kubeconfig-refresher/...
	strip bin/virtual-garden-kubeconfig-refresher
	md5sum bin/virtual-garden-kubeconfig-refresher > bin/virtual-garden-kubeconfig-refresher.md5
