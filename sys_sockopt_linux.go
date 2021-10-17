// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// This file may have been modified by CloudWeGo authors. (“CloudWeGo Modifications”).
// All CloudWeGo Modifications are Copyright 2021 CloudWeGo authors.

package netpoll

import (
	"os"
	"syscall"
)

func setDefaultSockopts(s, family, sotype int, ipv6only bool) error {
	if family == syscall.AF_INET6 && sotype != syscall.SOCK_RAW {
		// Allow both IP versions even if the OS default
		// is otherwise. Note that some operating systems
		// never admit this option.
		syscall.SetsockoptInt(s, syscall.IPPROTO_IPV6, syscall.IPV6_V6ONLY, boolint(ipv6only))
	}

	// Allow broadcast.

	// 允许广播的情况发生
	return os.NewSyscallError("setsockopt", syscall.SetsockoptInt(s, syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1))
}

// Boolean to int.
func boolint(b bool) int {
	if b {
		return 1
	}
	return 0
}
