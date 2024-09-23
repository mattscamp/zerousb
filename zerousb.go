package zerousb

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// ErrDeviceClosed is returned for operations where the device closed before or
// during the execution.
var ErrDeviceClosed = errors.New("usb: device closed")

// ErrDeviceDisconnected is returned for operations where the device disconnected
var ErrDeviceDisconnected = errors.New("usb: device disconnected")

// ErrUnsupportedPlatform is returned for all operations where the underlying
// operating system is not supported by the library.
var ErrUnsupportedPlatform = errors.New("usb: unsupported platform")

// ID represents a vendor or product ID.
type ID uint16

// String returns a hexadecimal ID.
func (id ID) String() string {
	return fmt.Sprintf("%04x", int(id))
}

const (
	usbConfigIndex = 0
)

type VendorAndProduct struct {
	VendorId   uint16
	ProductId  uint16
	Identifier *string
}

type DeviceWithId struct {
	Identifier *string
	Device     *Device
}

type Options struct {
	LogLevel LogLevel
}

type ZeroUSB struct {
	usbContext             *Context
	canDetach              bool
	options                Options
	logger                 *logrus.Logger
	vendorID               ID
	productID              ID
	currentConnectedDevice *ZeroUSBDevice
	endWatcher             chan bool
}

type ZeroUSBDevice struct {
	Identifier         *string
	dev                *Device
	options            Options
	logger             *logrus.Logger
	closed             int32 // atomic
	lock               sync.Mutex
	attach             bool
	handle             *DeviceHandle
	reader             *endpointAddress
	readerTransferType TransferType
	writerTransferType TransferType
	writer             *endpointAddress
	ifaceNum           int
}

func New(options Options, logger *logrus.Logger) (*ZeroUSB, error) {
	usbCtx, err := NewContext()
	if err != nil {
		return nil, err
	}

	usbCtx.SetDebug(options.LogLevel)

	return &ZeroUSB{
		usbContext: usbCtx,
		options:    options,
		canDetach:  runtime.GOOS != "windows",
		logger:     logger,
	}, nil
}

func (b *ZeroUSB) Close() {
	if b.usbContext != nil {
		b.currentConnectedDevice.Close(true)
		b.usbContext.Close()
		b.usbContext = nil
	}
}

func (b *ZeroUSB) Log(msg string) {
	if b.logger != nil {
		b.logger.Info(fmt.Sprintf("[zerousb] %s \n", msg))
	}
}

func (b *ZeroUSB) Error(msg string) {
	if b.logger != nil {
		b.logger.Error(fmt.Sprintf("[zerousb] %s \n", msg))
	}
}

func (b *ZeroUSB) Warn(msg string) {
	if b.logger != nil {
		b.logger.Warn(fmt.Sprintf("[zerousb] %s \n", msg))
	}
}

func (b *ZeroUSBDevice) Log(msg string) {
	if b.logger != nil {
		b.logger.Info(fmt.Sprintf("[zerousb] %s \n", msg))
	}
}

func (b *ZeroUSBDevice) Error(msg string) {
	if b.logger != nil {
		b.logger.Error(fmt.Sprintf("[zerousb] %s \n", msg))
	}
}

func (b *ZeroUSBDevice) Warn(msg string) {
	if b.logger != nil {
		b.logger.Warn(fmt.Sprintf("[zerousb] %s \n", msg))
	}
}

func indexOfVendorIDAndProductID(ids []VendorAndProduct, lookup []uint16) *int {
	for i, vendorAndProductID := range ids {
		if lookup[0] == vendorAndProductID.VendorId && lookup[1] == vendorAndProductID.ProductId {
			return &i
		}
	}
	return nil
}

func (b *ZeroUSB) Get() (*ZeroUSBDevice, error) {
	if b.usbContext == nil {
		return nil, errors.New("No context. Initialize ZeroUSB.")
	}

	if b.currentConnectedDevice == nil {
		return nil, errors.New("No device.")
	}

	return b.currentConnectedDevice, nil
}

func (b *ZeroUSB) EndWatch() {
	b.endWatcher <- true
}

func (b *ZeroUSB) Watch(vendorAndProductIDs []VendorAndProduct) error {
	if b.usbContext == nil {
		return errors.New("No context. Initialize ZeroUSB.")
	}

	ticker := time.NewTicker(1 * time.Second)
	b.endWatcher = make(chan bool)
	go func() {
		for {
			select {
			case <-b.endWatcher:
				ticker.Stop()
				return
			case <-ticker.C:
				if b.usbContext == nil {
					b.endWatcher <- true
					continue
				}

				connectedDevices, err := b.usbContext.DeviceList()
				if err != nil {
					b.Error(fmt.Sprintf("[zerousb] Getting devices: %+v \n", err))
					b.endWatcher <- true
				}

				watchedAndConnectedDevices := []DeviceWithId{}
				// Get all watched devices
				for _, device := range connectedDevices {
					indexOfWatchedDevice := indexOfVendorIDAndProductID(vendorAndProductIDs, []uint16{
						uint16(device.libusbDevice.device_descriptor.idVendor),
						uint16(device.libusbDevice.device_descriptor.idProduct),
					})
					if indexOfWatchedDevice != nil {
						watchedAndConnectedDevices = append(watchedAndConnectedDevices, DeviceWithId{Identifier: vendorAndProductIDs[*indexOfWatchedDevice].Identifier, Device: device})
					}
				}

				// Check if we need to remove a reference to the current device (unplugged)
				shouldDisconnect := true
				if b.currentConnectedDevice != nil {
					for _, device := range watchedAndConnectedDevices {
						if b.currentConnectedDevice.handle.libusbDeviceHandle.dev.device_descriptor.idVendor == device.Device.libusbDevice.device_descriptor.idVendor &&
							b.currentConnectedDevice.handle.libusbDeviceHandle.dev.device_descriptor.idProduct == device.Device.libusbDevice.device_descriptor.idProduct {
							// Our current reference is both connected and a watched device
							shouldDisconnect = false
							break
						}
					}
				} else {
					shouldDisconnect = false
				}

				// Our device seems disconnected. Clean up shop.
				if shouldDisconnect {
					b.Log(fmt.Sprintf("[zerousb] Detected UNPLUG event for device: %+v \n", b.currentConnectedDevice.Identifier))
					b.currentConnectedDevice.Close(true)
					b.currentConnectedDevice = nil
				}

				// Our current referenced device is still connected, get out of here
				if b.currentConnectedDevice != nil {
					continue
				}

				// Found no devices
				if len(watchedAndConnectedDevices) == 0 {
					continue
				}

				b.Log(fmt.Sprintf("[zerousb] Detected PLUG event for device: %+v \n", watchedAndConnectedDevices[0].Identifier))
				_, err = b.Connect(watchedAndConnectedDevices[0].Identifier, uint16(watchedAndConnectedDevices[0].Device.libusbDevice.device_descriptor.idVendor), uint16(watchedAndConnectedDevices[0].Device.libusbDevice.device_descriptor.idProduct))
				if err != nil {
					b.Error(fmt.Sprintf("[zerousb] Unable to connect to device: %+v \n", err))
					continue
				}
			}
		}
	}()

	return nil
}

func (b *ZeroUSB) Connect(name *string, vendorID, productID uint16) (*ZeroUSBDevice, error) {
	if b.usbContext == nil {
		return nil, errors.New("No context. Initialize ZeroUSB.")
	}

	var device *ZeroUSBDevice

	b.Log(fmt.Sprintf("[zerousb] Attempting to open device: %s \n", name))

	// attempt to find the device on the OS
	usbDevice, usbDeviceHandle, err := b.usbContext.OpenDeviceWithVendorProduct(vendorID, productID)
	if err != nil {
		b.Error(fmt.Sprintf("[zerousb] Failed to find device %s (%v) \n", name, err))
		return nil, errors.New("Unable to find device.")
	}

	activeCfg, err := usbDevice.ActiveConfigDescriptor()
	if err != nil {
		b.Error(fmt.Sprintf("[zerousb] Failed get active config for %s (%v) \n", name, err))
		return nil, errors.New("Unable to get active config")
	}

	// we found a device, now let's figure out if it is supported
	ifaces := activeCfg.SupportedInterfaces
	for _, iface := range ifaces {
		if iface.NumAltSettings == 0 {
			continue
		}

		for _, alt := range iface.InterfaceDescriptors {
			// Skip HID interfaces, they are handled directly by OS libraries
			if alt.InterfaceClass == uint8(hid) {
				continue
			}

			var reader, writer *endpointAddress
			var readerTransferType, writerTransferType TransferType

			for _, end := range alt.EndpointDescriptors {
				// Skip any non-bulk endpoints
				if end.Attributes.transferType() != BulkTransfer {
					continue
				}

				if end.Direction() == endpointIn {
					reader = &end.EndpointAddress
					readerTransferType = end.TransferType()
				} else if end.Direction() == endpointOut {
					writer = &end.EndpointAddress
					writerTransferType = end.TransferType()
				}
			}

			// If both in and out interrupts are available, match the device
			if reader != nil && writer != nil {
				usbDeviceDescriptor, _ := usbDevice.DeviceDescriptor()
				if err != nil {
					b.Error(fmt.Sprintf("[zerousb] Failed opening %s (%v \n", name, err))
					return nil, errors.New("Failed to open device.")
				}

				serialnum, _ := usbDeviceHandle.StringDescriptorASCII(usbDeviceDescriptor.SerialNumberIndex)
				manufacturer, _ := usbDeviceHandle.StringDescriptorASCII(usbDeviceDescriptor.ManufacturerIndex)
				product, _ := usbDeviceHandle.StringDescriptorASCII(usbDeviceDescriptor.ProductIndex)
				b.Log(fmt.Sprintf("[zerousb] Found %v %v S/N %s using Vendor ID %v and Product ID %v\n",
					manufacturer,
					product,
					serialnum,
					vendorID,
					productID,
				))
				device = &ZeroUSBDevice{
					Identifier:         name,
					dev:                usbDevice,
					options:            b.options,
					logger:             b.logger,
					handle:             usbDeviceHandle,
					reader:             reader,
					writer:             writer,
					readerTransferType: readerTransferType,
					writerTransferType: writerTransferType,
					ifaceNum:           alt.InterfaceNumber,
				}
			}
		}
	}

	if device == nil {
		// we could not find a device
		b.Error(fmt.Sprintf("[zerousb] Failed to find device %s \n", name))
		return nil, errors.New("Failed to find device.")
	}

	if b.canDetach {
		err := usbDeviceHandle.DetachKernelDriver(device.ifaceNum)
		if err != nil {
			b.Warn(fmt.Sprintf("detach of kernal driver failed: %s", err.Error()))
			// Fail softly. This is a newer MacOS feature any may not work everywhere.
		}
	}

	err = usbDeviceHandle.ClaimInterface(device.ifaceNum)
	if err != nil {
		b.Error(fmt.Sprintf("[zerousb] Failed to claim interface number %d: %v \n", device.ifaceNum, err))
		return nil, errors.New("Error claiming interface.")
	}

	b.currentConnectedDevice = device

	return device, nil
}

func (d *ZeroUSBDevice) Close(disconnected bool) error {
	if !disconnected {
		d.ClearBuffer()
	}

	err := d.handle.ReleaseInterface(d.ifaceNum)
	if err != nil {
		d.Error(fmt.Sprintf("error at releasing interface: %s", err))
	}

	d.handle.Close()

	return nil
}

func (d *ZeroUSBDevice) ClearBuffer() {
	var err error

	for err == nil {
		_, err = d.Read(64, 50)
	}
}

func IsErrorDisconnect(err error) bool {
	return (strings.Contains(err.Error(), ErrorName(errorIo)) ||
		strings.Contains(err.Error(), ErrorName(errorNoDevice)) ||
		strings.Contains(err.Error(), ErrorName(errorOther)) ||
		strings.Contains(err.Error(), ErrorName(errorPipe)))
}

func (d *ZeroUSBDevice) Write(buf []byte) (int, error) {
	if d.writer == nil {
		return 0, fmt.Errorf("Attempt to write before opening connection.")
	}

	if d.options.LogLevel == LogLevelDebug {
		d.Log(fmt.Sprintf("DEBUG. Write. %+v \n", buf))
	}

	return d.handle.BulkTransferOut(*d.writer, buf, 500)
}

func (d *ZeroUSBDevice) Read(length int, timeout int) ([]byte, error) {
	if d.writer == nil {
		return []byte{}, fmt.Errorf("Attempt to read before opening connection.")
	}

	if d.options.LogLevel == LogLevelDebug {
		d.Log(fmt.Sprintf("DEBUG. Read. %+v \n", length))
	}

	// default read timeout
	if timeout == 0 {
		timeout = 5000
	}

	readRes, _, err := d.handle.BulkTransferIn(*d.reader, length, timeout)
	if err != nil {
		return nil, err
	}

	return readRes, nil
}
