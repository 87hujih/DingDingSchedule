package pinyinutil

import (
	"strings"
	"unicode"

	"github.com/mozillazg/go-pinyin"
)

// FullAndAbbr 将中文姓名转换为：
// - full: 全拼（无声调、全小写、无空格），如 "张三" -> "zhangsan"
// - abbr: 首字母（全小写），如 "张三" -> "zs"
//
// 对于非中文字符，尽量保留字母/数字（转小写）。
func FullAndAbbr(name string) (full string, abbr string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ""
	}

	args := pinyin.NewArgs()
	args.Style = pinyin.Normal
	args.Heteronym = false

	syllables := pinyin.LazyPinyin(name, args)

	var fullBuilder strings.Builder
	var abbrBuilder strings.Builder

	for _, syl := range syllables {
		syl = strings.TrimSpace(syl)
		if syl == "" {
			continue
		}
		syl = strings.ToLower(syl)

		// 拼接全拼（去掉可能的空白）
		for _, r := range syl {
			if unicode.IsSpace(r) {
				continue
			}
			fullBuilder.WriteRune(r)
		}

		// 首字母：取第一个字母/数字
		for _, r := range syl {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				abbrBuilder.WriteRune(r)
				break
			}
		}
	}

	return fullBuilder.String(), abbrBuilder.String()
}
