SERVER_NAME = stream-server
GO_LDFLAGS = -ldflags "-s -w"
GO_VERSION = 1.20
GO_TESTPKGS:=$(shell go list ./... | grep -v cmd | grep -v examples)

all: nodes

go_init:
	go mod download
	go generate ./...

clean:
	rm -rf bin

build: go_init
	go build -o ./bin/$(SERVER_NAME) ./cmd/broadcast

run: build
	./bin/$(SERVER_NAME) -c config.toml

build-linux:
	GOOS=linux GOARCH=amd64 go build -o bin/sfu $(GO_LDFLAGS) ./cmd/main.go

test: go_init
	go test \
		-timeout 240s \
		-coverprofile=cover.out -covermode=atomic \
		-v -race ${GO_TESTPKGS}

monitor:
	docker-compose -f ./mon/dev/docker-compose.yml up -d
