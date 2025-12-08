package dbcore

import (
	"sync"

	"gorm.io/gorm"
)

var (
	instance *gorm.DB
	once     sync.Once
)

func GetDBInstance() *gorm.DB {
	return instance
}
