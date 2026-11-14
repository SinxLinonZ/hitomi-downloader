package web

import "embed"

// all: を付けて、ビルド前の .gitkeep だけの状態でもコンパイルできるようにする。
//go:embed all:dist
var Assets embed.FS
