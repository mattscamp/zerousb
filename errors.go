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
// static inline const char* libusb_strerror_wrapper (int code) {
// 	return libusb_strerror(code);
// }
import "C"

// ErrorCode is the type for the libusb_error C enum.
type ErrorCode int

// Error implements the Go error interface for ErrorCode.
func (err ErrorCode) Error() string {
	return fmt.Sprintf("%v: %v",
		ErrorName(err),
		StrError(err),
	)
}

// ErrorName implements the libusb_error_name function.
func ErrorName(err ErrorCode) string {
	return C.GoString(C.libusb_error_name(C.int(err)))
}

// StrError implements the libusb_strerror function.
func StrError(err ErrorCode) string {
	return C.GoString(C.libusb_strerror_wrapper(C.int(err)))
}

const (
	success           ErrorCode = C.LIBUSB_SUCCESS
	errorIo           ErrorCode = C.LIBUSB_ERROR_IO
	errorInvalidParam ErrorCode = C.LIBUSB_ERROR_INVALID_PARAM
	errorAccess       ErrorCode = C.LIBUSB_ERROR_ACCESS
	errorNoDevice     ErrorCode = C.LIBUSB_ERROR_NO_DEVICE
	errorNotFound     ErrorCode = C.LIBUSB_ERROR_NOT_FOUND
	errorBusy         ErrorCode = C.LIBUSB_ERROR_BUSY
	errorTimeout      ErrorCode = C.LIBUSB_ERROR_TIMEOUT
	errorOverflow     ErrorCode = C.LIBUSB_ERROR_OVERFLOW
	errorPipe         ErrorCode = C.LIBUSB_ERROR_PIPE
	errorInterrupted  ErrorCode = C.LIBUSB_ERROR_INTERRUPTED
	errorNoMem        ErrorCode = C.LIBUSB_ERROR_NO_MEM
	errorNotSupported ErrorCode = C.LIBUSB_ERROR_NOT_SUPPORTED
	errorOther        ErrorCode = C.LIBUSB_ERROR_OTHER

	errorTransferError    ErrorCode = C.LIBUSB_TRANSFER_ERROR
	errorTransferTimedOut ErrorCode = C.LIBUSB_TRANSFER_TIMED_OUT
	errorTransferCanceled ErrorCode = C.LIBUSB_TRANSFER_CANCELLED
	errorTransferStall    ErrorCode = C.LIBUSB_TRANSFER_STALL
	errorTransferNoDevice ErrorCode = C.LIBUSB_TRANSFER_NO_DEVICE
	errorTransferOverflow ErrorCode = C.LIBUSB_TRANSFER_OVERFLOW
)
