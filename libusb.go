//go:build (linux && cgo) || (freebsd && cgo) || (darwin && !ios && cgo) || (windows && cgo) || (openbsd && cgo)
// +build linux,cgo freebsd,cgo darwin,!ios,cgo windows,cgo openbsd,cgo

// A large chunk of this code has been imported from https://github.com/gotmc/libusb and modified.
// LICENSE: https://github.com/gotmc/libusb/blob/master/LICENSE.txt

package zerousb

/*
// #define ENABLE_LOGGING 1
// #define ENABLE_DEBUG_LOGGING 1
// #define ENUM_DEBUG
# define DEFAULT_VISIBILITY

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

*/
//
// void set_debug(libusb_context * ctx, int level) {
// #if HAVE_LIBUSB_SET_OPTION
//    libusb_set_option(ctx, LIBUSB_OPTION_LOG_LEVEL, level);
// #else
//    libusb_set_debug(ctx, LIBUSB_LOG_LEVEL_INFO);
// #endif
// }
import "C"
import (
	"fmt"
	"log"
	"math"
	"unsafe"
)

func bcdToDecimal(bcdValue uint16) float64 {
	bcdPowersByPosition := []string{"hundreths", "tenths", "ones", "tens"}

	bcdMap := make(map[string]uint16)
	for i, power := range bcdPowersByPosition {
		bcdMap[power] = bcdValue & (0xf << uint(4*i)) / uint16(math.Pow(16, float64(i)))
	}
	return 10*float64(bcdMap["tens"]) + float64(bcdMap["ones"]) +
		0.1*float64(bcdMap["tenths"]) + 0.01*float64(bcdMap["hundreths"])
}

// LogLevel is an enum for the C libusb log message levels.
type LogLevel int

// Log message levels
//
// http://bit.ly/enum_libusb_log_level
const (
	LogLevelNone    LogLevel = C.LIBUSB_LOG_LEVEL_NONE
	LogLevelError   LogLevel = C.LIBUSB_LOG_LEVEL_ERROR
	LogLevelWarning LogLevel = C.LIBUSB_LOG_LEVEL_WARNING
	LogLevelInfo    LogLevel = C.LIBUSB_LOG_LEVEL_INFO
	LogLevelDebug   LogLevel = C.LIBUSB_LOG_LEVEL_DEBUG
)

var logLevels = map[LogLevel]string{
	LogLevelNone:    "No messages ever printed by the library (default)",
	LogLevelError:   "Error messages are printed to stderr",
	LogLevelWarning: "Warning and error messages are printed to stderr",
	LogLevelInfo:    "Informational messages are printed to stdout, warning and error messages are printed to stderr",
	LogLevelDebug:   "Debug and informational messages are printed to stdout, warnings and errors to stderr",
}

func (level LogLevel) String() string {
	return logLevels[level]
}

// Context represents a libusb session/context.
type Context struct {
	libusbContext *C.libusb_context
	LogLevel      LogLevel
}

// NewContext intializes a new libusb session/context by creating a new
// Context and returning a pointer to that Context.
func NewContext() (*Context, error) {
	newContext := &Context{
		LogLevel: LogLevelNone,
	}
	errnum := C.libusb_init(&newContext.libusbContext)
	if errnum != 0 {
		return nil, fmt.Errorf(
			"failed to initialize new libusb context; received error %d", errnum)
	}
	return newContext, nil
}

// Close deinitializes the libusb session/context.
func (ctx *Context) Close() error {
	C.libusb_exit(ctx.libusbContext)
	ctx.libusbContext = nil
	return nil
}

// SetDebug sets the log message verbosity.
func (ctx *Context) SetDebug(level LogLevel) {
	C.set_debug(ctx.libusbContext, C.int(level))
	ctx.LogLevel = level
}

// DeviceList returns an array of devices for the context.
func (ctx *Context) DeviceList() ([]*Device, error) {
	var devices []*Device
	var list **C.libusb_device
	const unrefDevices = 1
	numDevicesFound := int(C.libusb_get_device_list(ctx.libusbContext, &list))
	if numDevicesFound < 0 {
		return nil, ErrorCode(numDevicesFound)
	}
	defer C.libusb_free_device_list(list, unrefDevices)
	libusbDevices := unsafe.Slice(list, numDevicesFound)
	// var libusbDevices []*C.libusb_device
	// *(*reflect.SliceHeader)(unsafe.Pointer(&libusbDevices)) = reflect.SliceHeader{
	// 	Data: uintptr(unsafe.Pointer(list)),
	// 	Len:  numDevicesFound,
	// 	Cap:  numDevicesFound,
	// }

	for _, thisLibusbDevice := range libusbDevices {
		thisDevice := Device{
			libusbDevice: thisLibusbDevice,
		}
		devices = append(devices, &thisDevice)
	}
	return devices, nil
}

// OpenDeviceWithVendorProduct opens a USB device using the VendorID and
// productID and then returns a device handle.
func (ctx *Context) OpenDeviceWithVendorProduct(
	vendorID uint16,
	productID uint16,
) (*Device, *DeviceHandle, error) {
	var deviceHandle DeviceHandle
	deviceHandle.libusbDeviceHandle = C.libusb_open_device_with_vid_pid(
		ctx.libusbContext, C.uint16_t(vendorID), C.uint16_t(productID))
	if deviceHandle.libusbDeviceHandle == nil {
		return nil, nil, fmt.Errorf("could not open USB device %v:%v",
			vendorID,
			productID,
		)
	}
	device := Device{
		libusbDevice: C.libusb_get_device(deviceHandle.libusbDeviceHandle),
	}
	return &device, &deviceHandle, nil
}

type endpointAddress byte
type endpointAttributes byte

// EndpointDescriptor models the descriptor for a given endpoint.
type EndpointDescriptor struct {
	Length          int
	DescriptorType  descriptorType
	EndpointAddress endpointAddress
	Attributes      endpointAttributes
	MaxPacketSize   uint16
	Interval        uint8
	Refresh         uint8
	SynchAddress    uint8
}

// Direction returns the endpointDirection.
func (end *EndpointDescriptor) Direction() EndpointDirection {
	return end.EndpointAddress.direction()
}

// Number returns the endpoint number in bits 0..3 in the endpoint
// address.
func (end *EndpointDescriptor) Number() byte {
	return end.EndpointAddress.endpointNumber()
}

// TransferType returns the transfer type for an endpoint.
func (end *EndpointDescriptor) TransferType() TransferType {
	return end.Attributes.transferType()
}

func (address endpointAddress) direction() EndpointDirection {
	// Bit 7 of the endpointAddress determines the direction
	const directionMask = 0x80
	const directionBit = 7
	return EndpointDirection(address&directionMask) >> directionBit
}

func (address endpointAddress) endpointNumber() byte {
	// Bits 0..3 determine the endpoint number
	const endpointNumberMask = 0x0F
	return byte(address & endpointNumberMask)
}

func (attributes endpointAttributes) transferType() TransferType {
	// Bits 0..1 of the bmAttributes determines the transfer type
	const transferTypeMask = 0x03
	return TransferType(attributes & transferTypeMask)
}

// SupportedInterface models an supported USB interface and its associated
// interface descriptors.
type SupportedInterface struct {
	InterfaceDescriptors
	NumAltSettings int
}

// SupportedInterfaces contains an array of the supported USB interfaces for a
// given USB device.
type SupportedInterfaces []*SupportedInterface

// InterfaceDescriptor "provides information about a function or feature that a
// device implements." (Source: *USB Complete* 5th edition by Jan Axelson)
type InterfaceDescriptor struct {
	Length              int
	DescriptorType      descriptorType
	InterfaceNumber     int
	AlternateSetting    int
	NumEndpoints        int
	InterfaceClass      uint8
	InterfaceSubClass   uint8
	InterfaceProtocol   uint8
	InterfaceIndex      int
	EndpointDescriptors []*EndpointDescriptor
}

// InterfaceDescriptors contains a slice of pointers to the available interface
// descriptors.
type InterfaceDescriptors []*InterfaceDescriptor

// Config models the USB configuration.
type Config struct {
	*ConfigDescriptor
	Device *Device
}

// ConfigDescriptor models the descriptor for the USB configuration
type ConfigDescriptor struct {
	Length               int
	DescriptorType       descriptorType
	TotalLength          uint16
	NumInterfaces        int
	ConfigurationValue   uint8
	ConfigurationIndex   uint8
	Attributes           uint8
	MaxPowerMilliAmperes uint
	SupportedInterfaces
}

type classCode byte
type bcd uint16

// String implements the Stringer interface for bcd.
func (b bcd) String() string {
	return fmt.Sprintf("%#04x (%2.2f)",
		uint16(b),
		b.AsDecimal(),
	)
}

// AsDecimal converts the BCD value with a format 0xJJMN into a decimal JJ.MN
// where JJ is the major version number, M is the minor version, and N is the
// sub-minor version number.
func (b bcd) AsDecimal() float64 {
	return bcdToDecimal(uint16(b))
}

const (
	perInterface       classCode = C.LIBUSB_CLASS_PER_INTERFACE
	audio              classCode = C.LIBUSB_CLASS_AUDIO
	comm               classCode = C.LIBUSB_CLASS_COMM
	hid                classCode = C.LIBUSB_CLASS_HID
	physical           classCode = C.LIBUSB_CLASS_PHYSICAL
	printer            classCode = C.LIBUSB_CLASS_PRINTER
	ptp                classCode = C.LIBUSB_CLASS_PTP
	image              classCode = C.LIBUSB_CLASS_IMAGE
	massStorage        classCode = C.LIBUSB_CLASS_MASS_STORAGE
	hub                classCode = C.LIBUSB_CLASS_HUB
	data               classCode = C.LIBUSB_CLASS_DATA
	smartCard          classCode = C.LIBUSB_CLASS_SMART_CARD
	contentSecurity    classCode = C.LIBUSB_CLASS_CONTENT_SECURITY
	video              classCode = C.LIBUSB_CLASS_VIDEO
	personalHealthcare classCode = C.LIBUSB_CLASS_PERSONAL_HEALTHCARE
	diagnosticDevice   classCode = C.LIBUSB_CLASS_DIAGNOSTIC_DEVICE
	wireless           classCode = C.LIBUSB_CLASS_WIRELESS
	application        classCode = C.LIBUSB_CLASS_APPLICATION
	vendorSpec         classCode = C.LIBUSB_CLASS_VENDOR_SPEC
)

var classCodes = map[classCode]string{
	perInterface:       "Each interface specifies its own class information and all interfaces operate independently.",
	audio:              "Audio class.",
	comm:               "Communications class.",
	hid:                "Human Interface Device class.",
	physical:           "Physical.",
	printer:            "Printer class.",
	image:              "Image class.",
	massStorage:        "Mass storage class.",
	hub:                "Hub class.",
	data:               "Data class.",
	smartCard:          "Smart Card.",
	contentSecurity:    "Content Security.",
	video:              "Video.",
	personalHealthcare: "Personal Healthcare.",
	diagnosticDevice:   "Diagnostic Device.",
	wireless:           "Wireless class.",
	application:        "Application class.",
	vendorSpec:         "Class is vendor-specific.",
}

// String implements the Stringer interface for classCode.
func (classCode classCode) String() string {
	return classCodes[classCode]
}

type descriptorType byte

const (
	descDevice            descriptorType = C.LIBUSB_DT_DEVICE
	descConfig            descriptorType = C.LIBUSB_DT_CONFIG
	descString            descriptorType = C.LIBUSB_DT_STRING
	descInterface         descriptorType = C.LIBUSB_DT_INTERFACE
	descEndpoint          descriptorType = C.LIBUSB_DT_ENDPOINT
	descBos               descriptorType = C.LIBUSB_DT_BOS
	descDeviceCapability  descriptorType = C.LIBUSB_DT_DEVICE_CAPABILITY
	descHid               descriptorType = C.LIBUSB_DT_HID
	descReport            descriptorType = C.LIBUSB_DT_REPORT
	descPhysical          descriptorType = C.LIBUSB_DT_PHYSICAL
	descHub               descriptorType = C.LIBUSB_DT_HUB
	descSuperspeedHub     descriptorType = C.LIBUSB_DT_SUPERSPEED_HUB
	descEndpointCompanion descriptorType = C.LIBUSB_DT_SS_ENDPOINT_COMPANION
)

var descriptorTypes = map[descriptorType]string{
	descDevice:            "Device descriptor.",
	descConfig:            "Configuration descriptor.",
	descString:            "String descriptor.",
	descInterface:         "Interface descriptor.",
	descEndpoint:          "Endpoint descriptor.",
	descBos:               "BOS descriptor.",
	descDeviceCapability:  "Device Capability descriptor.",
	descHid:               "HID descriptor.",
	descReport:            "HID report descriptor.",
	descPhysical:          "Physical descriptor.",
	descHub:               "Hub descriptor.",
	descSuperspeedHub:     "SuperSpeed Hub descriptor.",
	descEndpointCompanion: "SuperSpeed Endpoint Companion descriptor.",
}

func (descriptorType descriptorType) String() string {
	return descriptorTypes[descriptorType]
}

// EndpointDirection provides the type for an in or out endpoint.
type EndpointDirection byte

const (
	// Per USB 2.0 spec bit 7 of the endpoint address defines the direction,
	// where 0 = OUT and 1 = IN. The libusb C.LIBUSB_ENDPOINT_IN enumeration is
	// 128 instead of 1. Therefore, I'm not using C.LIBUSB_ENDPOINT_IN (128).
	endpointOut   EndpointDirection = C.LIBUSB_ENDPOINT_OUT
	endpointIn    EndpointDirection = 1
	directionMask endpointAddress   = 0x80
	directionBit                    = 7
)

var endpointDirections = map[EndpointDirection]string{
	endpointOut: "Out: host-to-device.",
	endpointIn:  "In: device-to-host.",
}

// String implements the Stringer interface for endpointDirection.
func (endpointDirection EndpointDirection) String() string {
	return endpointDirections[endpointDirection]
}

// TransferType provides which type of transfer.
type TransferType int

// Endpoint transfer type http://bit.ly/enum_libusb_transfer_type
const (
	ControlTransfer     TransferType = C.LIBUSB_TRANSFER_TYPE_CONTROL
	IsochronousTransfer TransferType = C.LIBUSB_TRANSFER_TYPE_ISOCHRONOUS
	BulkTransfer        TransferType = C.LIBUSB_TRANSFER_TYPE_BULK
	InterruptTransfer   TransferType = C.LIBUSB_TRANSFER_TYPE_INTERRUPT
)

var transferTypes = map[TransferType]string{
	ControlTransfer:     "Control endpoint.",
	IsochronousTransfer: "Isochronous endpoint.",
	BulkTransfer:        "Bulk endpoint.",
	InterruptTransfer:   "Interrupt endpoint.",
}

func (transferType TransferType) String() string {
	return transferTypes[transferType]
}

// TODO(mdr): May want to replace uint8 with a type specific for indexes.

type synchronizationType byte

// Synchronization type for isochronous endpoints. "Values for bits 2:3 of the
// bmAttributes field in libusb_endpoint_descriptor"
// http://bit.ly/enum_libusb_iso_sync_type
const (
	IsoSyncTypeNone     synchronizationType = C.LIBUSB_ISO_SYNC_TYPE_NONE
	IsoSyncTypeAsync    synchronizationType = C.LIBUSB_ISO_SYNC_TYPE_ASYNC
	IsoSyncTypeAdaptive synchronizationType = C.LIBUSB_ISO_SYNC_TYPE_ADAPTIVE
	IsoSynceTypeSync    synchronizationType = C.LIBUSB_ISO_SYNC_TYPE_SYNC
)

// Device represents a USB device including the opaque libusb_device struct.
type Device struct {
	libusbDevice        *C.libusb_device
	ActiveConfiguration *ConfigDescriptor
}

// Descriptor represents a USB device descriptor as a Go struct.
type Descriptor struct {
	Length              uint8
	DescriptorType      descriptorType
	USBSpecification    bcd
	DeviceClass         classCode
	DeviceSubClass      byte
	DeviceProtocol      byte
	MaxPacketSize0      uint8
	VendorID            uint16
	ProductID           uint16
	DeviceReleaseNumber bcd
	ManufacturerIndex   uint8
	ProductIndex        uint8
	SerialNumberIndex   uint8
	NumConfigurations   uint8
}

// HotPlugEventType ...
type HotPlugEventType uint8

// HotPlugCbFunc callback
type HotPlugCbFunc func(vID, pID uint16, eventType HotPlugEventType)

// HotPlug Event Types
const (
	HotplugUndefined HotPlugEventType = iota
	HotplugArrived
	HotplugLeft
)

// HotPlugEvent callback message
type HotPlugEvent struct {
	VendorID  uint16
	ProductID uint16
	Event     HotPlugEventType
}

type hotplugCallback struct {
	handler *C.libusb_hotplug_callback_handle
	fn      HotPlugCbFunc
}

// HotplugCallbackStorage ...
type HotplugCallbackStorage struct {
	callbackMap map[uint32]hotplugCallback
	done        chan struct{}
}

var hotplugCallbackStorage HotplugCallbackStorage

// BusNumber gets "the number of the bus that a device is connected to."
// (Source: libusb docs)
func (dev *Device) BusNumber() (int, error) {
	busNumber, err := C.libusb_get_bus_number(dev.libusbDevice)
	if err != nil {
		return 0, err
	}
	return int(busNumber), nil
}

// PortNumber gets "the number of the port that a device is connected to.
// Unless the OS does something funky, or you are hot-plugging USB extension
// cards, the port number returned by this call is usually guaranteed to be
// uniquely tied to a physical port, meaning that different devices plugged on
// the same physical port should return the same port number.  But outside of
// this, there is no guarantee that the port number returned by this call will
// remain the same, or even match the order in which ports have been numbered
// by the HUB/HCD manufacturer." (Source: libusb docs)
func (dev *Device) PortNumber() (int, error) {
	portNumber, err := C.libusb_get_port_number(dev.libusbDevice)
	if err != nil {
		return 0, fmt.Errorf("port number is unavailable for device %v", dev)
	}
	return int(portNumber), nil
}

// MaxPacketSize is a "convenience function to retrieve the wMaxPacketSize
// value for a particular endpoint in the active device configuration. This
// function was originally intended to be of assistance when setting up
// isochronous transfers, but a design mistake resulted in this function
// instead. It simply returns the wMaxPacketSize value without considering its
// contents. If you're dealing with isochronous transfers, you probably want
// libusb_get_max_iso_packet_size() instead." (Source: libusb docs)
func (dev *Device) MaxPacketSize(ep endpointAddress) (int, error) {
	maxPacketSize, err := C.libusb_get_max_packet_size(dev.libusbDevice, C.uchar(ep))
	if err != nil {
		return 0, fmt.Errorf("wMaxPacketSize is unavailable for device %v", dev)
	}
	return int(maxPacketSize), nil
}

// DeviceAddress gets "the address of the device on the bus it is connected
// to." (Source: libusb docs)
func (dev *Device) DeviceAddress() (int, error) {
	deviceAddress, err := C.libusb_get_device_address(dev.libusbDevice)
	if err != nil {
		return 0, err
	}
	return int(deviceAddress), nil
}

// Speed gets "the negotiated connection speed for a device." (Source:
// libusb docs)
func (dev *Device) Speed() (SpeedType, error) {
	deviceSpeed, err := C.libusb_get_device_speed(dev.libusbDevice)
	if err != nil {
		return 0, err
	}
	return SpeedType(deviceSpeed), nil
}

// Open will "open a device and obtain a device handle. A handle allows you to
// perform I/O on the device in question. Internally, this function adds a
// reference to the device and makes it available to you through
// libusb_get_device(). This reference is removed during libusb_close()." This
// is a non-blocking function; no requests are sent over the bus. (Source:
// libusb docs)
func (dev *Device) Open() (*DeviceHandle, error) {
	var handle *C.libusb_device_handle
	err := C.libusb_open(dev.libusbDevice, &handle)
	if err != 0 {
		return nil, ErrorCode(err)
	}
	deviceHandle := &DeviceHandle{
		libusbDeviceHandle: handle,
	}
	return deviceHandle, nil
}

// DeviceDescriptor implements the libusb_get_device_descriptor function to
// update the DeviceDescriptor struct embedded in the Device.  DeviceDescriptor
// gets "the USB device descriptor for a given device. This is a non-blocking
// function; the device descriptor is cached in memory. Note since
// libusb-1.0.16, LIBUSB_API_VERSION >= 0x01000102, this function always
// succeeds." (Source: libusb docs)
func (dev *Device) DeviceDescriptor() (*Descriptor, error) {
	var desc C.struct_libusb_device_descriptor
	err := C.libusb_get_device_descriptor(dev.libusbDevice, &desc)
	if err != 0 {
		return nil, ErrorCode(err)
	}
	deviceDescriptor := Descriptor{
		Length:              uint8(desc.bLength),
		DescriptorType:      descriptorType(desc.bDescriptorType),
		USBSpecification:    bcd(desc.bcdUSB),
		DeviceClass:         classCode(desc.bDeviceClass),
		DeviceSubClass:      byte(desc.bDeviceSubClass),
		DeviceProtocol:      byte(desc.bDeviceProtocol),
		MaxPacketSize0:      uint8(desc.bMaxPacketSize0),
		VendorID:            uint16(desc.idVendor),
		ProductID:           uint16(desc.idProduct),
		DeviceReleaseNumber: bcd(desc.bcdDevice),
		ManufacturerIndex:   uint8(desc.iManufacturer),
		ProductIndex:        uint8(desc.iProduct),
		SerialNumberIndex:   uint8(desc.iSerialNumber),
		NumConfigurations:   uint8(desc.bNumConfigurations),
	}
	return &deviceDescriptor, nil
}

// ActiveConfigDescriptor "gets the USB configuration descriptor for the
// currently active configuration. This is a non-blocking function which does
// not involve any requests being sent to the device." (Source: libusb docs)
func (dev *Device) ActiveConfigDescriptor() (*ConfigDescriptor, error) {
	var config *C.struct_libusb_config_descriptor
	err := C.libusb_get_active_config_descriptor(dev.libusbDevice, &config)
	defer C.libusb_free_config_descriptor(config)
	if err != 0 {
		return nil, ErrorCode(err)
	}
	activeConfiguration := &ConfigDescriptor{
		Length:               int(config.bLength),
		DescriptorType:       descriptorType(config.bDescriptorType),
		TotalLength:          uint16(config.wTotalLength),
		NumInterfaces:        int(config.bNumInterfaces),
		ConfigurationValue:   uint8(config.bConfigurationValue),
		ConfigurationIndex:   uint8(config.iConfiguration),
		Attributes:           uint8(config.bmAttributes),
		MaxPowerMilliAmperes: 2 * uint(config.MaxPower), // Convert from 2 mA to just mA
		SupportedInterfaces:  nil,
	}
	var cInterface *C.struct_libusb_interface = config._interface
	length := activeConfiguration.NumInterfaces
	libusbInterfaces := unsafe.Slice(cInterface, length)
	// hdr := reflect.SliceHeader{
	// 	Data: uintptr(unsafe.Pointer(cInterface)),
	// 	Len:  length,
	// 	Cap:  length,
	// }
	// libusbInterfaces := *(*[]C.struct_libusb_interface)(unsafe.Pointer(&hdr))

	var supportedInterfaces SupportedInterfaces
	// Loop through the array of interfaces support by this configuration
	// const struct libusb_interface * interface
	for _, libusbInterface := range libusbInterfaces {
		supportedInterface := SupportedInterface{
			NumAltSettings:       int(libusbInterface.num_altsetting),
			InterfaceDescriptors: nil,
		}
		var interfaceDescriptors InterfaceDescriptors
		var cInterfaceDescriptor *C.struct_libusb_interface_descriptor = libusbInterface.altsetting
		length := int(libusbInterface.num_altsetting)
		libusbInterfaceDescriptors := unsafe.Slice(cInterfaceDescriptor, length)
		// hdr := reflect.SliceHeader{
		// 	Data: uintptr(unsafe.Pointer(cInterfaceDescriptor)),
		// 	Len:  length,
		// 	Cap:  length,
		// }
		// libusbInterfaceDescriptors := *(*[]C.struct_libusb_interface_descriptor)(unsafe.Pointer(&hdr))

		// Loop through the array of interface descriptors
		// const struct libusb_interface_descriptor * altsetting
		for _, libusbInterfaceDescriptor := range libusbInterfaceDescriptors {
			interfaceDescriptor := InterfaceDescriptor{
				Length:              int(libusbInterfaceDescriptor.bLength),
				DescriptorType:      descriptorType(libusbInterfaceDescriptor.bDescriptorType),
				InterfaceNumber:     int(libusbInterfaceDescriptor.bInterfaceNumber),
				AlternateSetting:    int(libusbInterfaceDescriptor.bAlternateSetting),
				NumEndpoints:        int(libusbInterfaceDescriptor.bNumEndpoints),
				InterfaceClass:      uint8(libusbInterfaceDescriptor.bInterfaceClass),
				InterfaceSubClass:   uint8(libusbInterfaceDescriptor.bInterfaceSubClass),
				InterfaceProtocol:   uint8(libusbInterfaceDescriptor.bInterfaceProtocol),
				InterfaceIndex:      int(libusbInterfaceDescriptor.iInterface),
				EndpointDescriptors: nil,
			}
			var endpointDescriptors []*EndpointDescriptor
			var cEndpointDescriptor *C.struct_libusb_endpoint_descriptor = libusbInterfaceDescriptor.endpoint
			length := int(libusbInterfaceDescriptor.bNumEndpoints)
			libusbEndpointDescriptors := unsafe.Slice(cEndpointDescriptor, length)
			// hdr := reflect.SliceHeader{
			// 	Data: uintptr(unsafe.Pointer(cEndpointDescriptor)),
			// 	Len:  length,
			// 	Cap:  length,
			// }

			// libusbEndpointDescriptors := *(*[]C.struct_libusb_endpoint_descriptor)(unsafe.Pointer(&hdr))

			// Loop through the array of endpoint descriptors
			// const struct libusb_endpoint_descriptor * endpoint
			for _, libusbEndpointDescriptor := range libusbEndpointDescriptors {
				endpointDescriptor := EndpointDescriptor{
					Length:          int(libusbEndpointDescriptor.bLength),
					DescriptorType:  descriptorType(libusbEndpointDescriptor.bDescriptorType),
					EndpointAddress: endpointAddress(libusbEndpointDescriptor.bEndpointAddress),
					Attributes:      endpointAttributes(libusbEndpointDescriptor.bmAttributes),
					MaxPacketSize:   uint16(libusbEndpointDescriptor.wMaxPacketSize),
					Interval:        uint8(libusbEndpointDescriptor.bInterval),
				}
				endpointDescriptors = append(endpointDescriptors, &endpointDescriptor)
			}
			interfaceDescriptor.EndpointDescriptors = endpointDescriptors
			interfaceDescriptors = append(interfaceDescriptors, &interfaceDescriptor)
		}
		supportedInterface.InterfaceDescriptors = interfaceDescriptors
		supportedInterfaces = append(supportedInterfaces, &supportedInterface)
	}
	activeConfiguration.SupportedInterfaces = supportedInterfaces
	return activeConfiguration, nil
}

// ConfigDescriptor "gets a USB configuration descriptor based on its index.
// This is a non-blocking function which does not involve any requests being
// sent to the device." (Source: libusb docs)
func (dev *Device) ConfigDescriptor(configIndex int) (*ConfigDescriptor, error) {
	var cConfig *C.struct_libusb_config_descriptor
	err := C.libusb_get_config_descriptor(dev.libusbDevice, C.uint8_t(configIndex), &cConfig)
	defer C.libusb_free_config_descriptor(cConfig)
	if err != 0 {
		return nil, ErrorCode(err)
	}
	configuration := &ConfigDescriptor{
		Length:               int(cConfig.bLength),
		DescriptorType:       descriptorType(cConfig.bDescriptorType),
		TotalLength:          uint16(cConfig.wTotalLength),
		NumInterfaces:        int(cConfig.bNumInterfaces),
		ConfigurationValue:   uint8(cConfig.bConfigurationValue),
		ConfigurationIndex:   uint8(cConfig.iConfiguration),
		Attributes:           uint8(cConfig.bmAttributes),
		MaxPowerMilliAmperes: 2 * uint(cConfig.MaxPower), // Convert from 2 mA to just mA
		SupportedInterfaces:  nil,
	}
	return configuration, nil
}

// ConfigDescriptorByValue gets "a USB configuration descriptor with a
// specific bConfigurationValue. This is a non-blocking function which does not
// involve any requests being sent to the device. (Source: libusb docs)
func (dev *Device) ConfigDescriptorByValue(configValue int) (*ConfigDescriptor, error) {
	var cConfig *C.struct_libusb_config_descriptor
	err := C.libusb_get_config_descriptor_by_value(
		dev.libusbDevice, C.uint8_t(configValue), &cConfig,
	)
	defer C.libusb_free_config_descriptor(cConfig)
	if err != 0 {
		return nil, ErrorCode(err)
	}
	configuration := &ConfigDescriptor{
		Length:               int(cConfig.bLength),
		DescriptorType:       descriptorType(cConfig.bDescriptorType),
		TotalLength:          uint16(cConfig.wTotalLength),
		NumInterfaces:        int(cConfig.bNumInterfaces),
		ConfigurationValue:   uint8(cConfig.bConfigurationValue),
		ConfigurationIndex:   uint8(cConfig.iConfiguration),
		Attributes:           uint8(cConfig.bmAttributes),
		MaxPowerMilliAmperes: 2 * uint(cConfig.MaxPower), // Convert from 2 mA to just mA
		SupportedInterfaces:  nil,
	}
	return configuration, nil
}

// DeviceHandle represents the libusb device handle.
type DeviceHandle struct {
	libusbDeviceHandle *C.libusb_device_handle
}

// StringDescriptor retrieves a descriptor from a device.
func (dh *DeviceHandle) StringDescriptor(
	descIndex uint8,
	langID uint16,
) (string, error) {
	var cData *C.uchar
	length := 512
	usberr := C.libusb_get_string_descriptor(
		dh.libusbDeviceHandle,
		C.uint8_t(descIndex),
		C.uint16_t(langID),
		cData,
		C.int(length),
	)
	if usberr < 0 {
		return "", ErrorCode(usberr)
	}
	data := (*C.char)(unsafe.Pointer(cData))
	return C.GoString(data), nil
}

// StringDescriptorASCII retrieve(s) a string descriptor in C style ASCII.
// Wrapper around libusb_get_string_descriptor(). Uses the first language
// supported by the device. (Source: libusb docs)
func (dh *DeviceHandle) StringDescriptorASCII(
	descIndex uint8,
) (string, error) {
	// TODO(mdr): Should the length be a constant? Why did I pick 256 bytes?
	length := 256
	data := make([]byte, length)
	bytesRead, _ := C.libusb_get_string_descriptor_ascii(
		dh.libusbDeviceHandle,
		C.uint8_t(descIndex),
		// Unsafe pointer -> https://stackoverflow.com/a/16376039/95592
		(*C.uchar)(unsafe.Pointer(&data[0])),
		C.int(length),
	)
	if bytesRead < 0 {
		return "", ErrorCode(bytesRead)
	}
	return string(data[0:bytesRead]), nil
}

// Close implements libusb_close to close the device handle.
func (dh *DeviceHandle) Close() error {
	// Recover from any libusb panic just in case
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Recovered from libusb_close panic: %v", r)
		}
	}()
	C.libusb_close(dh.libusbDeviceHandle)
	return nil
}

// Device implements libusb_get_device to get the underlying device for a
// handle.
// TODO(mdr): Determine if I actually need this function.
// func (dh *DeviceHandle) Device() (*Device, error) {
// }

// Configuration implements the libusb_get_configuration function to
// determine the bConfigurationValue of the currently active configuration.
func (dh *DeviceHandle) Configuration() (int, error) {
	var configuration *C.int
	err := C.libusb_get_configuration(dh.libusbDeviceHandle, configuration)
	if err != 0 {
		return 0, ErrorCode(err)
	}
	return int(*configuration), nil
}

// SetConfiguration implements libusb_set_configuration to set the active
// configuration for the device.
func (dh *DeviceHandle) SetConfiguration(configuration int) error {
	err := C.libusb_set_configuration(dh.libusbDeviceHandle,
		C.int(configuration))
	if err != 0 {
		return ErrorCode(err)
	}
	return nil
}

// ClaimInterface implements libusb_claim_interface to claim an interface on a
// given device handle. You must claim the interface you wish to use before you
// can perform I/O on any of its endpoints.
func (dh *DeviceHandle) ClaimInterface(interfaceNum int) error {
	err := C.libusb_claim_interface(dh.libusbDeviceHandle, C.int(interfaceNum))
	if err != 0 {
		return ErrorCode(err)
	}
	return nil
}

// ReleaseInterface implements libusb_release_interface to release an interface
// previously claimed with libusb_claim_interface() (i.e., ClaimInterface()).
func (dh *DeviceHandle) ReleaseInterface(interfaceNum int) error {
	err := C.libusb_release_interface(dh.libusbDeviceHandle, C.int(interfaceNum))
	if err != 0 {
		return ErrorCode(err)
	}
	return nil
}

// SetInterfaceAltSetting activates an alternate setting for an interface.
func (dh *DeviceHandle) SetInterfaceAltSetting(
	interfaceNum int,
	alternateSetting int,
) error {
	err := C.libusb_set_interface_alt_setting(
		dh.libusbDeviceHandle,
		C.int(interfaceNum),
		C.int(alternateSetting),
	)
	if err != 0 {
		return ErrorCode(err)
	}
	return nil
}

// ClearHalt implements libusb_clear_halt to clear a halt/stall condition on an endpoint.
func (dh *DeviceHandle) ClearHalt(endpoint endpointAddress) error {
	err := C.libusb_clear_halt(dh.libusbDeviceHandle, C.uchar(endpoint))
	if err != 0 {
		return ErrorCode(err)
	}
	return nil
}

// ResetDevice implements libusb_reset_device to perform a USB port reset to
// reinitialize a device.
func (dh *DeviceHandle) ResetDevice() error {
	err := C.libusb_reset_device(dh.libusbDeviceHandle)
	if err != 0 {
		return ErrorCode(err)
	}
	return nil
}

// KernelDriverActive implements libusb_kernel_driver_active to determine if a
// kernel driver is active on an interface.
func (dh *DeviceHandle) KernelDriverActive(interfaceNum int) (bool, error) {
	ret := C.libusb_kernel_driver_active(
		dh.libusbDeviceHandle, C.int(interfaceNum))
	if ret == 1 {
		return true, nil
	} else if ret != 0 {
		return false, ErrorCode(ret)
	}
	return false, nil
}

// DetachKernelDriver implements libusb_detach_kernel_driver to detach a kernel
// driver from an interface.
func (dh *DeviceHandle) DetachKernelDriver(interfaceNum int) error {
	err := C.libusb_detach_kernel_driver(
		dh.libusbDeviceHandle, C.int(interfaceNum))
	if err != 0 {
		return ErrorCode(err)
	}
	return nil
}

// AttachKernelDriver implements libusb_attach_kernel_driver to re-attach an
// interface's kernel driver, which was previously detached using
// libusb_detach_kernel_driver().
func (dh *DeviceHandle) AttachKernelDriver(interfaceNum int) error {
	err := C.libusb_attach_kernel_driver(
		dh.libusbDeviceHandle, C.int(interfaceNum))
	if err != 0 {
		return ErrorCode(err)
	}
	return nil
}

// SetAutoDetachKernelDriver implements libusb_set_auto_detach_kernel_driver to
// enable/disable libusb's automatic kernel driver detachment.
func (dh *DeviceHandle) SetAutoDetachKernelDriver(enable bool) error {
	cEnable := C.int(0)
	if enable {
		cEnable = C.int(1)
	}
	err := C.libusb_set_auto_detach_kernel_driver(dh.libusbDeviceHandle, cEnable)
	if err != 0 {
		return ErrorCode(err)
	}
	return nil
}

// SpeedType provides the USB speed type.
type SpeedType int

const (
	speedUnknown SpeedType = C.LIBUSB_SPEED_UNKNOWN
	speedLow     SpeedType = C.LIBUSB_SPEED_LOW
	speedFull    SpeedType = C.LIBUSB_SPEED_FULL
	speedHigh    SpeedType = C.LIBUSB_SPEED_HIGH
	speedSuper   SpeedType = C.LIBUSB_SPEED_SUPER
)

var speedCodes = map[SpeedType]string{
	speedUnknown: "The OS doesn't report or know the device speed.",
	speedLow:     "The device is operating at low speed (1.5MBit/s)",
	speedFull:    "The device is operating at full speed (12MBit/s)",
	speedHigh:    "The device is operating at high speed (480MBit/s)",
	speedSuper:   "The device is operating at super speed (5000MBit/s)",
}

func (speed SpeedType) String() string {
	return speedCodes[speed]
}

type supportedSpeed int

const (
	lowSpeedOperation   supportedSpeed = C.LIBUSB_LOW_SPEED_OPERATION
	fullSpeedOperation  supportedSpeed = C.LIBUSB_FULL_SPEED_OPERATION
	highSpeedOperation  supportedSpeed = C.LIBUSB_HIGH_SPEED_OPERATION
	superSpeedOperation supportedSpeed = C.LIBUSB_SUPER_SPEED_OPERATION
)

var supportedSpeeds = map[supportedSpeed]string{
	lowSpeedOperation:   "Low speed operation supported (1.5MBit/s).",
	fullSpeedOperation:  "Full speed operation supported (12MBit/s).",
	highSpeedOperation:  "High speed operation supported (480MBit/s).",
	superSpeedOperation: "Superspeed operation supported (5000MBit/s).",
}

func (speed supportedSpeed) String() string {
	return supportedSpeeds[speed]
}

// BulkTransfer implements libusb_bulk_transfer to perform a USB bulk transfer.
func (dh *DeviceHandle) BulkTransfer(
	endpoint endpointAddress,
	data []byte,
	length int,
	timeout int,
) (int, error) {
	var transferred C.int
	err := C.libusb_bulk_transfer(
		dh.libusbDeviceHandle,
		C.uchar(endpoint),
		(*C.uchar)(unsafe.Pointer(&data[0])),
		C.int(length),
		&transferred,
		C.uint(timeout),
	)
	if err != 0 {
		return 0, ErrorCode(err)
	}
	return int(transferred), nil
}

// BulkTransferOut is a helper method that performs a USB bulk output transfer.
func (dh *DeviceHandle) BulkTransferOut(
	endpoint endpointAddress,
	data []byte,
	timeout int,
) (int, error) {
	return dh.BulkTransfer(
		endpoint,
		data,
		len(data),
		timeout,
	)
}

// BulkTransferIn is a helper method that performs a USB bulk input transfer.
func (dh *DeviceHandle) BulkTransferIn(
	endpoint endpointAddress,
	maxReceiveBytes int,
	timeout int,
) ([]byte, int, error) {
	data := make([]byte, maxReceiveBytes)
	transferred, err := dh.BulkTransfer(
		endpoint,
		data,
		maxReceiveBytes,
		timeout,
	)
	if err != nil {
		return nil, 0, err
	}
	return data, int(transferred), nil
}

// ControlTransfer sends a transfer using a control endpoint for the given
// device handle.
func (dh *DeviceHandle) ControlTransfer(
	requestType byte,
	request byte,
	value uint16,
	index uint16,
	data []byte,
	length int,
	timeout int,
) (int, error) {
	ret := C.libusb_control_transfer(
		dh.libusbDeviceHandle,
		C.uint8_t(requestType),
		C.uint8_t(request),
		C.uint16_t(value),
		C.uint16_t(index),
		(*C.uchar)(unsafe.Pointer(&data[0])),
		C.uint16_t(length),
		C.uint(timeout),
	)
	if ret < 0 {
		return 0, ErrorCode(ret)
	}
	return int(ret), nil
}

// InterruptTransfer performs a USB interrupt transfer.
func (dh *DeviceHandle) InterruptTransfer(
	endpoint endpointAddress,
	data []byte,
	length int,
	timeout int,
) (int, error) {
	var transferred C.int
	err := C.libusb_interrupt_transfer(
		dh.libusbDeviceHandle,
		C.uchar(endpoint),
		(*C.uchar)(unsafe.Pointer(&data[0])),
		C.int(length),
		&transferred,
		C.uint(timeout),
	)
	if err != 0 {
		return 0, ErrorCode(err)
	}
	return int(transferred), nil
}

func vidPidToUint32(vID, pID uint16) uint32 {
	return (uint32(vID) << 16) | (uint32(pID))
}
