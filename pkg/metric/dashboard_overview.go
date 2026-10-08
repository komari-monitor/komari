package metric

import (
	"context"
	"sort"
	"strconv"
	"time"
)

// DashboardTrafficPoint is one fleet-wide chart sample for the admin overview.
type DashboardTrafficPoint struct {
	Time     int64
	UpRate   float64
	DownRate float64
	UpCum    float64
	DownCum  float64
}

// DashboardNodeTotal ranks one entity's traffic for the overview list.
type DashboardNodeTotal struct {
	UUID     string
	Up       float64
	Down     float64
	Total    float64
	PeakRate float64
	PeakTime int64
}

// DashboardRankItem is a per-entity average/peak used by overview cards.
type DashboardRankItem struct {
	UUID     string
	Value    float64
	Peak     float64
	PeakTime int64
}

// DashboardOverview is the compact payload for admin dashboard first paint.
// It is built by streaming dashboard_buckets rows so peak memory stays near
// O(entities + chart buckets) instead of O(entities × buckets × metrics).
type DashboardOverview struct {
	TrafficPoints []DashboardTrafficPoint
	NodeTotals    []DashboardNodeTotal
	TotalUp       float64
	TotalDown     float64
	TopCPU        []DashboardRankItem
	TopMem        []DashboardRankItem
}

type overviewTrafficBucket struct {
	upRate, downRate, upDelta, downDelta float64
}

type overviewEntityTraffic struct {
	up, down   float64
	peakRate   float64
	peakTime   int64
	rateByTime map[int64]struct{ up, down float64 }
}

type overviewRankAcc struct {
	sum      float64
	count    float64
	peak     float64
	peakTime int64
}

type overviewNetTotalState struct {
	previous       *float64
	previousBucket int64
	rebound        *float64
}

type overviewBuilder struct {
	intervalMs  int64
	byTime      map[int64]*overviewTrafficBucket
	byEntity    map[string]*overviewEntityTraffic
	cpu         map[string]*overviewRankAcc
	mem         map[string]*overviewRankAcc
	netUp       map[string]*overviewNetTotalState
	netDown     map[string]*overviewNetTotalState
	skipUp      map[string]struct{}
	skipDown    map[string]struct{}
	compression float64
}

// DashboardOverview streams the 5-minute dashboard mirror into fleet summaries
// without materializing per-entity timeseries.
func (s *Store) DashboardOverview(ctx context.Context, entityIDs []string, start, end time.Time) (DashboardOverview, error) {
	if err := s.ensureOpen(); err != nil {
		return DashboardOverview{}, err
	}
	empty := DashboardOverview{
		TrafficPoints: []DashboardTrafficPoint{},
		NodeTotals:    []DashboardNodeTotal{},
		TopCPU:        []DashboardRankItem{},
		TopMem:        []DashboardRankItem{},
	}
	if !s.dashboardReady.Load() || len(entityIDs) == 0 {
		return empty, nil
	}

	start = start.UTC()
	end = end.UTC()
	builder := &overviewBuilder{
		intervalMs:  DashboardBucketInterval.Milliseconds(),
		byTime:      make(map[int64]*overviewTrafficBucket),
		byEntity:    make(map[string]*overviewEntityTraffic, len(entityIDs)),
		cpu:         make(map[string]*overviewRankAcc, len(entityIDs)),
		mem:         make(map[string]*overviewRankAcc, len(entityIDs)),
		netUp:       make(map[string]*overviewNetTotalState, len(entityIDs)),
		netDown:     make(map[string]*overviewNetTotalState, len(entityIDs)),
		skipUp:      make(map[string]struct{}),
		skipDown:    make(map[string]struct{}),
		compression: s.cfg.RollupPolicy.compression(),
	}

	metrics := []string{
		"net.in.rate", "net.out.rate",
		"net.total.up", "net.total.down",
		"traffic.up", "traffic.down",
		"cpu.usage", "memory.used",
	}
	fields := rollupReadSum | rollupReadMin | rollupReadMax | rollupReadLast | rollupReadDigest
	startMilli := bucketStartMillis(start.UnixMilli(), builder.intervalMs)
	endMilli := end.UnixMilli()
	indexName := DashboardMetricBucketIndexName(s.cfg.TablePrefix)

	sortedEntities := append([]string(nil), entityIDs...)
	sort.Strings(sortedEntities)
	for chunkStart := 0; chunkStart < len(sortedEntities); chunkStart += dashboardEntityIDChunkSize {
		chunkEnd := chunkStart + dashboardEntityIDChunkSize
		if chunkEnd > len(sortedEntities) {
			chunkEnd = len(sortedEntities)
		}
		rendered := s.dialect.renderDashboardRead(s.tables, indexName, dashboardReadPlan{
			MetricNames: metrics,
			EntityIDs:   sortedEntities[chunkStart:chunkEnd],
			StartMilli:  startMilli,
			EndMilli:    endMilli,
			Fields:      fields,
		})
		if err := s.scanDashboardOverviewRows(ctx, rendered, builder); err != nil {
			return DashboardOverview{}, err
		}
	}
	return builder.finish(), nil
}

func (s *Store) scanDashboardOverviewRows(ctx context.Context, rendered renderedSQL, builder *overviewBuilder) error {
	rows, err := s.reader().QueryContext(ctx, rendered.Query, rendered.Args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	var metricName, entityID, tagsHash, tagsJSON, labelsHash, labelsJSON string
	var bucket, count, lastTS int64
	var sum, min, max, lastVal float64
	var digestBlob []byte
	destinations := []any{
		&metricName, &entityID, &tagsHash, &tagsJSON, &labelsHash, &labelsJSON,
		&bucket, &count, &sum, &min, &max, &lastVal, &lastTS, &digestBlob,
	}

	for rows.Next() {
		if err := rows.Scan(destinations...); err != nil {
			return err
		}
		aligned := bucketStartMillis(bucket, builder.intervalMs)
		switch metricName {
		case "net.in.rate":
			value, err := overviewPercentile(count, min, max, digestBlob, builder.compression, 0.95)
			if err != nil {
				return err
			}
			builder.addRate(entityID, aligned, false, value)
		case "net.out.rate":
			value, err := overviewPercentile(count, min, max, digestBlob, builder.compression, 0.95)
			if err != nil {
				return err
			}
			builder.addRate(entityID, aligned, true, value)
		case "net.total.up":
			if builder.markNetTotalDiscontinuity(builder.netUp, entityID, aligned, lastVal) {
				builder.skipUp[overviewDiscKey(entityID, aligned)] = struct{}{}
			}
		case "net.total.down":
			if builder.markNetTotalDiscontinuity(builder.netDown, entityID, aligned, lastVal) {
				builder.skipDown[overviewDiscKey(entityID, aligned)] = struct{}{}
			}
		case "traffic.up":
			if _, skip := builder.skipUp[overviewDiscKey(entityID, aligned)]; skip {
				continue
			}
			builder.addDelta(entityID, aligned, true, sum)
		case "traffic.down":
			if _, skip := builder.skipDown[overviewDiscKey(entityID, aligned)]; skip {
				continue
			}
			builder.addDelta(entityID, aligned, false, sum)
		case "cpu.usage":
			builder.addRank(builder.cpu, entityID, aligned, sum, count, max)
		case "memory.used":
			builder.addRank(builder.mem, entityID, aligned, sum, count, max)
		}
	}
	return rows.Err()
}

func overviewPercentile(count int64, min, max float64, blob []byte, compression, q float64) (float64, error) {
	if count <= 0 {
		return 0, nil
	}
	digest, err := digestFromRollup(count, min, max, blob, compression)
	if err != nil {
		return 0, err
	}
	if digest.Count() == 0 {
		return 0, nil
	}
	return digest.Quantile(q), nil
}

func overviewDiscKey(entityID string, bucket int64) string {
	return entityID + "\x00" + strconv.FormatInt(bucket, 10)
}

func (b *overviewBuilder) markNetTotalDiscontinuity(states map[string]*overviewNetTotalState, entityID string, bucket int64, value float64) bool {
	state := states[entityID]
	if state == nil {
		state = &overviewNetTotalState{}
		states[entityID] = state
	}
	discontinuity := false
	if state.previousBucket != 0 && bucket-state.previousBucket > b.intervalMs+b.intervalMs/2 {
		discontinuity = true
	}
	if state.previous != nil && value < *state.previous {
		discontinuity = true
		prev := *state.previous
		state.rebound = &prev
	} else if state.rebound != nil {
		if value >= *state.rebound {
			discontinuity = true
		}
		state.rebound = nil
	}
	v := value
	state.previous = &v
	state.previousBucket = bucket
	return discontinuity
}

func (b *overviewBuilder) trafficBucket(ts int64) *overviewTrafficBucket {
	entry := b.byTime[ts]
	if entry == nil {
		entry = &overviewTrafficBucket{}
		b.byTime[ts] = entry
	}
	return entry
}

func (b *overviewBuilder) entityTraffic(entityID string) *overviewEntityTraffic {
	entry := b.byEntity[entityID]
	if entry == nil {
		entry = &overviewEntityTraffic{rateByTime: make(map[int64]struct{ up, down float64 })}
		b.byEntity[entityID] = entry
	}
	return entry
}

func (b *overviewBuilder) addRate(entityID string, ts int64, up bool, value float64) {
	bucket := b.trafficBucket(ts)
	entity := b.entityTraffic(entityID)
	rate := entity.rateByTime[ts]
	if up {
		bucket.upRate += value
		rate.up += value
	} else {
		bucket.downRate += value
		rate.down += value
	}
	entity.rateByTime[ts] = rate
	combined := rate.up + rate.down
	if combined > entity.peakRate {
		entity.peakRate = combined
		entity.peakTime = ts
	}
}

func (b *overviewBuilder) addDelta(entityID string, ts int64, up bool, value float64) {
	bucket := b.trafficBucket(ts)
	entity := b.entityTraffic(entityID)
	if up {
		bucket.upDelta += value
		entity.up += value
	} else {
		bucket.downDelta += value
		entity.down += value
	}
}

func (b *overviewBuilder) addRank(target map[string]*overviewRankAcc, entityID string, ts int64, sum float64, count int64, max float64) {
	if count <= 0 {
		return
	}
	item := target[entityID]
	if item == nil {
		item = &overviewRankAcc{}
		target[entityID] = item
	}
	item.sum += sum
	item.count += float64(count)
	if max > item.peak {
		item.peak = max
		item.peakTime = ts
	}
}

func (b *overviewBuilder) finish() DashboardOverview {
	times := make([]int64, 0, len(b.byTime))
	for ts := range b.byTime {
		times = append(times, ts)
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })

	points := make([]DashboardTrafficPoint, 0, len(times))
	var totalUp, totalDown float64
	for _, ts := range times {
		bucket := b.byTime[ts]
		totalUp += bucket.upDelta
		totalDown += bucket.downDelta
		points = append(points, DashboardTrafficPoint{
			Time:     ts,
			UpRate:   bucket.upRate,
			DownRate: bucket.downRate,
			UpCum:    totalUp,
			DownCum:  totalDown,
		})
	}

	nodeTotals := make([]DashboardNodeTotal, 0, len(b.byEntity))
	for uuid, entity := range b.byEntity {
		nodeTotals = append(nodeTotals, DashboardNodeTotal{
			UUID:     uuid,
			Up:       entity.up,
			Down:     entity.down,
			Total:    entity.up + entity.down,
			PeakRate: entity.peakRate,
			PeakTime: entity.peakTime,
		})
	}
	sort.Slice(nodeTotals, func(i, j int) bool { return nodeTotals[i].Total > nodeTotals[j].Total })

	return DashboardOverview{
		TrafficPoints: points,
		NodeTotals:    nodeTotals,
		TotalUp:       totalUp,
		TotalDown:     totalDown,
		TopCPU:        finishOverviewRanks(b.cpu),
		TopMem:        finishOverviewRanks(b.mem),
	}
}

func finishOverviewRanks(items map[string]*overviewRankAcc) []DashboardRankItem {
	out := make([]DashboardRankItem, 0, len(items))
	for uuid, item := range items {
		if item.count <= 0 {
			continue
		}
		out = append(out, DashboardRankItem{
			UUID:     uuid,
			Value:    item.sum / item.count,
			Peak:     item.peak,
			PeakTime: item.peakTime,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Value > out[j].Value })
	return out
}
