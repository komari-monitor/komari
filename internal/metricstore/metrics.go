package metricstore

const (
	MetricCPU            = "cpu.usage"
	MetricGPU            = "gpu.usage"
	MetricGPUDeviceUsage = "gpu.device.usage"
	MetricGPUMem         = "gpu.memory.used"
	MetricGPUMemTotal    = "gpu.memory.total"
	MetricGPUTemp        = "gpu.temperature"
	MetricRAM            = "memory.used"
	MetricSwap           = "swap.used"
	MetricLoad           = "load.average"
	MetricDisk           = "disk.used"
	MetricNetIn          = "net.in.rate"
	MetricNetOut         = "net.out.rate"
	MetricNetTotalUp     = "net.total.up"
	MetricNetTotalDown   = "net.total.down"
	MetricTrafficUp      = "traffic.up"
	MetricTrafficDown    = "traffic.down"
	MetricProcess        = "process.count"
	MetricConnections    = "connections.tcp"
	MetricConnectionsUDP = "connections.udp"
	MetricPingLatency    = "ping.latency_ms"
	MetricPingLoss       = "ping.loss"
)

// loadRecordMetricNames are the entity-level metrics used to reconstruct the
// legacy Record response shape.
var loadRecordMetricNames = []string{
	MetricCPU, MetricGPU, MetricRAM, MetricSwap, MetricLoad, MetricDisk, MetricNetIn, MetricNetOut,
	MetricNetTotalUp, MetricNetTotalDown, MetricTrafficUp, MetricTrafficDown,
	MetricProcess, MetricConnections, MetricConnectionsUDP,
}

// gpuDeviceRecordMetricNames are stored separately from the entity-level GPU
// average and are included when deleting all system records.
var gpuDeviceRecordMetricNames = []string{
	MetricGPUDeviceUsage, MetricGPUMem, MetricGPUMemTotal, MetricGPUTemp,
}

var recordMetricNames = joinMetricNames(loadRecordMetricNames, gpuDeviceRecordMetricNames)

// Ping has an independent retention and cleanup boundary.
var pingMetricNames = []string{MetricPingLatency, MetricPingLoss}

var builtinMetricNames = joinMetricNames(recordMetricNames, pingMetricNames)

func metricNameForRecordField(name string) (string, bool) {
	switch name {
	case "cpu":
		return MetricCPU, true
	case "gpu":
		return MetricGPU, true
	case "ram":
		return MetricRAM, true
	case "swap":
		return MetricSwap, true
	case "load":
		return MetricLoad, true
	case "disk":
		return MetricDisk, true
	case "net_in", "netin":
		return MetricNetIn, true
	case "net_out", "netout":
		return MetricNetOut, true
	case "net_total_up":
		return MetricNetTotalUp, true
	case "net_total_down":
		return MetricNetTotalDown, true
	case "traffic_up":
		return MetricTrafficUp, true
	case "traffic_down":
		return MetricTrafficDown, true
	case "process":
		return MetricProcess, true
	case "connections":
		return MetricConnections, true
	case "connections_udp":
		return MetricConnectionsUDP, true
	default:
		return "", false
	}
}

func joinMetricNames(groups ...[]string) []string {
	total := 0
	for _, group := range groups {
		total += len(group)
	}
	names := make([]string, 0, total)
	for _, group := range groups {
		names = append(names, group...)
	}
	return names
}
