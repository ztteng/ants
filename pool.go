package ants

import (
	"sync"
	"sync/atomic"
	"time"
)

// Pool accept the tasks from client,it limits the total
// of goroutines to a given number by recycling goroutines.
type Pool struct {
	capacity       int32         //线程数量
	running        int32         //正在运行的线程数量
	expiryDuration time.Duration //定时清除过期协程时间

	workers []*Worker //这里是存储有用的work结构体 work最大数量等于capacity

	// release is used to notice the pool to closed itself.
	release int32 //关闭协程标志

	// lock for synchronous operation.
	lock sync.Mutex //主要是控制workers

	// cond for waiting to get a idle worker.
	cond *sync.Cond //

	once sync.Once //释放线程池实例的时候保证只执行一次

	// workerCache speeds up the obtainment of the an usable worker in function:retrieveWorker.
	workerCache sync.Pool

	// PanicHandler is used to handle panics from each worker goroutine.
	// if nil, panics will be thrown out again from worker goroutines.
	PanicHandler func(interface{})
}

// 定期清理过期的work. 退出等待执行任务的work协程并把work设置为nil
func (p *Pool) periodicallyPurge() {
	heartbeat := time.NewTicker(p.expiryDuration)
	defer heartbeat.Stop() //记得需要释放timer

	for range heartbeat.C {
		if CLOSED == atomic.LoadInt32(&p.release) { //p.release是线程池关闭标志
			break
		}
		currentTime := time.Now()
		p.lock.Lock()
		idleWorkers := p.workers
		n := -1
		for i, w := range idleWorkers {
			if currentTime.Sub(w.recycleTime) <= p.expiryDuration {
				//因为work放在最前的是时间最长的.如果第一个都没过期.后面肯定也没过期.所以这里直接break不用继续处理
				break
			}
			n = i
			w.task <- nil
			idleWorkers[i] = nil
		}
		if n > -1 {
			if n >= len(idleWorkers)-1 {
				p.workers = idleWorkers[:0]
			} else {
				p.workers = idleWorkers[n+1:]
			}
		}
		p.lock.Unlock()
	}
}

func NewPool(size int) (*Pool, error) {
	return NewTimingPool(size, DEFAULT_CLEAN_INTERVAL_TIME)
}

func NewTimingPool(size, expiry int) (*Pool, error) {
	if size <= 0 {
		return nil, ErrInvalidPoolSize
	}
	if expiry <= 0 {
		return nil, ErrInvalidPoolExpiry
	}
	p := &Pool{
		capacity:       int32(size),
		expiryDuration: time.Duration(expiry) * time.Second,
	}
	p.cond = sync.NewCond(&p.lock)
	go p.periodicallyPurge() //定时清理过期work
	return p, nil
}

//---------------------------------------------------------------------------

func (p *Pool) Submit(task func()) error {
	if CLOSED == atomic.LoadInt32(&p.release) {
		return ErrPoolClosed
	}
	p.retrieveWorker().task <- task
	return nil
}

// 返回正在运行的协程
func (p *Pool) Running() int {
	return int(atomic.LoadInt32(&p.running))
}

// 返回空闲的协程
func (p *Pool) Free() int {
	return int(atomic.LoadInt32(&p.capacity) - atomic.LoadInt32(&p.running))
}

// 返回协程最大容量
func (p *Pool) Cap() int {
	return int(atomic.LoadInt32(&p.capacity))
}

// 重置协程容量
// 如果是扩容直接更改容量字段就好.
// 如果是缩容.正在执行的数量大于要更改后的数量.需要停掉这之间的差值协程
func (p *Pool) Tune(size int) {
	if size == p.Cap() {
		return
	}
	atomic.StoreInt32(&p.capacity, int32(size))
	diff := p.Running() - size
	for i := 0; i < diff; i++ {
		p.retrieveWorker().task <- nil
	}
}

//释放协程示例
func (p *Pool) Release() error {
	p.once.Do(func() {
		atomic.StoreInt32(&p.release, 1)
		p.lock.Lock()
		idleWorkers := p.workers
		for i, w := range idleWorkers {
			w.task <- nil //正在等待任务执行的协程程收到nil信号会退出协程
			idleWorkers[i] = nil
		}
		p.workers = nil
		p.lock.Unlock()
	})
	return nil
}

//---------------------------------------------------------------------------

//增加正在运行的协程数量
func (p *Pool) incRunning() {
	atomic.AddInt32(&p.running, 1)
}

//减少正在运行的协程数量
func (p *Pool) decRunning() {
	atomic.AddInt32(&p.running, -1)
}

// retrieveWorker returns a available worker to run the tasks.
func (p *Pool) retrieveWorker() *Worker {
	var w *Worker

	p.lock.Lock()
	idleWorkers := p.workers
	n := len(idleWorkers) - 1
	if n >= 0 {
		w = idleWorkers[n]
		idleWorkers[n] = nil
		p.workers = idleWorkers[:n]
		p.lock.Unlock()
	} else if p.Running() < p.Cap() {
		//如果正在运行的线程小于设置的线程数量. 则新建work
		p.lock.Unlock()
		if cacheWorker := p.workerCache.Get(); cacheWorker != nil {
			w = cacheWorker.(*Worker)
		} else {
			w = &Worker{
				pool: p,
				task: make(chan func(), 1),
			}
		}
		w.run() //创建协程 协程阻塞在chan里. 等待具体需要执行的任务函数
	} else {
		//如果 线程全部跑满 这里阻塞等待有新的线程释放
		for {
			p.cond.Wait()
			l := len(p.workers) - 1
			if l < 0 {
				continue
			}
			w = p.workers[l]
			p.workers[l] = nil
			p.workers = p.workers[:l]
			break
		}
		p.lock.Unlock()
	}
	return w
}

// revertWorker puts a worker back into free pool, recycling the goroutines.
func (p *Pool) revertWorker(worker *Worker) bool {
	if CLOSED == atomic.LoadInt32(&p.release) {
		return false
	}
	worker.recycleTime = time.Now()
	p.lock.Lock()
	p.workers = append(p.workers, worker)
	// Notify the invoker stuck in 'retrieveWorker()' of there is an available worker in the worker queue.
	p.cond.Signal()
	p.lock.Unlock()
	return true
}
