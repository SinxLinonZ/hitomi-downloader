// Package i18n は、ターミナル出力と API エラーメッセージの多言語化を担う。
//
// 原文（簡体字中国語）をそのままキーにする gettext 方式で、翻訳が無い文字列は原文を返す。
// 対応言語は zh / ja / en。言語はプロセス全体で 1 つ。
package i18n

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
)

const (
	ZH = "zh"
	JA = "ja"
	EN = "en"
)

var current atomic.Value // string

func init() { current.Store(ZH) }

// Normalize は "ja_JP.UTF-8" や "zh-CN" のような表記を zh / ja / en に正規化する。
// 認識できなければ空文字を返す。
func Normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case s == "":
		return ""
	case strings.HasPrefix(s, "zh"):
		return ZH
	case strings.HasPrefix(s, "ja"):
		return JA
	case strings.HasPrefix(s, "en"), s == "c", s == "posix":
		return EN
	}
	return ""
}

// Detect は環境変数から言語を推定する。分からなければ英語。
func Detect() string {
	for _, key := range []string{"HITOMI_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		if l := Normalize(os.Getenv(key)); l != "" {
			return l
		}
	}
	return EN
}

// SetLang は言語を設定する。"auto" や空文字は環境変数からの推定になる。
func SetLang(l string) string {
	n := Normalize(l)
	if n == "" {
		n = Detect()
	}
	current.Store(n)
	return n
}

func Lang() string { return current.Load().(string) }

// T は原文を現在の言語に翻訳する。
func T(s string) string {
	switch Lang() {
	case JA:
		if v, ok := ja[s]; ok {
			return v
		}
	case EN:
		if v, ok := en[s]; ok {
			return v
		}
	}
	return s
}

func Sprintf(format string, a ...any) string { return fmt.Sprintf(T(format), a...) }

func Errorf(format string, a ...any) error { return fmt.Errorf(T(format), a...) }

// Catalog は指定した言語の翻訳表を返す（テスト用）。
func Catalog(lang string) map[string]string {
	switch lang {
	case JA:
		return ja
	case EN:
		return en
	}
	return nil
}
