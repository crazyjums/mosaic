package usage

import (
	"encoding/csv"
	"io"
	"strconv"
)

// CSVHeader 是导出的列。与账本表一致，方便在表格里对账。
var CSVHeader = []string{
	"time", "day", "session_id", "source", "profile", "model", "group", "try_no", "status", "error_kind",
	"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "duration_ms", "ttft_ms",
}

// WriteCSV 把原始记录写成 CSV。字段里可能出现逗号和引号（例如配置档名），交给 encoding/csv 转义。
// 会话 ID、名称这类以 = + - @ 开头的字段在表格软件里会被当作公式执行，所以加一个前导单引号（CSV 注入防护）。
func WriteCSV(w io.Writer, events []Event) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(CSVHeader); err != nil {
		return err
	}
	n := func(v int64) string { return strconv.FormatInt(v, 10) }
	for _, e := range events {
		rec := []string{
			e.Time.UTC().Format("2006-01-02T15:04:05Z"), e.Day, safe(e.SessionID), e.Source, safe(e.Profile), safe(e.Model), safe(e.Group),
			strconv.Itoa(e.TryNo), e.Status, string(e.ErrorKind),
			n(int64(e.Usage.InputTokens)), n(int64(e.Usage.OutputTokens)), n(int64(e.Usage.CacheReadTokens)), n(int64(e.Usage.CacheWriteTokens)),
			n(e.DurationMS), n(e.TTFTMS),
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func safe(s string) string {
	if s != "" && (s[0] == '=' || s[0] == '+' || s[0] == '-' || s[0] == '@' || s[0] == '\t' || s[0] == '\r') {
		return "'" + s
	}
	return s
}
