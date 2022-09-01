//go:build windows
// +build windows

package zerousb

// #include "./libwdi/libwdi/libwdi.h"
import "C"

import (
	"fmt"
)

type wdiDeviceInfo *C.wdi_device_info
const infName    "usb_device.inf"
const defaultDir "usb_driver"

func installWindowsDrivers(vid uint, pid uint) {
	ldev = &{}
	r := C.wdi_prepare_driver(&dev, ext_dir, inf_name, &opd)

	fmt.Printf("%v \n", C.wdi_strerror(r))

	if (r != C.WDI_SUCCESS) {
		return
	}

	// Try to match against a plugged device to avoid device manager prompts
	matching_device_found = FALSE;
	if (C.wdi_create_list(&ldev, &ocl) == C.WDI_SUCCESS) {
		r = C.WDI_SUCCESS;
		for (; (ldev != NULL) && (r == WDI_SUCCESS); ldev = ldev->next) {
			if ( (ldev->vid == dev.vid) && (ldev->pid == dev.pid) && (ldev->mi == dev.mi) &&(ldev->is_composite == dev.is_composite) ) {
				
				dev.hardware_id = ldev->hardware_id;
				dev.device_id = ldev->device_id;
				matching_device_found = TRUE;
				fmt.Printf("%v: ", dev.hardware_id);
				r = C.wdi_install_driver(&dev, ext_dir, inf_name, &oid);
				fmt.Printf("%v\n", C.wdi_strerror(r));
			}
		}
	}

	// No plugged USB device matches this one -> install driver
	if (!matching_device_found) {
		r = C.wdi_install_driver(&dev, ext_dir, inf_name, &oid);
		fmt.Prrintf("%v\n", C.wdi_strerror(r));
	}
}
