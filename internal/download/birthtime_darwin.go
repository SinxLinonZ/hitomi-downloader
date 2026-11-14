package download

import (
	"os"
	"syscall"
	"time"
)

// ディレクトリの作成時刻（ダウンロード開始時刻に相当）を返す。取得できなければゼロ値。
func birthTime(info os.FileInfo) time.Time {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Birthtimespec.Sec <= 0 {
		return time.Time{}
	}
	return time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec)
}
