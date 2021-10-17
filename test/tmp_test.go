package main

import (
	"runtime"
	"testing"
)

type in struct {
	c   chan *out
	arg int
}

type out struct {
	ret int
}

func client(c chan *in, arg int) int {
	rc := make(chan *out)
	c <- &in{
		c:   rc,
		arg: arg,
	}
	ret := <-rc
	return ret.ret
}

func _server(c chan *in, argadjust func(int) int) {
	for r := range c {
		r.c <- &out{ret: argadjust(r.arg)}
	}
}

func server(c chan *in) {
	_server(c, func(arg int) int {
		return 3 + arg
	})
}

func lockedServer(c chan *in) {
	runtime.LockOSThread()
	server(c)
	runtime.UnlockOSThread()
}

// server with 1 C call per request
func cserver(c chan *in) {
	_server(c, cargadjust)
}

// server with 10 C calls per request
func cserver10(c chan *in) {
	_server(c, func(arg int) int {
		for i := 0; i < 10; i++ {
			arg = cargadjust(arg)
		}
		return arg
	})
}

func benchmark(b *testing.B, srv func(chan *in)) {
	inc := make(chan *in)
	go srv(inc)
	for i := 0; i < b.N; i++ {
		client(inc, i)
	}
	close(inc)
}

func BenchmarkUnlocked(b *testing.B) { benchmark(b, server) }
func BenchmarkLocked(b *testing.B)   { benchmark(b, lockedServer) }
func BenchmarkCGo(b *testing.B)      { benchmark(b, cserver) }
func BenchmarkCGo10(b *testing.B)    { benchmark(b, cserver10) }
