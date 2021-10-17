// Copyright 2021 CloudWeGo Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package netpoll

import (
	"runtime"
	"sync/atomic"
	"unsafe"
)

func allocop() *FDOperator {
	return opcache.alloc()
}

func freeop(op *FDOperator) {
	opcache.free(op)
}

func init() {
	opcache = &operatorCache{
		// cache: make(map[int][]byte),
		//  //  通过指针实现的0 GC操作  性质其实是相同的
		cache: make([]*FDOperator, 0, 1024),
	}
	runtime.KeepAlive(opcache)
}

var opcache *operatorCache

type operatorCache struct {
	locked int32
	first  *FDOperator
	cache  []*FDOperator
}

// 分配链表的内存
func (c *operatorCache) alloc() *FDOperator {
	c.lock()
	if c.first == nil {
		const opSize = unsafe.Sizeof(FDOperator{})
		n := block4k / opSize
		if n == 0 {
			n = 1
		}

		//  必须在非GC内存中，因为可以引用( 是如何被分配到非GC 内存当中的？？？)
		// Must be in non-GC memory because can be referenced
		// only from epoll/kqueue internals.
		for i := uintptr(0); i < n; i++ {
			pd := &FDOperator{}
			c.cache = append(c.cache, pd)
			pd.next = c.first
			c.first = pd //  采用链表的头插 法
		}
	}
	op := c.first
	c.first = op.next
	c.unlock()
	return op
}

// 对申请的链表进行释放操作
func (c *operatorCache) free(op *FDOperator) {
	if !op.isUnused() {
		panic("op is using now")
	}
	op.reset()
	c.lock()
	op.next = c.first
	c.first = op
	c.unlock()
}

func (c *operatorCache) lock() {
	for !atomic.CompareAndSwapInt32(&c.locked, 0, 1) {
		runtime.Gosched() // 没有锁上让出当前P 进行重新调度
	}
}

func (c *operatorCache) unlock() {
	atomic.StoreInt32(&c.locked, 0)
}
