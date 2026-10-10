VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GOBUILD := CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)"
TARGETS := linux/amd64 linux/arm64 linux/arm/7 linux/arm/6 windows/amd64 windows/arm64

.PHONY: build run dev test lint dist docker layouts clean

build:            ## build for this machine
	$(GOBUILD) -o opencouchpad ./cmd/opencouchpad

run: build        ## run with real input (needs /dev/uinput access)
	./opencouchpad --port 8080

dev:              ## run without touching your mouse/keyboard (logs input instead)
	go run ./cmd/opencouchpad --driver dryrun --port 8080 --host opencouchpad-dev --log-level debug

test:
	go vet ./...
	go test -race ./...
	for f in web/js/*.js; do node --check $$f; done

dist:             ## cross-compile release binaries into dist/
	@mkdir -p dist
	@for t in $(TARGETS); do \
		os=$${t%%/*}; rest=$${t#*/}; arch=$${rest%%/*}; arm=$$( [ "$$arch" = arm ] && echo $${rest#*/} ); \
		name=opencouchpad-$$os-$$arch$${arm:+v$$arm}; [ $$os = windows ] && name=$$name.exe; \
		echo "  $$name"; GOOS=$$os GOARCH=$$arch GOARM=$$arm $(GOBUILD) -o dist/$$name ./cmd/opencouchpad || exit 1; \
	done
	cd dist && sha256sum opencouchpad-* > SHA256SUMS

docker:
	docker buildx build --platform linux/amd64,linux/arm64,linux/arm/v7,linux/arm/v6 --build-arg VERSION=$(VERSION) -t opencouchpad:$(VERSION) .

layouts:          ## regenerate keyboard layout tables from the system XKB database
	python3 tools/genlayouts.py > internal/keymap/layouts_gen.go
	gofmt -w internal/keymap/layouts_gen.go

clean:
	rm -rf dist opencouchpad opencouchpad.exe
