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
	"syscall"
	"unsafe"

	"github.com/cloudwego/netpoll/syncx"
)

var linkedPool = syncx.Pool{
	NoGC: true,
	New: func() interface{} {
		return &linkBufferNode{
			refer: 1,
		}
		// node := memnodes.alloc()
		// node.refer = 1 // 自带 1 引用
		// return node
	},
}

func init() {
	memnodes = &memallocs{
		// cache: make(map[int][]byte),
		cache: make([]*linkBufferNode, 0, 1024),
	}
	runtime.KeepAlive(memnodes)
}

var memnodes *memallocs

type memallocs struct {
	locked int32
	first  *linkBufferNode
	cache  []*linkBufferNode
}

func (c *memallocs) alloc() *linkBufferNode {
	c.lock()
	if c.first == nil {
		const opSize = unsafe.Sizeof(linkBufferNode{})
		n := block4k / opSize
		if n == 0 {
			n = 1
		}
		// Must be in non-GC memory because can be referenced
		// only from epoll/kqueue internals.
		tmp, _ := syscall.Mmap(-1, 0, int(n*opSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
		mem := uintptr(unsafe.Pointer(&tmp[0]))
		// mem := persistentalloc(n * opSize)
		for i := uintptr(0); i < n; i++ {
			pd := (*linkBufferNode)(unsafe.Pointer(mem + i*opSize))
			// runtime.KeepAlive(pd)
			pd.next2 = c.first
			c.first = pd
		}
	}
	op := c.first
	c.first = op.next2
	c.unlock()
	return op
}

func (c *memallocs) lock() {
	for !atomic.CompareAndSwapInt32(&c.locked, 0, 1) {
		runtime.Gosched()
	}
}

func (c *memallocs) unlock() {
	atomic.StoreInt32(&c.locked, 0)
}

func persistentalloc(size uintptr) uintptr {
	p, err := mmap(nil, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE, -1, 0)
	if err != nil {
		if err == syscall.EACCES {
			panic("runtime: mmap: access denied")
		}
		if err == syscall.EAGAIN {
			panic("runtime: mmap: too much locked memory (check 'ulimit -l').")
		}
		return 0
	}
	return p
}

func mmap(addr unsafe.Pointer, size uintptr, prot, flags, fd, offset int) (ptr uintptr, err error) {
	r, _, e := syscall.Syscall6(syscall.SYS_MMAP, uintptr(addr), size, uintptr(prot), uintptr(flags), uintptr(fd), uintptr(offset))
	if e != 0 {
		return 0, e
	}
	return r, nil
}

func munmap(addr uintptr, size int) (err error) {
	_, _, e := syscall.Syscall(syscall.SYS_MUNMAP, addr, uintptr(size), 0)
	if e != 0 {
		return e
	}
	return nil
}
