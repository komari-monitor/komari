package utils

import (
	"strings"
)

func DataMasking(str string, private []string) string {
	if str == "" || len(private) == 0 {
		return str
	}
	mask := "********"

	// 相似度阈值，可根据需要调节（0~1，越大越严格）
	const threshold = 0.8

	runes := []rune(str)
	n := len(runes)
	toMask := make([]bool, n)

	// 预处理 private 中的词，去掉空、重复
	uniq := make(map[string]struct{})
	var words []string
	for _, w := range private {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		if _, ok := uniq[w]; ok {
			continue
		}
		uniq[w] = struct{}{}
		words = append(words, w)
	}
	if len(words) == 0 {
		return str
	}

	// 逐词进行滑动窗口匹配 + 模糊匹配（Levenshtein 相似度）
	for _, w := range words {
		wRunes := []rune(w)
		wl := len(wRunes)
		if wl == 0 || wl > n {
			continue
		}

		// 滑动窗口大小采用敏感词长度
		for i := 0; i <= n-wl; i++ {
			if allMasked(toMask[i : i+wl]) { // 已全被标记则跳过
				continue
			}
			sub := string(runes[i : i+wl])
			sim := similarity(sub, w)
			if sim >= threshold {
				for k := 0; k < wl; k++ {
					toMask[i+k] = true
				}
			}
		}
	}

	// 构造输出：连续的掩码段只输出一次；如果原始被遮蔽长度>5，展示首尾字符
	var b strings.Builder
	i := 0
	for i < n {
		if toMask[i] {
			start := i
			for i < n && toMask[i] {
				i++
			}
			end := i // 不包含
			segLen := end - start
			if segLen > 5 {
				b.WriteRune(runes[start])
				b.WriteString(mask)
				b.WriteRune(runes[end-1])
			} else {
				b.WriteString(mask)
			}
		} else {
			b.WriteRune(runes[i])
			i++
		}
	}
	return b.String()
}

// allMasked 判断一个区间是否全部已经被标记
func allMasked(bools []bool) bool {
	for _, v := range bools {
		if !v {
			return false
		}
	}
	return true
}

// similarity 返回两个字符串的相似度 (0~1)，基于 Levenshtein 距离
func similarity(a, b string) float64 {
	if a == b {
		return 1
	}
	ar := []rune(a)
	br := []rune(b)
	dist := levenshtein(ar, br)
	maxLen := len(ar)
	if len(br) > maxLen {
		maxLen = len(br)
	}
	if maxLen == 0 {
		return 1
	}
	return 1 - float64(dist)/float64(maxLen)
}

// levenshtein 计算两个 rune slice 的编辑距离
func levenshtein(a, b []rune) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	// 使用滚动数组降低空间复杂度
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = minInt(del, ins, sub)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

func minInt(vals ...int) int {
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}
