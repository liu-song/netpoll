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
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"
)

//  wrap into 把什么包装进什么
// newServer wrap listener into server, quit will be invoked when server exit.
func newServer(ln Listener, prepare OnPrepare, quit func(err error)) *server {
	return &server{
		ln:      ln,
		prepare: prepare,
		quit:    quit,
	}
}

//  主 和 从 reactor 是如何划分的。。
type server struct {
	operator    FDOperator // 文件描述 对应的链表操作符
	ln          Listener   //  Listener extends net.Listener, but supports getting the listener's fd.
	prepare     OnPrepare
	quit        func(err error)
	connections sync.Map // key=fd, value=connection   //  对当前所有的链接进行存储

}

// Run this server.
func (s *server) Run() (err error) {
	s.operator = FDOperator{ //  server 的操作符
		FD:     s.ln.Fd(), //   Listener的操作符。
		OnRead: s.OnRead,  //   为什么只有read这个操作。
		OnHup:  s.OnHup,
	}
	s.operator.poll = pollmanager.Pick()
	err = s.operator.Control(PollReadable) //  PollReadable  是一个常量
	if err != nil {
		s.quit(err)
	}
	return err
}

// Close this server with deadline.
func (s *server) Close(ctx context.Context) error {
	s.operator.Control(PollDetach)
	s.ln.Close()

	var conns []gracefulExit
	s.connections.Range(func(key, value interface{}) bool {
		var conn, ok = value.(gracefulExit)
		if ok && !conn.isIdle() {
			conns = append(conns, conn)
		} else {
			value.(Connection).Close()
		}
		return true
	})

	var ticker = time.NewTicker(time.Second)
	defer ticker.Stop()
	var count = len(conns) - 1
	for count >= 0 {
		for i := count; i >= 0; i-- {
			if conns[i].isIdle() {
				conns[i].Close()
				conns[i] = conns[count]
				count--
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			continue
		}
	}
	return nil
}

// OnRead implements FDOperator.
func (s *server) OnRead(p Poll) error {
	// accept socket
	conn, err := s.ln.Accept()    //  获取到对应的连接
	if err != nil {
		// shut down
		if strings.Contains(err.Error(), "closed") {
			s.operator.Control(PollDetach)
			s.quit(err)
			return err
		}
		log.Println("accept conn failed:", err.Error())
		return err
	}
	if conn == nil {
		return nil
	}
	// store & register connection
	var connection = &connection{}
	connection.init(conn.(Conn), s.prepare)           //  初始化连接 ，并且是准备好的状态了
	if !connection.IsActive() {
		return nil
	}
	var fd = conn.(Conn).Fd()                //  获得了链接对应的FD 状态，然后进行相应的删除
	connection.AddCloseCallback(func(connection Connection) error {
		s.connections.Delete(fd)
		return nil
	})
	s.connections.Store(fd, connection)
	return nil
}

// OnHup implements FDOperator.
func (s *server) OnHup(p Poll) error {
	s.quit(errors.New("listener close"))
	return nil
}
