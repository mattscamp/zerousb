// Copyright 2013 Google Inc.  All rights reserved.
// Copyright 2016 the gousb Authors.  All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package zerousb

import (
	"fmt"
)

// #include "./libusb/libusb/libusb.h"
import "C"

// libusbError is an Error code from libusb.
type libusbError C.int

// Error implements the Error interface.
func (e libusbError) Error() string {
	return fmt.Sprintf("libusb: %s [code %d]", libusbErrorString[e], e)
}

// fromLibusbErrno converts a raw libusb Error into a Go type.
func fromLibusbErrno(Errno C.int) error {
	err := libusbError(Errno)
	if err == ErrSuccess {
		return nil
	}
	return err
}

const (
	ErrSuccess      libusbError = C.LIBUSB_SUCCESS
	ErrIO           libusbError = C.LIBUSB_ERROR_IO
	ErrInvalidParam libusbError = C.LIBUSB_ERROR_INVALID_PARAM
	ErrAccess       libusbError = C.LIBUSB_ERROR_ACCESS
	ErrNoDevice     libusbError = C.LIBUSB_ERROR_NO_DEVICE
	ErrNotFound     libusbError = C.LIBUSB_ERROR_NOT_FOUND
	ErrBusy         libusbError = C.LIBUSB_ERROR_BUSY
	ErrTimeout      libusbError = C.LIBUSB_ERROR_TIMEOUT
	ErrOverflow     libusbError = C.LIBUSB_ERROR_OVERFLOW
	ErrPipe         libusbError = C.LIBUSB_ERROR_PIPE
	ErrIntErrupted  libusbError = C.LIBUSB_ERROR_INTERRUPTED
	ErrNoMem        libusbError = C.LIBUSB_ERROR_NO_MEM
	ErrNotSupported libusbError = C.LIBUSB_ERROR_NOT_SUPPORTED
	ErrOther        libusbError = C.LIBUSB_ERROR_OTHER
)

var libusbErrorString = map[libusbError]string{
	ErrSuccess:      "success",
	ErrIO:           "i/o Error",
	ErrInvalidParam: "invalid param",
	ErrAccess:       "bad access",
	ErrNoDevice:     "no device",
	ErrNotFound:     "not found",
	ErrBusy:         "device or resource busy",
	ErrTimeout:      "timeout",
	ErrOverflow:     "overflow",
	ErrPipe:         "pipe Error",
	ErrIntErrupted:  "intErrupted",
	ErrNoMem:        "out of memory",
	ErrNotSupported: "not supported",
	ErrOther:        "unknown Error",
}
