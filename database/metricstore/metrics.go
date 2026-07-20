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

var obsoleteBuiltinMetricNames = []string{
	"memory.total",
	"swap.total",
	"temperature",
	"disk.total",
}

// gpuDeviceRecordMetricNames are stored separately from the entity-level GPU
// average and are included when deleting all system records.
var gpuDeviceRecordMetricNames = []string{
	MetricGPUDeviceUsage, MetricGPUMem, MetricGPUMemTotal, MetricGPUTemp,
}

var recordMetricNames = joinMetricNames(loadRecordMetricNames, gpuDeviceRecordMetricNames)

// Ping has an independent retention and cleanup boundary.
var pingMetricNames = []string{MetricPingLatency, MetricPingLoss}

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
