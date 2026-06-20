package metric

import (
	"strconv"
	"strings"
)

// Pxx builds the Aggregation for an arbitrary percentile. The argument is a
// percentage in (0,100): Pxx(99.9) -> "p99.9", Pxx(50) -> "p50". The fixed
// AggP50/AggP95/AggP99 constants are just the common cases of this same string
// form, so they keep working unchanged.
//
// This is what turns the package from "p50/p95/p99 only" into "any percentile":
// callers can ask for p75, p90, p99.99, etc., and every path (in-memory,
// SQL pushdown, and rollup-via-t-digest) understands it.
//
// Pxx 根据任意百分位构造 Aggregation。参数是 (0,100) 内的百分比：
// Pxx(99.9) -> "p99.9"，Pxx(50) -> "p50"。固定的 AggP50/AggP95/AggP99
// 常量只是同一字符串形式的常用情况，因此会保持原有行为。
//
// 这让 package 从“只支持 p50/p95/p99”变成“支持任意百分位”：调用方可以请求
// p75、p90、p99.99 等，并且每条路径（内存、SQL 下推、基于 t-digest 的 rollup）
// 都能理解它。
func Pxx(p float64) Aggregation {
	// Trim trailing zeros so Pxx(95) == AggP95 ("p95"), not "p95.000000".
	s := strconv.FormatFloat(p, 'f', -1, 64)
	return Aggregation("p" + s)
}

// parsePercentile reports whether agg names a percentile and, if so, returns
// the corresponding fraction in [0,1]. "p99.9" -> 0.999. Out-of-range
// percentages (<=0 or >=100) are rejected so validation can reject them.
//
// parsePercentile 判断 agg 是否命名了百分位；如果是，则返回对应的 [0,1] 小数。
// 例如 "p99.9" -> 0.999。越界百分比（<=0 或 >=100）会被拒绝，以便校验逻辑
// 能拒绝它们。
func parsePercentile(agg Aggregation) (float64, bool) {
	s := string(agg)
	if len(s) < 2 || (s[0] != 'p' && s[0] != 'P') {
		return 0, false
	}
	pct, err := strconv.ParseFloat(s[1:], 64)
	if err != nil {
		return 0, false
	}
	if pct <= 0 || pct >= 100 {
		return 0, false
	}
	return pct / 100, true
}

// isPercentile reports whether agg is any percentile aggregation.
//
// isPercentile 判断聚合类型是否为任意百分位聚合。
func isPercentile(agg Aggregation) bool {
	_, ok := parsePercentile(agg)
	return ok
}

// percentileFractionString renders the fraction for SQL percentile_cont, e.g.
// "p99.9" -> "0.999". Trailing zeros are trimmed for stable SQL text.
//
// percentileFractionString 把百分位聚合转换为 SQL percentile_cont 需要的
// 小数字符串，并去掉尾随零以保持 SQL 文本稳定。
func percentileFractionString(agg Aggregation) (string, bool) {
	f, ok := parsePercentile(agg)
	if !ok {
		return "", false
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		// f is in (0,1) so this should not happen, but guard anyway.
		s = strconv.FormatFloat(f, 'f', 1, 64)
	}
	return s, true
}
