package metric

import "time"

func timeMillis(t time.Time) int64 { return t.UTC().UnixMilli() }

func fromMillis(value int64) time.Time { return time.UnixMilli(value).UTC() }
