// Package usb provide interfaces for generic USB devices.
package zerousb

import (
	"errors"
	"fmt"
	"sync"
)

// lock is a mutex for locking access to the device.
var lock sync.Mutex

// ID represents a vendor or product ID.
type ID uint16

// String returns a hexadecimal ID.
func (id ID) String() string {
	return fmt.Sprintf("%04x", int(id))
}

// ErrDeviceClosed is returned for operations where the device closed before or
// during the execution.
var ErrDeviceClosed = errors.New("usb: device closed")

// ErrUnsupportedPlatform is returned for all operations where the underlying
// operating system is not supported by the library.
var ErrUnsupportedPlatform = errors.New("usb: unsupported platform")

// DeviceInfo contains all the information we know about a USB device. In case of
// HID devices, that might be a lot more extensive (empty fields for raw USB).
type DeviceInfo struct {
	Path         string // Platform-specific device path
	VendorID     uint16 // Device Vendor ID
	ProductID    uint16 // Device Product ID
	Release      uint16 // Device Release Number in binary-coded decimal, also known as Device Version Number
	Serial       string // Serial Number
	Manufacturer string // Manufacturer String
	Product      string // Product string
	UsagePage    uint16 // Usage Page for this Device/Interface (Windows/Mac only)
	Usage        uint16 // Usage for this Device/Interface (Windows/Mac only)

	// The USB interface which this logical device
	// represents. Valid on both Linux implementations
	// in all cases, and valid on the Windows implementation
	// only if the device contains more than one interface.
	Interface int

	// Raw low level libusb endpoint data for simplified communication
	libusbDevice       interface{}
	libusbPort         *uint8 // Pointer to differentiate between unset and port 0
	libusbReader       *uint8 // Pointer to differentiate between unset and endpoint 0
	libusbWriter       *uint8 // Pointer to differentiate between unset and endpoint 0
	readerTransferType *uint8
	writerTransferType *uint8
}

// Device is a generic USB device interface. It currently only a libusb device.
type Device interface {
	// Close releases the USB device.
	Close() error

	// Write sends a binary blob to a USB device. Uses interrupt or bulk transfers.
	Write(b []byte) (int, error)

	// Read retrieves a binary blob from a USB device. Uses interrupt or bulk transfers.
	Read(b []byte) (int, error)
}

// Find returns a list of all the USB devices attached to the system and
// match the vendor and product id unless:
//  - If the vendor id is set to 0 then any vendor matches.
//  - If the product id is set to 0 then any product matches.
//  - If the vendor and product id are both 0, all devices are returned.
func Find(vendorID ID, productID ID) ([]DeviceInfo, error) {
	lock.Lock()
	defer lock.Unlock()

	return getAllDevices(vendorID, productID)
}

// Open connects to a previsouly discovered USB device.
func (info DeviceInfo) Open() (Device, error) {
	lock.Lock()
	defer lock.Unlock()

	return open(info)
}
