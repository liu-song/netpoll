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

//go:build darwin || netbsd || freebsd || openbsd || dragonfly || linux
// +build darwin netbsd freebsd openbsd dragonfly linux

package netpoll

import (
	"context"
	"sync"
)

// 网络IO 主要是处理 网络上的各种事情 。
// A EventLoop is a network server.
type EventLoop interface {
	// Serve registers a listener and runs blockingly to provide services, including listening to ports,
	// accepting connections and processing trans data. When an exception occurs or Shutdown is invoked,
	// Serve will return an error which describes the specific reason.
	Serve(ln Listener) error

	// Shutdown is used to graceful exit.
	// It will close all idle connections on the server, but will not change the underlying pollers.
	//
	// Argument: ctx set the waiting deadline, after which an error will be returned,
	// but will not force the closing of connections in progress.
	Shutdown(ctx context.Context) error
}

// OnRequest defines the function for handling connection. When data is sent from the connection peer,
// netpoll actively reads the data in LT mode and places it in the connection's input buffer.
// Generally, OnRequest starts handling the data in the following way:
//
//	func OnRequest(ctx context, connection Connection) error {
//		input := connection.Reader().Next(n)
//		handling input data...
//  	send, _ := connection.Writer().Malloc(l)
//		copy(send, output)
//		connection.Flush()
//		return nil
//	}
//
// OnRequest will run in a separate goroutine and
// it is guaranteed that there is one and only one OnRequest running at the same time.
// The underlying logic is similar to:
//
//	go func() {
//		for !connection.Reader().IsEmpty() {
//			OnRequest(ctx, connection)
//		}
//	}()
//
// PLEASE NOTE:
// OnRequest must either eventually read all the input data or actively Close the connection,
// otherwise the goroutine will fall into a dead loop.
//
// Return: error is unused which will be ignored directly.
type OnRequest func(ctx context.Context, connection Connection) error

//
//OnPrepare is used to inject custom preparation at connection initialization,
//which is optional but important in some scenarios. For example, a qps limiter
//can be set by closing overloaded connections directly in OnPrepare.
//
//Return:
//context will become the argument of OnRequest.
//Usually, custom resources can be initialized in OnPrepare and used in OnRequest.
//
//PLEASE NOTE:
//OnPrepare is executed without any data in the connection,
//so Reader() or Writer() cannot be used here, but may be supported in the future.
//OnPrepare用于在连接初始化时注入自定义准备，
//
//这是可选的，但在某些情况下很重要。例如，qps限制器
//
//可以通过直接在OnPrepare中关闭重载连接来设置。
//
//返回：
//
//上下文将成为OnRequest的参数。
//
//通常，自定义资源可以在OnPrepare中初始化并在OnRequest中使用。
//
//请注意:
//
//OnPrepare在连接中没有任何数据的情况下执行，
//
//因此，这里不能使用Reader（）或Writer（），但将来可能会支持。
type OnPrepare func(connection Connection) context.Context

// NewEventLoop .
// 网络IO 对连接的处理
func NewEventLoop(onRequest OnRequest, ops ...Option) (EventLoop, error) {
	opt := &options{}
	for _, do := range ops {
		do.f(opt)
	}
	return &eventLoop{
		opt:     opt,
		prepare: opt.prepare(onRequest),
		stop:    make(chan error, 1),
	}, nil
}

type eventLoop struct {
	sync.Mutex
	opt     *options
	prepare OnPrepare
	svr     *server
	stop    chan error
}

// Serve implements EventLoop.
func (evl *eventLoop) Serve(ln Listener) error {
	evl.Lock()
	evl.svr = newServer(ln, evl.prepare, evl.quit)
	evl.svr.Run()
	evl.Unlock()

	return evl.waitQuit() //  注意这种退出机制的使用
}

// Shutdown signals a shutdown an begins server closing.

//  注意一下 shutdown 的时机
func (evl *eventLoop) Shutdown(ctx context.Context) error {
	evl.Lock()
	var svr = evl.svr
	evl.svr = nil
	evl.Unlock()

	if svr == nil {
		return nil
	}
	evl.quit(nil)
	return svr.Close(ctx)
}

// waitQuit waits for a quit signal
//  测试中使用到了这种场景就需要注意一下
func (evl *eventLoop) waitQuit() error {
	return <-evl.stop
}

func (evl *eventLoop) quit(err error) {
	select {
	case evl.stop <- err:
	default:
	}
}
