package checkpoint

import (
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
)

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func timestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse workflow timestamp: %w", err)
	}
	return parsed, nil
}

func optionalTime(value sql.NullString) (time.Time, error) {
	if !value.Valid {
		return time.Time{}, nil
	}
	return parseTime(value.String)
}

func sortRecords(records []workflow.NodeRecord) {
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
}
