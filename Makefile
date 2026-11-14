BIN := nxmem
VERSION ?= 0.1.0

.PHONY: build cross test demo run docker

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/$(BIN) ./cmd/nxmem

cross:
	@for t in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64; do \
	  os=$${t%/*}; arch=$${t#*/}; \
	  echo "→ $$os/$$arch"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath \
	    -ldflags "-s -w -X main.version=$(VERSION)" -o bin/$(BIN)-$$os-$$arch ./cmd/nxmem; \
	done

test:
	go vet ./...
	go test ./...

demo:
	go run ./cmd/nxmem demo

run:
	go run ./cmd/nxmem serve --addr 127.0.0.1:8861

docker:
	docker build -t nxmem:$(VERSION) .
