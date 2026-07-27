# VERSION, COMMIT and DATE feed the same three -X ldflags that GoReleaser sets on a released
# binary. That is deliberate: `canopy version` should read the same whether the binary came from
# `make install` or from a downloaded release archive, so nobody has to work out which build they
# have from a mismatched format.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.date=$(DATE)

.PHONY: build test lint fmt vet install snapshot clean

# Matches the CGO_ENABLED=0 and -trimpath used by GoReleaser, so a local build is not just
# version-labelled the same as a release build but built the same way.
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o canopy ./cmd/canopy

# -race catches data races that only show up under the scheduler's own timing, which is most of
# what this project needs to get right given how much of it is agents running concurrently.
# -count=1 defeats the test cache: a green result from ten minutes ago is not evidence about the
# code as it stands now, which is the same reason the engine treats a stale test result as no
# result at all.
test:
	go test -race -count=1 ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

# go install ignores the ldflags above unless they are passed explicitly, so without this a
# `go install ./cmd/canopy` run straight from the README would silently produce a binary that
# reports itself as "dev" forever, which is a worse first impression than no version at all.
install:
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/canopy

# Builds through the real GoReleaser config without publishing anything, so the archive layout and
# the ldflags wiring can be checked before a tag ever gets pushed.
snapshot:
	goreleaser build --snapshot --clean

clean:
	rm -f canopy
	rm -rf dist
