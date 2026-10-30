BINARY := dsh-lan
GO     ?= go

.PHONY: all build test vet fmt clean install

all: build

build:
	$(GO) build -trimpath -ldflags "-s -w" -o $(BINARY) ./cmd/dsh-lan

install:
	$(GO) install ./cmd/dsh-lan

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

clean:
	rm -f $(BINARY)
