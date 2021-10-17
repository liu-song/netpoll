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

//go:build !race
// +build !race

package netpoll

import (
	"log"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// Includes defaultPoll/multiPoll/uringPoll...
func openPoll() Poll {
	return openDefaultPoll()
}

// 创建epoll
func openDefaultPoll() *defaultPoll {
	var poll = defaultPoll{}
	poll.buf = make([]byte, 8)
	var p, err = syscall.EpollCreate1(0)
	if err != nil {
		panic(err)
	}
	poll.fd = p
	var r0, _, e0 = syscall.Syscall(syscall.SYS_EVENTFD2, 0, 0, 0)
	if e0 != 0 {
		syscall.Close(p)
		panic(err)
	}

	poll.Reset = poll.reset
	poll.Handler = poll.handler

	poll.wop = &FDOperator{FD: int(r0)}
	poll.Control(poll.wop, PollReadable)
	return &poll
}

type defaultPoll struct {
	pollArgs
	fd      int         // epoll fd
	wop     *FDOperator // eventfd, wake epoll_wait     // 唤醒操作
	buf     []byte      // read wfd trigger msg
	trigger uint32      // trigger flag
	// fns for handle events
	Reset   func(size, caps int)                    //  todo 函数类型的场景下会被调用
	Handler func(events []epollevent) (closed bool) //  todo 这种模式值得学习

	//  这个handler 的处理是只要有就一直处理？ 以及关闭的时候对应的情况是如何的
}

//  poll 的参数可以动态的调整
type pollArgs struct {
	size     int
	caps     int
	events   []epollevent
	barriers []barrier // 事件上等待操作的数据
}

// todo size 和  caps 的 区别  ， caps 是数量？
func (a *pollArgs) reset(size, caps int) {
	a.size, a.caps = size, caps
	a.events, a.barriers = make([]epollevent, size), make([]barrier, size)
	for i := range a.barriers {
		a.barriers[i].bs = make([][]byte, a.caps)
		a.barriers[i].ivs = make([]syscall.Iovec, a.caps)
	}
}

// Wait implements Poll.
func (p *defaultPoll) Wait() (err error) {
	// init
	var caps, msec, n = barriercap, -1, 0
	p.Reset(128, caps)
	// wait
	for {
		//
		if n == p.size && p.size < 128*1024 {
			p.Reset(p.size<<1, caps)
		}
		n, err = EpollWait(p.fd, p.events, msec) //只在这里被调用了，循环调用   // n 是被等待调用的数量
		if err != nil && err != syscall.EINTR {
			return err
		}
		if n <= 0 {
			msec = -1
			runtime.Gosched() //  todo 重点，进行一次调度
			continue
		}
		msec = 0

		//  todo 在for 循环里面  return nil 之后，
		if p.Handler(p.events[:n]) {
			return nil //  return 会直接让当前函数直接退出 ， 会有多少wait呢，退出之后还会动态创建吗？？
		}
	}
}

// 只有所有等待处理的事件都处理完了，才会返回 false //  todo  返回false 会发生对应的什么情况呢
func (p *defaultPoll) handler(events []epollevent) (closed bool) {
	var hups []*FDOperator // TODO: maybe can use sync.Pool
	for i := range events {

		//  这里减少了业务层面的copy
		var operator = *(**FDOperator)(unsafe.Pointer(&events[i].data))
		// trigger or exit gracefully
		if operator.FD == p.wop.FD {
			// must clean trigger first
			syscall.Read(p.wop.FD, p.buf)
			atomic.StoreUint32(&p.trigger, 0)
			// if closed & exit
			if p.buf[0] > 0 {
				syscall.Close(p.wop.FD)
				syscall.Close(p.fd)
				return true
			}
			continue
		}
		if !operator.do() {
			continue
		}
		switch {
		// check hup first
		case events[i].events&(syscall.EPOLLHUP|syscall.EPOLLRDHUP) != 0:
			hups = append(hups, operator)
		case events[i].events&syscall.EPOLLERR != 0:
			// Under block-zerocopy, the kernel may give an error callback, which is not a real error, just an EAGAIN.
			// So here we need to check this error, if it is EAGAIN then do nothing, otherwise still mark as hup.
			if _, _, _, _, err := syscall.Recvmsg(operator.FD, nil, nil, syscall.MSG_ERRQUEUE); err != syscall.EAGAIN {
				hups = append(hups, operator)
			}
			//
		case events[i].events&syscall.EPOLLIN != 0:
			// for non-connection
			if operator.OnRead != nil {
				operator.OnRead(p)
				break
			}
			// only for connection
			var bs = operator.Inputs(p.barriers[i].bs)
			if len(bs) == 0 {
				break
			}
			var n, err = readv(operator.FD, bs, p.barriers[i].ivs)
			operator.InputAck(n)
			if err != nil && err != syscall.EAGAIN && err != syscall.EINTR {
				log.Printf("readv(fd=%d) failed: %s", operator.FD, err.Error())
				hups = append(hups, operator)
			}
		case events[i].events&syscall.EPOLLOUT != 0:
			// for non-connection
			if operator.OnWrite != nil {
				operator.OnWrite(p)
				break
			}
			// only for connection
			var bs, supportZeroCopy = operator.Outputs(p.barriers[i].bs)
			if len(bs) == 0 {
				break
			}
			// TODO: Let the upper layer pass in whether to use ZeroCopy.
			var n, err = sendmsg(operator.FD, bs, p.barriers[i].ivs, false && supportZeroCopy)
			operator.OutputAck(n)
			if err != nil && err != syscall.EAGAIN {
				log.Printf("sendmsg(fd=%d) failed: %s", operator.FD, err.Error())
				hups = append(hups, operator)
			}
		}
		operator.done()
	}
	// hup conns together to avoid blocking the poll.
	if len(hups) > 0 {
		p.detaches(hups)
	}
	return false
}

// Close will write 10000000
func (p *defaultPoll) Close() error {
	_, err := syscall.Write(p.wop.FD, []byte{1, 0, 0, 0, 0, 0, 0, 0})
	return err
}

// Trigger implements Poll.

// todo 目前这个的触发都在 test 当中
func (p *defaultPoll) Trigger() error {
	//  trigger  标记的唯一使用场景
	if atomic.AddUint32(&p.trigger, 1) > 1 {
		return nil
	}
	// MAX(eventfd) = 0xfffffffffffffffe      //  最大的文件操作符 ？？？
	_, err := syscall.Write(p.wop.FD, []byte{0, 0, 0, 0, 0, 0, 0, 1})
	return err
}

// Control implements Poll.
func (p *defaultPoll) Control(operator *FDOperator, event PollEvent) error {
	var op int
	var evt epollevent
	// todo 还是用了很多 unsafe 操作对应的指针操作
	*(**FDOperator)(unsafe.Pointer(&evt.data)) = operator
	switch event {
	case PollReadable:
		operator.inuse()
		op, evt.events = syscall.EPOLL_CTL_ADD, syscall.EPOLLIN|syscall.EPOLLRDHUP|syscall.EPOLLERR
	case PollModReadable:
		operator.inuse()
		op, evt.events = syscall.EPOLL_CTL_MOD, syscall.EPOLLIN|syscall.EPOLLRDHUP|syscall.EPOLLERR
	case PollDetach:
		defer operator.unused() //   唯一的操作符解锁的调用
		op, evt.events = syscall.EPOLL_CTL_DEL, syscall.EPOLLIN|syscall.EPOLLOUT|syscall.EPOLLRDHUP|syscall.EPOLLERR
	case PollWritable:
		operator.inuse()
		op, evt.events = syscall.EPOLL_CTL_ADD, EPOLLET|syscall.EPOLLOUT|syscall.EPOLLRDHUP|syscall.EPOLLERR

		//  后面的两个实现没有对应的原子锁 操作，从内核态到用户态的实现？
	case PollR2RW:
		op, evt.events = syscall.EPOLL_CTL_MOD, syscall.EPOLLIN|syscall.EPOLLOUT|syscall.EPOLLRDHUP|syscall.EPOLLERR
	case PollRW2R:
		op, evt.events = syscall.EPOLL_CTL_MOD, syscall.EPOLLIN|syscall.EPOLLRDHUP|syscall.EPOLLERR
	}
	return EpollCtl(p.fd, op, operator.FD, &evt) // todo 注意这里创建的操作
}

func (p *defaultPoll) detaches(hups []*FDOperator) error {
	var onhups = make([]func(p Poll) error, len(hups))
	for i := range hups {
		onhups[i] = hups[i].OnHup
		p.Control(hups[i], PollDetach)
	}
	//  这俩就用到了早晨学习 的 goroutine 的宝座的操作

	//  遍历赋值，再进行处理 哈哈哈哈
	go func(onhups []func(p Poll) error) {
		for i := range onhups {
			if onhups[i] != nil {
				onhups[i](p)
			}
		}
	}(onhups)
	return nil
}
