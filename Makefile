NEBULA_CMD_PATH = "./cmd/nebula"
BUILD_NUMBER ?= dev+$(shell date -u '+%Y%m%d%H%M%S')
GO111MODULE = on
export GO111MODULE

all: bin-linux bin-linux-386 bin-linux-ppc64le bin-arm bin-arm6 bin-arm64 bin-darwin bin-windows bin-mips bin-mipsle bin-mips64 bin-mips64le

bin:
	go build -ldflags "-X main.Build=$(BUILD_NUMBER)" -o ./nebula ${NEBULA_CMD_PATH}
	go build -ldflags "-X main.Build=$(BUILD_NUMBER)" -o ./nebula-cert ./cmd/nebula-cert

install:
	go install -ldflags "-X main.Build=$(BUILD_NUMBER)" ${NEBULA_CMD_PATH}
	go install -ldflags "-X main.Build=$(BUILD_NUMBER)" ./cmd/nebula-cert

bin-arm:
	mkdir -p build/arm
	GOARCH=arm GOOS=linux go build -o build/arm/nebula -ldflags "-X main.Build=$(BUILD_NUMBER)" ${NEBULA_CMD_PATH}
	GOARCH=arm GOOS=linux go build -o build/arm/nebula-cert -ldflags "-X main.Build=$(BUILD_NUMBER)" ./cmd/nebula-cert

bin-arm6:
	mkdir -p build/arm6
	GOARCH=arm GOARM=6 GOOS=linux go build -o build/arm6/nebula -ldflags "-X main.Build=$(BUILD_NUMBER)" ${NEBULA_CMD_PATH}
	GOARCH=arm GOARM=6 GOOS=linux go build -o build/arm6/nebula-cert -ldflags "-X main.Build=$(BUILD_NUMBER)" ./cmd/nebula-cert

bin-arm64:
	mkdir -p build/arm64
	GOARCH=arm64 GOOS=linux go build -o build/arm64/nebula -ldflags "-X main.Build=$(BUILD_NUMBER)" ${NEBULA_CMD_PATH}
	GOARCH=arm64 GOOS=linux go build -o build/arm64/nebula-cert -ldflags "-X main.Build=$(BUILD_NUMBER)" ./cmd/nebula-cert

bin-vagrant:
	GOARCH=amd64 GOOS=linux go build -o nebula -ldflags "-X main.Build=$(BUILD_NUMBER)" ${NEBULA_CMD_PATH}
	GOARCH=amd64 GOOS=linux go build -ldflags "-X main.Build=$(BUILD_NUMBER)" -o ./nebula-cert ./cmd/nebula-cert

bin-darwin:
	mkdir -p build/darwin
	GOARCH=amd64 GOOS=darwin go build -o build/darwin/nebula -ldflags "-X main.Build=$(BUILD_NUMBER)" ${NEBULA_CMD_PATH}
	GOARCH=amd64 GOOS=darwin go build -o build/darwin/nebula-cert -ldflags "-X main.Build=$(BUILD_NUMBER)" ./cmd/nebula-cert

bin-windows:
	mkdir -p build/windows
	GOARCH=amd64 GOOS=windows go build -o build/windows/nebula.exe -ldflags "-X main.Build=$(BUILD_NUMBER)" ${NEBULA_CMD_PATH}
	GOARCH=amd64 GOOS=windows go build -o build/windows/nebula-cert.exe -ldflags "-X main.Build=$(BUILD_NUMBER)" ./cmd/nebula-cert

bin-linux:
	mkdir -p build/linux
	GOARCH=amd64 GOOS=linux go build -o build/linux/nebula -ldflags "-X main.Build=$(BUILD_NUMBER)" ${NEBULA_CMD_PATH}
	GOARCH=amd64 GOOS=linux go build -o build/linux/nebula-cert -ldflags "-X main.Build=$(BUILD_NUMBER)" ./cmd/nebula-cert

bin-linux-386:
	mkdir -p build/linux-386
	GOARCH=386 GOOS=linux go build -o build/linux-386/nebula -ldflags "-X main.Build=$(BUILD_NUMBER)" ./cmd/nebula
	GOARCH=386 GOOS=linux go build -o build/linux-386/nebula-cert -ldflags "-X main.Build=$(BUILD_NUMBER)" ./cmd/nebula-cert

bin-linux-ppc64le:
	mkdir -p build/linux-ppc64le
	GOARCH=ppc64le GOOS=linux go build -o build/linux-ppc64le/nebula -ldflags "-X main.Build=$(BUILD_NUMBER)" ./cmd/nebula
	GOARCH=ppc64le GOOS=linux go build -o build/linux-ppc64le/nebula-cert -ldflags "-X main.Build=$(BUILD_NUMBER)" ./cmd/nebula-cert

bin-mips:
	mkdir -p build/mips
	GOARCH=mips GOOS=linux go build -o build/mips/nebula -ldflags "-X main.Build=$(BUILD_NUMBER)" ./cmd/nebula
	GOARCH=mips GOOS=linux go build -o build/mips/nebula-cert -ldflags "-X main.Build=$(BUILD_NUMBER)" ./cmd/nebula-cert


bin-mipsle:
	mkdir -p build/mipsle
	GOARCH=mipsle GOOS=linux go build -o build/mipsle/nebula -ldflags "-X main.Build=$(BUILD_NUMBER)" ./cmd/nebula
	GOARCH=mipsle GOOS=linux go build -o build/mipsle/nebula-cert -ldflags "-X main.Build=$(BUILD_NUMBER)" ./cmd/nebula-cert

bin-mips64:
	mkdir -p build/mips64
	GOARCH=mips64 GOOS=linux go build -o build/mips64/nebula -ldflags "-X main.Build=$(BUILD_NUMBER)" ./cmd/nebula
	GOARCH=mips64 GOOS=linux go build -o build/mips64/nebula-cert -ldflags "-X main.Build=$(BUILD_NUMBER)" ./cmd/nebula-cert

bin-mips64le:
	mkdir -p build/mips64le
	GOARCH=mips64le GOOS=linux go build -o build/mips64le/nebula -ldflags "-X main.Build=$(BUILD_NUMBER)" ./cmd/nebula
	GOARCH=mips64le GOOS=linux go build -o build/mips64le/nebula-cert -ldflags "-X main.Build=$(BUILD_NUMBER)" ./cmd/nebula-cert

release: all
	tar -zcv -C build/arm/ -f nebula-linux-arm.tar.gz nebula nebula-cert
	tar -zcv -C build/arm6/ -f nebula-linux-arm6.tar.gz nebula nebula-cert
	tar -zcv -C build/arm64/ -f nebula-linux-arm64.tar.gz nebula nebula-cert
	tar -zcv -C build/darwin/ -f nebula-darwin-amd64.tar.gz nebula nebula-cert
	tar -zcv -C build/windows/ -f nebula-windows-amd64.tar.gz nebula.exe nebula-cert.exe
	tar -zcv -C build/linux/ -f nebula-linux-amd64.tar.gz nebula nebula-cert
	tar -zcv -C build/linux-386/ -f nebula-linux-386.tar.gz nebula nebula-cert
	tar -zcv -C build/linux-ppc64le/ -f nebula-linux-ppc64le.tar.gz nebula nebula-cert
	tar -zcv -C build/mips/ -f nebula-linux-mips.tar.gz nebula nebula-cert
	tar -zcv -C build/mipsle/ -f nebula-linux-mipsle.tar.gz nebula nebula-cert
	tar -zcv -C build/mips64/ -f nebula-linux-mips64.tar.gz nebula nebula-cert
	tar -zcv -C build/mips64le/ -f nebula-linux-mips64le.tar.gz nebula nebula-cert

vet:
	go vet -v ./...

test:
	go test -v ./...

test-cov-html:
	go test -coverprofile=coverage.out
	go tool cover -html=coverage.out

bench:
	go test -bench=.

bench-cpu:
	go test -bench=. -benchtime=5s -cpuprofile=cpu.pprof
	go tool pprof go-audit.test cpu.pprof

bench-cpu-long:
	go test -bench=. -benchtime=60s -cpuprofile=cpu.pprof
	go tool pprof go-audit.test cpu.pprof

proto: nebula.pb.go cert/cert.pb.go

nebula.pb.go: nebula.proto .FORCE
	go build github.com/golang/protobuf/protoc-gen-go
	PATH="$(PWD):$(PATH)" protoc --go_out=. $<
	rm protoc-gen-go

cert/cert.pb.go: cert/cert.proto .FORCE
	$(MAKE) -C cert cert.pb.go

service:
	@echo > /dev/null
	$(eval NEBULA_CMD_PATH := "./cmd/nebula-service")
ifeq ($(words $(MAKECMDGOALS)),1)
	$(MAKE) service ${.DEFAULT_GOAL} --no-print-directory
endif

.FORCE:
.PHONY: test test-cov-html bench bench-cpu bench-cpu-long bin proto release service
.DEFAULT_GOAL := bin
