//go:build !darwin && !windows

package download

import (
	"os"
	"time"
)

// 作成時刻を標準 API で取れない環境ではゼロ値を返し、呼び出し側が更新時刻で代用する。
func birthTime(os.FileInfo) time.Time { return time.Time{} }
