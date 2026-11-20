# MiHex 构建入口。前端文本源在 python/mihex/www，构建时压缩进 server/wwwgz 再内嵌。
.PHONY: all build generate clean

all: build

build: generate
	go build -o mihex ./cmd/mihex

generate:
	go generate ./server

clean:
	rm -f mihex
