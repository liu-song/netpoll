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

//go:build !arm64
// +build !arm64

package netpoll

import (
	"syscall"
	"unsafe"
)

// todo 会用到  EPOLLET  的场景
const EPOLLET = -syscall.EPOLLET

type epollevent struct {
	events uint32
	data   [8]byte // unaligned uintptr
}

// https://www.cnblogs.com/xuewangkai/p/11158576.html   事件的添加和处理

//epoll的事件注册函数，epoll_ctl向 epoll对象中添加、修改或者删除感兴趣的事件，返回0表示成功，否则返回–1，此时需要根据errno错误码判断错误类型。
//
//它不同与select()是在监听事件时告诉内核要监听什么类型的事件，而是在这里先注册要监听的事件类型。
//
//epoll_wait方法返回的事件必然是通过 epoll_ctl添加到 epoll中的。

// EpollCtl implements epoll_ctl.
func EpollCtl(epfd int, op int, fd int, event *epollevent) (err error) {
	_, _, err = syscall.RawSyscall6(syscall.SYS_EPOLL_CTL, uintptr(epfd), uintptr(op), uintptr(fd), uintptr(unsafe.Pointer(event)), 0, 0)
	if err == syscall.Errno(0) {
		err = nil
	}
	return err
}

// EpollWait implements epoll_wait.
func EpollWait(epfd int, events []epollevent, msec int) (n int, err error) {
	var r0 uintptr
	var _p0 = unsafe.Pointer(&events[0])
	if msec == 0 {
		r0, _, err = syscall.RawSyscall6(syscall.SYS_EPOLL_WAIT, uintptr(epfd), uintptr(_p0), uintptr(len(events)), 0, 0, 0)
	} else {
		r0, _, err = syscall.Syscall6(syscall.SYS_EPOLL_WAIT, uintptr(epfd), uintptr(_p0), uintptr(len(events)), uintptr(msec), 0, 0)
	}
	if err == syscall.Errno(0) {
		err = nil
	}
	return int(r0), err
}


// EPOLL_CTL_ADD：注册新的fd到epfd中；
//EPOLL_CTL_MOD：修改已经注册的fd的监听事件；
//EPOLL_CTL_DEL：从epfd中删除一个fd；

//epoll有两种工作模式：LT（水平触发）模式和ET（边缘触发）模式。
//
//默认情况下，epoll采用 LT模式工作，这时可以处理阻塞和非阻塞套接字，而上表中的 EPOLLET表示可以将一个事件改为 ET模式。ET模式的效率要比 LT模式高，它只支持非阻塞套接字。
//
//ET模式与LT模式的区别在于：
//
//当一个新的事件到来时，ET模式下当然可以从 epoll_wait调用中获取到这个事件，可是如果这次没有把这个事件对应的套接字缓冲区处理完，在这个套接字没有新的事件再次到来时，
//在 ET模式下是无法再次从 epoll_wait调用中获取这个事件的；而 LT模式则相反，只要一个事件对应的套接字缓冲区还有数据，
//就总能从 epoll_wait中获取这个事件。因此，在 LT模式下开发基于 epoll的应用要简单一些，
//不太容易出错，而在 ET模式下事件发生时，如果没有彻底地将缓冲区数据处理完，则会导致缓冲区中的用户请求得不到响应。默认情况下，Nginx是通过 ET模式使用 epoll的。
//
//
//结论:
//ET模式仅当状态发生变化的时候才获得通知,这里所谓的状态的变化并不包括缓冲区中还有未处理的数据,也就是说,如果要采用ET模式
//,需要一直read/write直到出错为止,很多人反映为什么采用ET模式只接收了一部分数据就再也得不到通知了,
//大多因为这样;而LT模式是只要有数据没有处理就会一直通知下去的.