package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestNormalizeAndDetect(t *testing.T) {
	for in, want := range map[string]string{"ja_JP.UTF-8": JA, "zh-CN": ZH, "zh_TW": ZH, "en_US.UTF-8": EN, "C": EN, "fr_FR": "", "": ""} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	t.Setenv("HITOMI_LANG", "")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "ja_JP.UTF-8")
	if Detect() != JA {
		t.Fatal("LANG should select Japanese")
	}
	t.Setenv("HITOMI_LANG", "en")
	if Detect() != EN {
		t.Fatal("HITOMI_LANG takes precedence")
	}
	t.Setenv("HITOMI_LANG", "")
	t.Setenv("LANG", "fr_FR.UTF-8")
	if Detect() != EN {
		t.Fatal("unknown languages fall back to English")
	}
}

func TestTranslateFallsBackToSource(t *testing.T) {
	defer SetLang(ZH)
	SetLang(JA)
	if T("无效 ID") != "無効な ID です" || Sprintf("替换 %d 页", 2) != "2 ページ差し替え" {
		t.Fatal("ja")
	}
	if T("没有这条翻译") != "没有这条翻译" {
		t.Fatal("missing keys must return the source text")
	}
	SetLang(EN)
	if T("无效 ID") != "invalid ID" {
		t.Fatal("en")
	}
	SetLang(ZH)
	if T("无效 ID") != "无效 ID" {
		t.Fatal("zh")
	}
}

var verbs = regexp.MustCompile(`%[#+\- 0-9.]*[a-zA-Z]`)

// 翻訳表のすべてのキーについて、書式指定子の数と種類が原文と一致することを確かめる。
func TestCatalogsKeepFormatVerbs(t *testing.T) {
	for name, cat := range map[string]map[string]string{JA: ja, EN: en} {
		for k, v := range cat {
			if strings.Join(verbs.FindAllString(k, -1), ",") != strings.Join(verbs.FindAllString(v, -1), ",") {
				t.Errorf("%s: format verbs differ: %q -> %q", name, k, v)
			}
		}
	}
}

// ソース中の i18n.T / Sprintf / Errorf の文字列リテラルがすべて翻訳表にあること、
// 翻訳表に使われていないキーが残っていないことを確かめる。
func TestEveryLiteralIsTranslated(t *testing.T) {
	used := map[string]bool{}
	fset := token.NewFileSet()
	for _, dir := range []string{"..", "../../cmd"} {
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, e := parser.ParseFile(fset, path, nil, 0)
			if e != nil {
				t.Fatal(e)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "i18n" || (sel.Sel.Name != "T" && sel.Sel.Name != "Sprintf" && sel.Sel.Name != "Errorf") {
					return true
				}
				lit, ok := call.Args[0].(*ast.BasicLit)
				if !ok {
					t.Errorf("%s: i18n.%s needs a string literal", fset.Position(call.Pos()), sel.Sel.Name)
					return true
				}
				key, _ := strconv.Unquote(lit.Value)
				used[key] = true
				return true
			})
			return nil
		})
	}
	for key := range used {
		if _, ok := ja[key]; !ok {
			t.Errorf("missing ja translation: %q", key)
		}
		if _, ok := en[key]; !ok {
			t.Errorf("missing en translation: %q", key)
		}
	}
	for key := range ja {
		if !used[key] {
			t.Errorf("unused ja key: %q", key)
		}
	}
	for key := range en {
		if !used[key] {
			t.Errorf("unused en key: %q", key)
		}
	}
}
