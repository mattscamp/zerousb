// go:build (freebsd && cgo) || (linux && cgo) || (darwin && !ios && cgo) || (windows && cgo)

package zerousb

/*
#cgo CFLAGS: -I./libusb/libusb
#cgo CFLAGS: -DDEFAULT_VISIBILITY=""
#cgo CFLAGS: -DPOLL_NFDS_TYPE=int

#cgo linux CFLAGS: -DOS_LINUX -D_GNU_SOURCE -DHAVE_SYS_TIME_H
#cgo linux,!android LDFLAGS: -lrt
#cgo darwin CFLAGS: -DOS_DARWIN -DHAVE_SYS_TIME_H
#cgo darwin LDFLAGS: -framework CoreFoundation -framework IOKit -lobjc
#cgo windows CFLAGS: -DOS_WINDOWS
#cgo windows LDFLAGS: -lsetupapi
#cgo freebsd CFLAGS: -DOS_FREEBSD
#cgo freebsd LDFLAGS: -lusb
#cgo openbsd CFLAGS: -DOS_OPENBSD

#if defined(OS_LINUX) || defined(OS_DARWIN) || defined(DOS_FREEBSD) || defined(OS_OPENBSD)
	#include <poll.h>
	#include "os/threads_posix.c"
	#include "os/poll_posix.c"
#elif defined(OS_WINDOWS)
	#include "os/poll_windows.c"
	#include "os/threads_windows.c"
#endif

#ifdef OS_LINUX
	#include "os/linux_usbfs.c"
	#include "os/linux_netlink.c"
#elif OS_DARWIN
	#include "os/darwin_usb.c"
#elif OS_WINDOWS
	#include "os/windows_nt_common.c"
	#include "os/windows_usbdk.c"
	#include "os/windows_winusb.c"
#elif OS_FREEBSD
	#include <libusb.h>
#elif DOS_OPENBSD
	#include "os/openbsd_usb.c"
#endif

#ifndef OS_FREEBSD
	#include "core.c"
	#include "descriptor.c"
	#include "hotplug.c"
	#include "io.c"
	#include "strerror.c"
	#include "sync.c"
#endif
*/
import "C"
