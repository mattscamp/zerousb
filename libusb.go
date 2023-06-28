//go:build (linux && cgo) || (freebsd && cgo) || (darwin && !ios && cgo) || (windows && cgo) || (openbsd && cgo)
// +build linux,cgo freebsd,cgo darwin,!ios,cgo windows,cgo openbsd,cgo

package zerousb

/*
extern void goLibusbLog(const char *s);
# define ENABLE_LOGGING 1
// #define ENABLE_DEBUG_LOGGING 1
// #define ENUM_DEBUG
#define DEFAULT_VISIBILITY
#cgo CFLAGS: -I./libusb/libusb
#cgo linux CFLAGS: -DOS_LINUX -D_GNU_SOURCE -DPLATFORM_POSIX -DHAVE_CLOCK_GETTIME
#cgo linux,!android LDFLAGS: -lrt
#cgo freebsd CFLAGS: -DOS_FREEBSD -DPLATFORM_POSIX
#cgo freebsd LDFLAGS: -lusb
#cgo openbsd CFLAGS: -DOS_OPENBSD -DPLATFORM_POSIX
#cgo openbsd LDFLAGS: -L/usr/local/lib -lusb-1.0
#cgo darwin CFLAGS: -DOS_DARWIN -DPLATFORM_POSIX
#cgo darwin LDFLAGS: -framework CoreFoundation -framework IOKit -framework Security -lobjc
#cgo windows CFLAGS: -DOS_WINDOWS -DPLATFORM_WINDOWS
#cgo windows LDFLAGS: -lsetupapi

#if defined(OS_LINUX) || defined(OS_DARWIN) || defined(DOS_FREEBSD) || defined(OS_OPENBSD)
	#include "libusbi.h"
	#include <sys/poll.h>
	#include "os/threads_posix.c"
	#include "os/events_posix.c"
#elif defined(OS_WINDOWS)
	#include "os/threads_windows.c"
	#include "os/events_windows.c"
#endif

#ifdef OS_LINUX
	#include "os/linux_usbfs.c"
	#include "os/linux_netlink.c"
#elif OS_DARWIN
	#include "os/darwin_usb.c"
#elif OS_WINDOWS
	#include "os/windows_common.c"
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


static uint8_t *dev_capability_data_ptr(struct libusb_bos_dev_capability_descriptor *x) {
  return &x->dev_capability_data[0];
}
static struct libusb_bos_dev_capability_descriptor **dev_capability_ptr(struct libusb_bos_descriptor *x) {
  return &x->dev_capability[0];
}
*/
import "C"
import (
	"strings"

	libusb "github.com/gotmc/libusb/v2"
)

const EndpointOut = 0x00
const EndpointIn = 0x80

func FindEndpoints(ifaces libusb.SupportedInterfaces) (*libusb.EndpointDescriptor, *libusb.EndpointDescriptor) {
	for _, iface := range ifaces {
		in, out := CheckDescriptor(iface.InterfaceDescriptors)
		if in != nil && out != nil {
			return in, out
		}
	}
	return nil, nil
}

func CheckDescriptor(ifaceDesc libusb.InterfaceDescriptors) (*libusb.EndpointDescriptor, *libusb.EndpointDescriptor) {
	var inEnd *libusb.EndpointDescriptor = nil
	var outEnd *libusb.EndpointDescriptor = nil

	for _, ifaceDesc := range ifaceDesc {
		inEnd, outEnd = CheckEndpoints(ifaceDesc.EndpointDescriptors)
		if inEnd != nil && outEnd != nil {
			break
		}
	}
	return inEnd, outEnd
}

func CheckEndpoints(endpoints libusb.EndpointDescriptors) (*libusb.EndpointDescriptor, *libusb.EndpointDescriptor) {
	var inEnd *libusb.EndpointDescriptor = nil
	var outEnd *libusb.EndpointDescriptor = nil

	for _, endpoint := range endpoints {
		if endpoint.TransferType() == libusb.BulkTransfer || endpoint.TransferType() == libusb.InterruptTransfer {
			if strings.Contains(endpoint.Direction().String(), "device-to-host.") {
				inEnd = endpoint
			} else {
				outEnd = endpoint
			}
		}
	}

	return inEnd, outEnd
}
