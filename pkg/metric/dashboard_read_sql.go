package metric

import (
	"fmt"
	"strings"
)

type dashboardReadPlan struct {
	MetricNames []string
	EntityIDs   []string
	Tags        map[string]string
	StartMilli  int64
	EndMilli    int64
	Fields      rollupReadFields
}

func (d sqliteDialect) renderDashboardRead(tables tables, indexName string, plan dashboardReadPlan) renderedSQL {
	return renderExpandedDashboardRead(d, tables, " INDEXED BY "+indexName, plan)
}

func (d mysqlDialect) renderDashboardRead(tables tables, indexName string, plan dashboardReadPlan) renderedSQL {
	return renderExpandedDashboardRead(d, tables, " FORCE INDEX ("+indexName+")", plan)
}

func (d postgresDialect) renderDashboardRead(tables tables, _ string, plan dashboardReadPlan) renderedSQL {
	args := []any{plan.MetricNames, plan.StartMilli, plan.EndMilli}
	parts := []string{
		"d.metric_name = ANY(" + d.placeholder(1) + "::text[])",
		"d.bucket_milli >= " + d.placeholder(2),
		"d.bucket_milli <= " + d.placeholder(3),
	}
	if len(plan.EntityIDs) > 0 {
		args = append(args, plan.EntityIDs)
		parts = append(parts, "d.entity_id = ANY("+d.placeholder(len(args))+"::text[])")
	}
	for _, key := range sortedKeys(plan.Tags) {
		args = append(args, plan.Tags[key])
		parts = append(parts, d.jsonExtractEquals("d.tags", key, d.placeholder(len(args))))
	}
	return renderedSQL{
		Query: fmt.Sprintf("SELECT %s FROM %s d WHERE %s ORDER BY d.bucket_milli ASC, d.metric_name ASC, d.entity_id ASC, d.labels_hash ASC",
			dashboardReadColumns(plan.Fields), tables.dashboard, strings.Join(parts, " AND ")),
		Args: args,
	}
}

func renderExpandedDashboardRead(d dialect, tables tables, indexHint string, plan dashboardReadPlan) renderedSQL {
	args := make([]any, 0, len(plan.MetricNames)+len(plan.EntityIDs)+len(plan.Tags)+2)
	metricPlaceholders := appendPlaceholders(d, &args, plan.MetricNames)
	parts := []string{
		"d.metric_name IN (" + strings.Join(metricPlaceholders, ", ") + ")",
		"d.bucket_milli >= " + d.placeholder(len(args)+1),
		"d.bucket_milli <= " + d.placeholder(len(args)+2),
	}
	args = append(args, plan.StartMilli, plan.EndMilli)
	if len(plan.EntityIDs) > 0 {
		entityPlaceholders := appendPlaceholders(d, &args, plan.EntityIDs)
		parts = append(parts, "d.entity_id IN ("+strings.Join(entityPlaceholders, ", ")+")")
	}
	for _, key := range sortedKeys(plan.Tags) {
		args = append(args, plan.Tags[key])
		parts = append(parts, d.jsonExtractEquals("d.tags", key, d.placeholder(len(args))))
	}
	return renderedSQL{
		Query: fmt.Sprintf("SELECT %s FROM %s d%s WHERE %s ORDER BY d.bucket_milli ASC, d.metric_name ASC, d.entity_id ASC, d.labels_hash ASC",
			dashboardReadColumns(plan.Fields), tables.dashboard, indexHint, strings.Join(parts, " AND ")),
		Args: args,
	}
}

func dashboardReadColumns(fields rollupReadFields) string {
	columns := []string{
		"d.metric_name", "d.entity_id", "d.tags_hash", "d.tags", "d.labels_hash", "d.labels",
		"d.bucket_milli", "d.count",
	}
	if fields&rollupReadSum != 0 {
		columns = append(columns, "d.sum")
	}
	if fields&rollupReadSumSq != 0 {
		columns = append(columns, "d.sum_sq")
	}
	if fields&rollupReadMin != 0 {
		columns = append(columns, "d.min_val")
	}
	if fields&rollupReadMax != 0 {
		columns = append(columns, "d.max_val")
	}
	if fields&rollupReadFirst != 0 {
		columns = append(columns, "d.first_val", "d.first_ts_milli")
	}
	if fields&rollupReadLast != 0 {
		columns = append(columns, "d.last_val", "d.last_ts_milli")
	}
	if fields&rollupReadDigest != 0 {
		columns = append(columns, "d.digest")
	}
	return strings.Join(columns, ", ")
}
