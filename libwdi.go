//go:build windows
// +build windows

package zerousb

// #include "./libwdi/libwdi/libwdi.h"
import "C"

import (
	"fmt"
	"unsafe"
)

type wdiDeviceInfo struct {
	c C.struct_wdi_device_info
}
type wdiOptionsCreateList struct {
	c C.struct_wdi_options_create_list
}
type wdiOptionsPrepareDriver struct {
	c C.struct_wdi_options_prepare_driver
}
type wdiOptionsInstallDriver struct {
	c C.struct_wdi_options_install_driver
}

func installWindowsDrivers(vid uint, pid uint, desc string) {
	infName := C.CString("usb_device.inf")
	defaultDir := C.CString("usb_driver")
	description := C.CString(desc)

	defer C.free(unsafe.Pointer(infName))
	defer C.free(unsafe.Pointer(defaultDir))
	defer C.free(unsafe.Pointer(description))

	dev := &C.struct_wdi_device_info{
		vid:  (C.ushort)(vid),
		pid:  (C.ushort)(pid),
		desc: description,
	}
	defer C.free(unsafe.Pointer(dev))
	ldev := &C.struct_wdi_device_info{}
	defer C.free(unsafe.Pointer(ldev))
	opd := &C.struct_wdi_options_prepare_driver{
		driver_type: C.WDI_WINUSB,
	}
	defer C.free(unsafe.Pointer(opd))
	ocl := &C.struct_wdi_options_create_list{}
	defer C.free(unsafe.Pointer(ocl))
	oid := &C.struct_wdi_options_install_driver{}
	defer C.free(unsafe.Pointer(oid))

	r := C.wdi_prepare_driver(dev, defaultDir, infName, opd)

	fmt.Printf("%v \n", C.wdi_strerror(r))

	if r != C.WDI_SUCCESS {
		return
	}
	fmt.Printf("%v \n", r)

	// Try to match against a plugged device to avoid device manager prompts
	foundDevice := false
	if C.wdi_create_list(&ldev, ocl) == C.WDI_SUCCESS {
		r = C.WDI_SUCCESS
		for ldev != nil && r == C.WDI_SUCCESS {
			ldev = ldev.next
			if (ldev.vid == dev.vid) && (ldev.pid == dev.pid) && (ldev.mi == dev.mi) && (ldev.is_composite == dev.is_composite) {
				dev.hardware_id = ldev.hardware_id
				dev.device_id = ldev.device_id
				foundDevice = true
				fmt.Printf("%v: ", dev.hardware_id)
				r = C.wdi_install_driver(dev, defaultDir, infName, oid)
				fmt.Printf("%v\n", C.wdi_strerror(r))
			}
		}
	}

	// No plugged USB device matches this one -> install driver
	if !foundDevice {
		r = C.wdi_install_driver(dev, defaultDir, infName, oid)
		fmt.Printf("%v\n", C.wdi_strerror(r))
	}
}
