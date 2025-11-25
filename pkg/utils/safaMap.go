package utils

import (
	"sync"
)

// SafeMap 是一个线程安全的泛型映射类型
// 使用 sync.RWMutex 来确保并发访问的安全性
type SafeMap[K comparable, V any] struct {
	mu    sync.RWMutex // 读写锁，用于保护 items 的并发访问
	items map[K]V      // 存储键值对的底层映射
}

// NewSafeMap 创建一个新的 SafeMap 实例
// 返回一个指向 SafeMap 的指针
func NewSafeMap[K comparable, V any]() *SafeMap[K, V] {
	return &SafeMap[K, V]{
		items: make(map[K]V), // 初始化底层映射
	}
}

// Set 添加或更新映射中的键值对
// 使用写锁保护操作
func (sm *SafeMap[K, V]) Set(key K, value V) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.items[key] = value
}

// Store 添加或更新映射中的键值对
// 是 Set 方法的别名
func (sm *SafeMap[K, V]) Store(key K, value V) {
	sm.Set(key, value)
}

// Get 根据键从映射中检索值
// 返回值和一个布尔值，指示键是否存在
func (sm *SafeMap[K, V]) Get(key K) (V, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	value, exists := sm.items[key]
	return value, exists
}

// Delete 从映射中移除指定键的键值对
// 使用写锁保护操作
func (sm *SafeMap[K, V]) Delete(key K) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	delete(sm.items, key)
}

// Keys 返回映射中所有键的切片
// 使用读锁保护操作
func (sm *SafeMap[K, V]) Keys() []K {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	keys := make([]K, 0, len(sm.items))
	for key := range sm.items {
		keys = append(keys, key)
	}
	return keys
}

// Len 返回映射中键值对的数量
// 使用读锁保护操作
func (sm *SafeMap[K, V]) Len() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return len(sm.items)
}

// Range 遍历映射中的所有键值对
// 接受一个函数作为参数，如果函数返回 false，则停止遍历
func (sm *SafeMap[K, V]) Range(f func(key K, value V) bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	for key, value := range sm.items {
		if !f(key, value) {
			break
		}
	}
}
