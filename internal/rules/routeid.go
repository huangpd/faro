package rules

import (
	"fmt"
	"unicode"
	"unicode/utf8"
)

// An exit's name (a route's id) is written in the rules text, at the start of
// a route line and of every rule that sends to it, and is part of the names
// the core is given (route-<id>, dns-route-<id>, <id>#gate). So it may hold
// no space or quote (they split and join the words of a line), no '#' (it
// starts a comment, and ends the id in <id>#gate) and no ':' (a word with one
// is a kind:value). It may not be a word a line starts with by itself.
//
// Chinese is allowed, and any letter outside ASCII. Latin letters are lower
// case: upper case is how regions are written (UK, JP), and an exit named
// like one would read as a region.

// maxRouteID is the longest a name may be, in characters.
const maxRouteID = 24

// reservedIDs are the targets built in, and the words a line starts with.
var reservedIDs = map[string]bool{
	TargetProxy: true, TargetAuto: true, TargetDirect: true, TargetBlock: true,
	"route": true, "region": true, "notice": true,
}

// CheckRouteID says what is wrong with an exit's name, in words for the
// person typing it, or "" when it is right. It is asked as the name is typed,
// and again when the rules are compiled.
func CheckRouteID(id string) string {
	if id == "" {
		return "请给出口起个名字"
	}
	for _, r := range id {
		switch {
		case unicode.IsSpace(r):
			return "出口名不能有空格"
		case r == '"' || r == '“' || r == '”':
			return "出口名不能有引号"
		case r == '#':
			return "出口名不能有 #：# 在规则文本里表示注释"
		case r == ':':
			return "出口名不能有冒号 :，它在规则里用来写 domain:example.com 这类条件"
		case r >= 'A' && r <= 'Z':
			return "英文请用小写：大写字母留给 UK、JP 这类地区代码"
		case !idRune(r):
			return fmt.Sprintf("出口名不能有「%c」：只能用中文、小写字母、数字和 -", r)
		}
	}
	if first, _ := utf8.DecodeRuneInString(id); !idLetter(first) {
		return "出口名要以中文或字母开头"
	}
	if n := utf8.RuneCountInString(id); n > maxRouteID {
		return fmt.Sprintf("出口名最长 %d 个字，现在是 %d 个", maxRouteID, n)
	}
	if reservedIDs[id] {
		return fmt.Sprintf("「%s」是 Faro 自己用的词，请换一个名字", id)
	}
	return ""
}

// idLetter is a letter a name may start with: a lower-case Latin one, or any
// letter outside ASCII (Chinese, and the rest).
func idLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r > unicode.MaxASCII && unicode.IsLetter(r))
}

func idRune(r rune) bool {
	return idLetter(r) || (r >= '0' && r <= '9') || r == '-'
}
