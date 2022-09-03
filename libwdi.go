//go:build windows
// +build windows

package zerousb

/*
	#include "./libwdi/libwdi/libwdi.h"

	struct wdi_device_info {
		struct wdi_device_info *next;
		unsigned short vid;
		unsigned short pid;
		BOOL is_composite;
		unsigned char mi;
		char* desc;
		char* driver;
		char* device_id;
		char* hardware_id;
		char* compatible_id;
		char* upper_filter;
		UINT64 driver_version;
	};

	struct wdi_options_create_list {
		BOOL list_all;
		BOOL list_hubs;
		BOOL trim_whitespaces;
	};

	struct wdi_options_prepare_driver {
		int driver_type;
		char* vendor_name;
		char* device_guid;
		BOOL disable_cat;
		BOOL disable_signing;
		char* cert_subject;
		BOOL use_wcid_driver;
		BOOL external_inf;
	};

	struct wdi_options_install_driver {
		HWND hWnd;
		BOOL install_filter_driver;
		UINT32 pending_install_timeout;
	};
*/
import "C"

import (
	"fmt"
)

type wdiDeviceInfo struct {
	c C.wdi_device_info
}
type wdiOptionsCreateList struct {
	c C.wdi_options_create_list
}
type wdiOptionsPrepareDriver struct {
	c C.wdi_options_prepare_driver
}
type wdiOptionsInstallDriver struct {
	c C.wdi_options_install_driver
}

const infName =   "usb_device.inf"
const defaultDir = "usb_driver"

func installWindowsDrivers(vid uint, pid uint) {
	dev := &wdiDeviceInfo{
		c: C.wdi_device_info
	}
	opd := wdiOptionsPrepareDriver{
		c: C.wdi_options_prepare_driver{
			driver_type: C.WDI_WINUSB,
		}
	}
	r := C.wdi_prepare_driver(&dev, defaultDir, infName, &opd.c)

	fmt.Printf("%v \n", C.wdi_strerror(r))

	if (r != C.WDI_SUCCESS) {
		return
	}
	fmt.Printf("%v \n", r)

	// Try to match against a plugged device to avoid device manager prompts
	foundDevice := false
	if (C.wdi_create_list(&ldev, &ocl) == C.WDI_SUCCESS) {
		r = C.WDI_SUCCESS;
		for (ldev != NULL) && (r == WDI_SUCCESS) {
			ldev = ldev->next
			if (ldev.vid == dev.vid) && (ldev.pid == dev.pid) && (ldev.mi == dev.mi) &&(ldev.is_composite == dev.is_composite) {
				dev.hardware_id = ldev.hardware_id
				dev.device_id = ldev.device_id
				foundDevice = true
				fmt.Printf("%v: ", dev.hardware_id);
				r = C.wdi_install_driver(&dev, ext_dir, inf_name, &oid);
				fmt.Printf("%v\n", C.wdi_strerror(r));
			}
		}
	}

	// No plugged USB device matches this one -> install driver
	if (!foundDevice) {
		r = C.wdi_install_driver(&dev, ext_dir, inf_name, &oid);
		fmt.Prrintf("%v\n", C.wdi_strerror(r));
	}
}
