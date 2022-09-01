//go:build dummy

// This file is part of a workaround for `go mod vendor` which won't vendor
// C files if there's no Go file in the same directory.
//
// This Go file imports the c directory where there is another dummy.go file which
// is the second part of this workaround.
//
// These two files combined make it so `go mod vendor` behaves correctly.
//
// See this issue for reference: https://github.com/golang/go/issues/26366

package main

import (
	_ "libusb"
	_ "libusb/libusb"
	_ "libusb/libusb/os"
	_ "libwdi"
	_ "libwdi/libwdi"
)
