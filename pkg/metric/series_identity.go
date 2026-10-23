package metric

import "encoding/json"

// SeriesIdentity is an immutable snapshot of output entity and Tags, not a
// fingerprint. Its canonical key uses the historical chart JSON ordering,
// including the distinction between nil and empty Tags. Labels are excluded.
// Do not reuse an identity after changing a point's entity or Tags.
type SeriesIdentity struct{ key string }

// NewSeriesIdentity snapshots output identity once per series. It intentionally
// uses output Tags (including nil when omitted), not the storage tags hash:
// omission and merged aggregates must retain their historical chart grouping.
func NewSeriesIdentity(entity string, tags map[string]string) *SeriesIdentity {
	encoded, _ := json.Marshal(struct {
		Entity string
		Tags   map[string]string
	}{entity, tags})
	return &SeriesIdentity{key: string(encoded)}
}

// Key is collision-free with respect to the historical JSON grouping key and
// preserves its lexical ordering. A nil/zero identity requests caller fallback.
func (s *SeriesIdentity) Key() string {
	if s == nil {
		return ""
	}
	return s.key
}
