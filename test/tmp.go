package main

// int argadjust(int arg) { return 3 + arg; }
import "C"

// XXX here because cannot use C in tests directly
func cargadjust(arg int) int {
	return int(C.argadjust(C.int(arg)))
}
