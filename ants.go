package ants

import (
	"errors"
	"math"
)

const (
	DEFAULT_ANTS_POOL_SIZE      = math.MaxInt32 //默认线程梳理
	DEFAULT_CLEAN_INTERVAL_TIME = 1             //默认清除协程时间
	CLOSED                      = 1             //关闭协程标志
)

var (
	//错误类型
	ErrInvalidPoolSize   = errors.New("invalid size for pool")
	ErrInvalidPoolExpiry = errors.New("invalid expiry for pool")
	ErrPoolClosed        = errors.New("this pool has been closed")

	//引用该包的时候 会默认创建一个线程池实例
	defaultAntsPool, _ = NewPool(DEFAULT_ANTS_POOL_SIZE)
)

// 线程具体要执行的任务
func Submit(task func()) error {
	return defaultAntsPool.Submit(task)
}

// 返回默认线程池实例正在运行的线程数量
func Running() int {
	return defaultAntsPool.Running()
}

// 返回默认线程池实例线程最大总数
func Cap() int {
	return defaultAntsPool.Cap()
}

// 返回默认线程池实例现在可用线程
func Free() int {
	return defaultAntsPool.Free()
}

// 释放默认线程池实例
func Release() {
	_ = defaultAntsPool.Release()
}
