package download

import (
	"os"
	"syscall"
	"time"
)

// ディレクトリの作成時刻（ダウンロード開始時刻に相当）を返す。取得できなければゼロ値。
func birthTime(info os.FileInfo) time.Time {
	attr, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return time.Time{}
	}
	return time.Unix(0, attr.CreationTime.Nanoseconds())
}
