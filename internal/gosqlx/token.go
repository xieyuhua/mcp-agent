package gosqlx

import (
	"strings"
	"unicode"
)

// TokenKind 词法单元类型。
type TokenKind int

const (
	TokenEOF TokenKind = iota
	TokenIdent
	TokenKeyword
	TokenNumber
	TokenString  // 单引号字符串字面量
	TokenPunct   // 标点：( ) , . * = 等
	TokenQuoted  // 反引号 / 双引号 / [] 包裹的标识符
	TokenComment // 注释（解析阶段拦截）
)

// Token 词法单元。
type Token struct {
	Kind  TokenKind
	Text  string // 原始文本（含引号）
	Value string // 归一化后的值（标识符去引号，关键字小写）
	Pos   int    // 在原始 SQL 中的起始字节偏移
	End   int    // 结束偏移（不含）
}

// keywords 需要识别为关键字的保留字（全部小写）。
var keywords = map[string]bool{
	"select": true, "from": true, "where": true, "join": true,
	"inner": true, "left": true, "right": true, "full": true,
	"outer": true, "cross": true, "on": true, "using": true,
	"group": true, "by": true, "having": true, "order": true,
	"limit": true, "offset": true, "as": true, "and": true,
	"or": true, "not": true, "in": true, "is": true, "null": true,
	"union": true, "all": true, "distinct": true, "with": true,
	"case": true, "when": true, "then": true, "else": true, "end": true,
	"between": true, "like": true, "exists": true, "asc": true, "desc": true,
}

// Lexer SQL 词法分析器，逐字符扫描生成 token 流。
type Lexer struct {
	src  string
	pos  int
	toks []Token
}

// Tokenize 把 SQL 拆分为 token 流。返回值第二个参数为遇到的第一个注释（若有）。
func Tokenize(sql string) ([]Token, bool) {
	l := &Lexer{src: sql}
	hasComment := false
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			l.pos++
		case c == '-' && l.peek(1) == '-':
			hasComment = true
			l.skipLineComment()
		case c == '/' && l.peek(1) == '*':
			hasComment = true
			l.skipBlockComment()
		case c == '#':
			hasComment = true
			l.skipLineComment()
		case c == '\'':
			l.readString()
		case c == '`':
			l.readQuoted('`', '`')
		case c == '"':
			l.readQuoted('"', '"')
		case c == '[':
			l.readQuoted('[', ']')
		case isIdentStart(rune(c)):
			l.readIdent()
		case c >= '0' && c <= '9':
			l.readNumber()
		default:
			l.readPunct()
		}
	}
	l.toks = append(l.toks, Token{Kind: TokenEOF, Pos: l.pos, End: l.pos})
	return l.toks, hasComment
}

func (l *Lexer) peek(n int) byte {
	if l.pos+n < len(l.src) {
		return l.src[l.pos+n]
	}
	return 0
}

func (l *Lexer) skipLineComment() {
	for l.pos < len(l.src) && l.src[l.pos] != '\n' {
		l.pos++
	}
}

func (l *Lexer) skipBlockComment() {
	l.pos += 2
	for l.pos < len(l.src) {
		if l.src[l.pos] == '*' && l.peek(1) == '/' {
			l.pos += 2
			return
		}
		l.pos++
	}
}

func (l *Lexer) readString() {
	start := l.pos
	l.pos++ // 跳过起始 '
	for l.pos < len(l.src) {
		if l.src[l.pos] == '\'' {
			// 处理 '' 转义
			if l.peek(1) == '\'' {
				l.pos += 2
				continue
			}
			l.pos++
			break
		}
		l.pos++
	}
	text := l.src[start:l.pos]
	l.toks = append(l.toks, Token{Kind: TokenString, Text: text, Value: text, Pos: start, End: l.pos})
}

func (l *Lexer) readQuoted(open, close byte) {
	start := l.pos
	l.pos++ // 跳过起始引号
	for l.pos < len(l.src) {
		if l.src[l.pos] == close {
			l.pos++
			break
		}
		l.pos++
	}
	text := l.src[start:l.pos]
	val := strings.TrimSuffix(strings.TrimPrefix(text, string(open)), string(close))
	l.toks = append(l.toks, Token{Kind: TokenQuoted, Text: text, Value: val, Pos: start, End: l.pos})
}

func (l *Lexer) readIdent() {
	start := l.pos
	for l.pos < len(l.src) && isIdentPart(rune(l.src[l.pos])) {
		l.pos++
	}
	text := l.src[start:l.pos]
	lower := strings.ToLower(text)
	kind := TokenIdent
	if keywords[lower] {
		kind = TokenKeyword
	}
	l.toks = append(l.toks, Token{Kind: kind, Text: text, Value: lower, Pos: start, End: l.pos})
}

func (l *Lexer) readNumber() {
	start := l.pos
	for l.pos < len(l.src) && (unicode.IsDigit(rune(l.src[l.pos])) || l.src[l.pos] == '.') {
		l.pos++
	}
	text := l.src[start:l.pos]
	l.toks = append(l.toks, Token{Kind: TokenNumber, Text: text, Value: text, Pos: start, End: l.pos})
}

func (l *Lexer) readPunct() {
	start := l.pos
	c := l.src[l.pos]
	// 双字符运算符
	two := string(c) + string(l.peek(1))
	switch two {
	case ">=", "<=", "<>", "!=", "||":
		l.pos += 2
		l.toks = append(l.toks, Token{Kind: TokenPunct, Text: two, Value: two, Pos: start, End: l.pos})
		return
	}
	l.pos++
	l.toks = append(l.toks, Token{Kind: TokenPunct, Text: string(c), Value: string(c), Pos: start, End: l.pos})
}

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentPart(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
