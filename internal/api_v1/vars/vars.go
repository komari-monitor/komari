package vars

import (
	"time"

	"github.com/patrickmn/go-cache"
)

var (
	Records = cache.New(1*time.Minute, 1*time.Minute)
)
