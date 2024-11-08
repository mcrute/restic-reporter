restic-reporter: *.go
	go mod tidy
	CGO_ENABLED=0 go build -o $@ *.go

.PHONY: clean
clean:
	rm restic-reporter || true
	( cd .. ; git checkout go.mod go.sum )
