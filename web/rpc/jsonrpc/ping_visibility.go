package jsonrpc

import "github.com/komari-monitor/komari/database/models"

// visiblePingRecords filters before any public aggregation or sampling.
func visiblePingRecords(records []models.PingRecord, hidden map[string]bool) []models.PingRecord {
	visible := records[:0]
	for _, record := range records {
		if record.Client != "" && hidden[record.Client] {
			continue
		}
		visible = append(visible, record)
	}
	return visible
}
