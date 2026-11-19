#!/bin/sh
# 由 //go:generate 调用：把前端的可压缩资源 gzip 后写入 wwwgz，供 go:embed 使用。
set -e
cd "$(dirname "$0")"
rm -rf wwwgz && mkdir wwwgz
for f in ../python/mihex/www/*; do
	case "$f" in
	*.png|*.woff2) cp "$f" wwwgz/ ;;
	*) gzip -9 -n -c "$f" > "wwwgz/$(basename "$f")" ;;
	esac
done
