package metric

import "errors"

var (
	ErrInvalidArgument = errors.New("metric: invalid argument")
	ErrNotFound        = errors.New("metric: not found")
	ErrNoData          = errors.New("metric: no data in range")
	ErrAlreadyExists   = errors.New("metric: already exists")
	ErrClosed          = errors.New("metric: store is closed")
)
