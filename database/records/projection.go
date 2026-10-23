package records

import "github.com/komari-monitor/komari/database/models"

// LoadProjection is the field subset selected by a load_type request.
type LoadProjection struct {
	Cpu            *float32 `json:"cpu,omitempty"`
	Gpu            *float32 `json:"gpu,omitempty"`
	Ram            *int64   `json:"ram,omitempty"`
	RamTotal       *int64   `json:"ram_total,omitempty"`
	Swap           *int64   `json:"swap,omitempty"`
	SwapTotal      *int64   `json:"swap_total,omitempty"`
	Load           *float32 `json:"load,omitempty"`
	Temp           *float32 `json:"temp,omitempty"`
	Disk           *int64   `json:"disk,omitempty"`
	DiskTotal      *int64   `json:"disk_total,omitempty"`
	NetIn          *int64   `json:"net_in,omitempty"`
	NetOut         *int64   `json:"net_out,omitempty"`
	NetTotalUp     *int64   `json:"net_total_up,omitempty"`
	NetTotalDown   *int64   `json:"net_total_down,omitempty"`
	Process        *int     `json:"process,omitempty"`
	Connections    *int     `json:"connections,omitempty"`
	ConnectionsUdp *int     `json:"connections_udp,omitempty"`
}

// ProjectLoad returns the load_type projection for one record.
func ProjectLoad(r models.Record, loadType string) LoadProjection {
	var p LoadProjection
	switch loadType {
	case "cpu":
		p.Cpu = ptr(r.Cpu)
	case "gpu":
		p.Gpu = ptr(r.Gpu)
	case "ram":
		p.Ram = ptr(r.Ram)
		p.RamTotal = ptr(r.RamTotal)
	case "swap":
		p.Swap = ptr(r.Swap)
		p.SwapTotal = ptr(r.SwapTotal)
	case "load":
		p.Load = ptr(r.Load)
	case "temp":
		p.Temp = ptr(r.Temp)
	case "disk":
		p.Disk = ptr(r.Disk)
		p.DiskTotal = ptr(r.DiskTotal)
	case "network":
		p.NetIn = ptr(r.NetIn)
		p.NetOut = ptr(r.NetOut)
		p.NetTotalUp = ptr(r.NetTotalUp)
		p.NetTotalDown = ptr(r.NetTotalDown)
	case "process":
		p.Process = ptr(r.Process)
	case "connections":
		p.Connections = ptr(r.Connections)
		p.ConnectionsUdp = ptr(r.ConnectionsUdp)
	default:
		p.Cpu = ptr(r.Cpu)
	}
	return p
}

func ptr[T any](v T) *T { return &v }
