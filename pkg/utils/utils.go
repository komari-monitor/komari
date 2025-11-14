package utils

import (
	"sort"
	"strings"
	"time"

	"github.com/komari-monitor/komari/internal/common"
	"github.com/komari-monitor/komari/internal/database/models"
)

// AverageReport 根据 topPercentage 参数计算报告的平均值。
// 如果 topPercentage 为 0，则计算所有记录的平均值。
// 如果 topPercentage 大于 0 且小于等于 1，则计算从大到小排序后的前 topPercentage 记录的平均值。
func AverageReport(uuid string, time time.Time, records []common.Report, topPercentage float64) models.Record {
	count := len(records)
	if count == 0 {
		return models.Record{}
	}

	recordsToAverageCount := count
	if topPercentage > 0 && topPercentage <= 1 {
		recordsToAverageCount = int(float64(count) * topPercentage)
		if recordsToAverageCount == 0 && count > 0 {
			recordsToAverageCount = 1 // 确保至少选择一个记录，除非总数为0
		}
	}

	// 定义一个辅助函数，用于排序和求和
	sumAndSort := func(getFloat32Value func(common.Report) float32, getInt64Value func(common.Report) int64, isFloat bool) (float32, int64) {
		if isFloat {
			sort.Slice(records, func(i, j int) bool {
				return getFloat32Value(records[i]) > getFloat32Value(records[j])
			})
			var sum float32
			for i := 0; i < recordsToAverageCount; i++ {
				sum += getFloat32Value(records[i])
			}
			return sum, 0
		} else {
			sort.Slice(records, func(i, j int) bool {
				return getInt64Value(records[i]) > getInt64Value(records[j])
			})
			var sum int64
			for i := 0; i < recordsToAverageCount; i++ {
				sum += getInt64Value(records[i])
			}
			return 0, sum
		}
	}

	var sumCPU, sumLOAD, sumGPU float32
	var sumRAM, sumRAMTotal, sumSWAP, sumSWAPTotal, sumDISK, sumDISKTotal, sumNETIn, sumNETOut, sumNETTotalUp, sumNETTotalDown int64
	var sumPROCESS, sumConnections, sumConnectionsUDP int

	if topPercentage > 0 && topPercentage <= 1 {
		sumCPU, _ = sumAndSort(func(r common.Report) float32 { return float32(r.CPU.Usage) }, nil, true)
		sumLOAD, _ = sumAndSort(func(r common.Report) float32 { return float32(r.Load.Load1) }, nil, true)
		sumGPU, _ = sumAndSort(func(r common.Report) float32 {
			if r.GPU != nil {
				return float32(r.GPU.AverageUsage)
			}
			return 0
		}, nil, true)

		_, sumRAM = sumAndSort(nil, func(r common.Report) int64 { return r.Ram.Used }, false)
		//_, sumRAMTotal = sumAndSort(nil, nil, func(r common.Report) int64 { return r.Ram.Total }, false)
		_, sumSWAP = sumAndSort(nil, func(r common.Report) int64 { return r.Swap.Used }, false)
		//_, sumSWAPTotal = sumAndSort(nil, nil, func(r common.Report) int64 { return r.Swap.Total }, false)
		_, sumDISK = sumAndSort(nil, func(r common.Report) int64 { return r.Disk.Used }, false)
		//_, sumDISKTotal = sumAndSort(nil, nil, func(r common.Report) int64 { return r.Disk.Total }, false)
		_, sumNETIn = sumAndSort(nil, func(r common.Report) int64 { return r.Network.Down }, false)
		_, sumNETOut = sumAndSort(nil, func(r common.Report) int64 { return r.Network.Up }, false)
		_, sumNETTotalUp = sumAndSort(nil, func(r common.Report) int64 { return r.Network.TotalUp }, false)
		_, sumNETTotalDown = sumAndSort(nil, func(r common.Report) int64 { return r.Network.TotalDown }, false)

		_, sumInt := sumAndSort(nil, func(r common.Report) int64 { return int64(r.Process) }, false)
		sumPROCESS = int(sumInt)
		_, sumInt = sumAndSort(nil, func(r common.Report) int64 { return int64(r.Connections.TCP) }, false)
		sumConnections = int(sumInt)
		_, sumInt = sumAndSort(nil, func(r common.Report) int64 { return int64(r.Connections.UDP) }, false)
		sumConnectionsUDP = int(sumInt)

	} else { // 计算所有记录的平均值
		for _, r := range records {
			sumCPU += float32(r.CPU.Usage)
			sumLOAD += float32(r.Load.Load1)
			if r.GPU != nil {
				sumGPU += float32(r.GPU.AverageUsage)
			}
			sumRAM += r.Ram.Used
			sumRAMTotal += r.Ram.Total
			sumSWAP += r.Swap.Used
			sumSWAPTotal += r.Swap.Total
			sumDISK += r.Disk.Used
			sumDISKTotal += r.Disk.Total
			sumNETIn += r.Network.Down
			sumNETOut += r.Network.Up
			sumNETTotalUp += r.Network.TotalUp
			sumNETTotalDown += r.Network.TotalDown
			sumPROCESS += r.Process
			sumConnections += r.Connections.TCP
			sumConnectionsUDP += r.Connections.UDP
		}
	}

	// 创建新的聚合记录
	newRecord := models.Record{
		Client:         uuid,
		Time:           models.FromTime(time),
		Cpu:            sumCPU / float32(recordsToAverageCount),
		Gpu:            sumGPU / float32(recordsToAverageCount), // 计算GPU平均使用率
		Ram:            sumRAM / int64(recordsToAverageCount),
		RamTotal:       records[0].Ram.Total,
		Swap:           sumSWAP / int64(recordsToAverageCount),
		SwapTotal:      records[0].Swap.Total,
		Load:           sumLOAD / float32(recordsToAverageCount),
		Temp:           0, // 保持原始行为
		Disk:           sumDISK / int64(recordsToAverageCount),
		DiskTotal:      records[0].Disk.Total,
		NetIn:          sumNETIn / int64(recordsToAverageCount),
		NetOut:         sumNETOut / int64(recordsToAverageCount),
		NetTotalUp:     sumNETTotalUp / int64(recordsToAverageCount),
		NetTotalDown:   sumNETTotalDown / int64(recordsToAverageCount),
		Process:        sumPROCESS / recordsToAverageCount,
		Connections:    sumConnections / recordsToAverageCount,
		ConnectionsUdp: sumConnectionsUDP / recordsToAverageCount,
	}
	return newRecord
}

// AverageGPUReports 使用与 AverageReport 相同的聚合逻辑处理GPU数据
// 返回每个GPU设备的聚合记录
func AverageGPUReports(uuid string, time time.Time, reports []common.Report, topPercentage float64) []models.GPURecord {
	if len(reports) == 0 {
		return []models.GPURecord{}
	}

	// 收集所有GPU设备数据
	deviceData := make(map[int]struct {
		DeviceName  string
		MemTotal    []int64
		MemUsed     []int64
		Utilization []float64
		Temperature []int
	})

	for _, report := range reports {
		if report.GPU != nil && len(report.GPU.DetailedInfo) > 0 {
			for idx, gpu := range report.GPU.DetailedInfo {
				if _, exists := deviceData[idx]; !exists {
					deviceData[idx] = struct {
						DeviceName  string
						MemTotal    []int64
						MemUsed     []int64
						Utilization []float64
						Temperature []int
					}{DeviceName: gpu.Name}
				}
				data := deviceData[idx]
				data.MemTotal = append(data.MemTotal, gpu.MemoryTotal)
				data.MemUsed = append(data.MemUsed, gpu.MemoryUsed)
				data.Utilization = append(data.Utilization, gpu.Utilization)
				data.Temperature = append(data.Temperature, gpu.Temperature)
				deviceData[idx] = data
			}
		}
	}

	// 复用现有的聚合逻辑
	sumAndSort := func(values []float64, topPerc float64) float64 {
		if len(values) == 0 {
			return 0
		}
		count := len(values)
		recordsToAverageCount := count
		if topPerc > 0 && topPerc <= 1 {
			recordsToAverageCount = int(float64(count) * topPerc)
			if recordsToAverageCount == 0 && count > 0 {
				recordsToAverageCount = 1
			}
		}

		if topPerc > 0 && topPerc <= 1 {
			sort.Float64s(values)
			// 取最高的值
			var sum float64
			for i := count - recordsToAverageCount; i < count; i++ {
				sum += values[i]
			}
			return sum / float64(recordsToAverageCount)
		} else {
			var sum float64
			for _, val := range values {
				sum += val
			}
			return sum / float64(count)
		}
	}

	sumAndSortInt64 := func(values []int64, topPerc float64) int64 {
		if len(values) == 0 {
			return 0
		}
		count := len(values)
		recordsToAverageCount := count
		if topPerc > 0 && topPerc <= 1 {
			recordsToAverageCount = int(float64(count) * topPerc)
			if recordsToAverageCount == 0 && count > 0 {
				recordsToAverageCount = 1
			}
		}

		if topPerc > 0 && topPerc <= 1 {
			sort.Slice(values, func(i, j int) bool { return values[i] > values[j] })
			var sum int64
			for i := 0; i < recordsToAverageCount; i++ {
				sum += values[i]
			}
			return sum / int64(recordsToAverageCount)
		} else {
			var sum int64
			for _, val := range values {
				sum += val
			}
			return sum / int64(count)
		}
	}

	sumAndSortInt := func(values []int, topPerc float64) int {
		if len(values) == 0 {
			return 0
		}
		int64Values := make([]int64, len(values))
		for i, v := range values {
			int64Values[i] = int64(v)
		}
		return int(sumAndSortInt64(int64Values, topPerc))
	}

	// 生成每个设备的聚合记录
	var result []models.GPURecord
	for deviceIndex, data := range deviceData {
		if len(data.MemTotal) > 0 {
			record := models.GPURecord{
				Client:      uuid,
				Time:        models.FromTime(time),
				DeviceIndex: deviceIndex,
				DeviceName:  data.DeviceName,
				MemTotal:    sumAndSortInt64(data.MemTotal, topPercentage),
				MemUsed:     sumAndSortInt64(data.MemUsed, topPercentage),
				Utilization: float32(sumAndSort(data.Utilization, topPercentage)),
				Temperature: sumAndSortInt(data.Temperature, topPercentage),
			}
			result = append(result, record)
		}
	}

	return result
}

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
