package plugin

import (
	"errors"
	"strings"
)

// expand 把字符串里的 ${NAME} 换成 vars[NAME]。变量名在校验清单时已经确认过是已知的，这里遇到未知的仍然报错。
func expand(s string, vars map[string]string) (string, error) {
	var firstErr error
	out := varRe.ReplaceAllStringFunc(s, func(m string) string {
		name := varRe.FindStringSubmatch(m)[1]
		v, ok := vars[name]
		if !ok && firstErr == nil {
			firstErr = errors.New("未知的变量 ${" + name + "}")
		}
		return v
	})
	return out, firstErr
}

// splitCommand 把一条命令按空白拆成参数，支持双引号和单引号（引号内的空白不拆分），不做任何 shell 解释：
// 没有管道、重定向、通配符、$(...)。反斜杠只在双引号里转义引号和反斜杠本身——Windows 路径里的反斜杠要原样保留。
//
// 先拆再展开变量（见 expandArgs）：变量的值里有空格或引号，也只会是同一个参数的一部分，不会变成新的参数。
func splitCommand(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inWord := false
	var quote rune
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else if quote == '"' && r == '\\' && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\\') {
				i++
				cur.WriteRune(rs[i])
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, inWord = r, true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if inWord {
				args = append(args, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, errors.New("命令里有没有闭合的引号")
	}
	if inWord {
		args = append(args, cur.String())
	}
	if len(args) == 0 {
		return nil, errors.New("命令为空")
	}
	return args, nil
}

// expandArgs 先把命令拆成参数，再对每个参数展开变量。
func expandArgs(command string, vars map[string]string) ([]string, error) {
	args, err := splitCommand(command)
	if err != nil {
		return nil, err
	}
	for i, a := range args {
		if args[i], err = expand(a, vars); err != nil {
			return nil, err
		}
	}
	return args, nil
}
