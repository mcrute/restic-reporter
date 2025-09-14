BINARY=restic-reporter

$(BINARY): *.go
	go mod tidy
	CGO_ENABLED=0 go build \
		-ldflags "-X main.version=$(shell git describe --long --tags --dirty --always)"  \
		-o $@ *.go

.PHONY: clean
clean:
	@rm $(BINARY) || true
	( cd .. ; git checkout go.mod go.sum )
