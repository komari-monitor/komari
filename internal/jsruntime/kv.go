package jsruntime

import (
	"errors"
	"sync"
)

// RamKv 简单的内存 KV 存储
type RamKv struct {
	data map[string]interface{}
	mu   sync.RWMutex
}

func NewRamKv() *RamKv {
	return &RamKv{
		data: make(map[string]interface{}),
	}
}
func (r *RamKv) Set(key string, value interface{}) error {
	if key == "" {
		return errors.New("key is required")
	}
	r.mu.Lock()
	r.data[key] = value
	r.mu.Unlock()
	return nil
}

func (r *RamKv) Get(key string) (interface{}, bool) {
	r.mu.RLock()
	val, exists := r.data[key]
	r.mu.RUnlock()
	return val, exists
}

func (r *RamKv) Has(key string) bool {
	r.mu.RLock()
	_, exists := r.data[key]
	r.mu.RUnlock()
	return exists
}

func (r *RamKv) Del(key string) {
	r.mu.Lock()
	delete(r.data, key)
	r.mu.Unlock()
}
