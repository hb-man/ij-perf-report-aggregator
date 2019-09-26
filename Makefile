.PHONY: build build-mac build-win build-linux

# https://github.com/bvinc/go-sqlite-lite/issues/10#issuecomment-498539630

build:
	make build-mac
	make build-linux
	make build-windows

build-mac:
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 go build -ldflags='-s -w' -o dist/mac/report-aggregator ./

build-linux:
	env GOOS=linux GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-linux-musl-gcc CXX=x86_64-linux-musl-g++ go build -ldflags='-s -w' -o dist/linux/report-aggregator ./

build-windows:
	env GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=/usr/local/bin/x86_64-w64-mingw32-gcc CXX=/usr/local/bin/x86_64-w64-mingw32-g++ go build -ldflags='-s -w' -o dist/windows/report-aggregator.exe ./