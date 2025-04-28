package zerousb

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
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
		if b.currentConnectedDevice != nil {
			b.currentConnectedDevice.Close(true)
			b.currentConnectedDevice = nil
		}
		b.usbContext.Close()
		b.usbContext = nil
	}
}

func (b *ZeroUSB) logf(level logrus.Level, format string, args ...interface{}) {
	if b.logger != nil {
		b.logger.Logf(level, "(zerousb) "+format, args...)
	}
}

func (b *ZeroUSBDevice) logf(level logrus.Level, format string, args ...interface{}) {
	if b.logger != nil {
		b.logger.Logf(level, "(zerousb) "+format, args...)
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
	if b.endWatcher != nil {
		b.endWatcher <- true
	}
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
					b.logf(logrus.ErrorLevel, "Getting devices: %+v", err)
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
					b.logf(logrus.InfoLevel, "Detected UNPLUG event for device: %+v", b.currentConnectedDevice.Identifier)
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

				b.logf(logrus.InfoLevel, "Detected PLUG event for device: %+v", watchedAndConnectedDevices[0].Identifier)
				_, err = b.Connect(watchedAndConnectedDevices[0].Identifier, uint16(watchedAndConnectedDevices[0].Device.libusbDevice.device_descriptor.idVendor), uint16(watchedAndConnectedDevices[0].Device.libusbDevice.device_descriptor.idProduct))
				if err != nil {
					b.logf(logrus.ErrorLevel, "Unable to connect to device: %+v", err)
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

	b.logf(logrus.InfoLevel, "Attempting to open device: %s ", name)

	// attempt to find the device on the OS
	usbDevice, usbDeviceHandle, err := b.usbContext.OpenDeviceWithVendorProduct(vendorID, productID)
	if err != nil {
		b.logf(logrus.ErrorLevel, "Failed to find device %s (%v)", name, err)
		return nil, errors.New("Unable to find device.")
	}

	activeCfg, err := usbDevice.ActiveConfigDescriptor()
	if err != nil {
		b.logf(logrus.ErrorLevel, "Failed get active config for %s (%v)", name, err)
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
				usbDeviceDescriptor, err := usbDevice.DeviceDescriptor()
				if err != nil {
					b.logf(logrus.ErrorLevel, "Failed to get device descriptor for %s (%v)", name, err)
					return nil, errors.New("Failed to get device descriptor")
				}

				serialnum, _ := usbDeviceHandle.StringDescriptorASCII(usbDeviceDescriptor.SerialNumberIndex)
				manufacturer, _ := usbDeviceHandle.StringDescriptorASCII(usbDeviceDescriptor.ManufacturerIndex)
				product, _ := usbDeviceHandle.StringDescriptorASCII(usbDeviceDescriptor.ProductIndex)
				b.logf(logrus.InfoLevel, "Found %v %v S/N %s using Vendor ID %v and Product ID %v", manufacturer,
					product,
					serialnum,
					vendorID,
					productID)
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
		b.logf(logrus.ErrorLevel, "Failed to find device: %s", name)
		return nil, errors.New("failed to find device")
	}

	if b.canDetach {
		err := usbDeviceHandle.DetachKernelDriver(device.ifaceNum)
		if err != nil {
			b.logf(logrus.WarnLevel, "Failed to detach kernel driver: %v", err)
			// Fail softly. This is a newer MacOS feature and may not work everywhere.
		}
	}

	err = usbDeviceHandle.ClaimInterface(device.ifaceNum)
	if err != nil {
		b.logf(logrus.ErrorLevel, "Failed to claim interface %d: %v", device.ifaceNum, err)
		return nil, errors.New("failed to claim interface")
	}

	b.currentConnectedDevice = device

	return device, nil
}

func (d *ZeroUSBDevice) Close(disconnected bool) error {
	if !disconnected {
		d.ClearBuffer()
	}

	if !atomic.CompareAndSwapInt32(&d.closed, 0, 1) {
		// already closed
		return nil
	}

	if d != nil && d.handle != nil {
		err := d.handle.ReleaseInterface(d.ifaceNum)
		if err != nil {
			d.logf(logrus.ErrorLevel, "Failed to release interface: %v", err)
		}

		d.handle.Close()
	}

	return nil
}

func (d *ZeroUSBDevice) GetIdentifier() *string {
	return d.Identifier
}

func (d *ZeroUSBDevice) ClearBuffer() {
	for {
		_, err := d.Read(64, 50)
		if err != nil {
			d.logf(logrus.DebugLevel, "Stopping ClearBuffer due to read error: %v", err)
			break
		}
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
		return 0, fmt.Errorf("attempt to write before opening connection")
	}

	d.logf(logrus.DebugLevel, "Attempting to write %d bytes", len(buf))

	locked := d.lock.TryLock()
	if !locked {
		return 0, fmt.Errorf("device lock busy")
	}
	defer d.lock.Unlock()

	bytesWritten, err := d.handle.BulkTransferOut(*d.writer, buf, 500)
	if err != nil {
		d.logf(logrus.ErrorLevel, "Write error: %v", err)
	} else {
		d.logf(logrus.DebugLevel, "Wrote %d bytes", bytesWritten)
	}

	return bytesWritten, err
}

func (d *ZeroUSBDevice) Read(length int, timeout int) ([]byte, error) {
	if d.writer == nil {
		return []byte{}, fmt.Errorf("attempt to read before opening connection")
	}

	d.logf(logrus.DebugLevel, "Attempting to read %d bytes with timeout %dms", length, timeout)

	if timeout == 0 {
		timeout = 5000
	}

	locked := d.lock.TryLock()
	if !locked {
		return []byte{}, fmt.Errorf("device lock busy")
	}
	defer d.lock.Unlock()

	readRes, _, err := d.handle.BulkTransferIn(*d.reader, length, timeout)
	if err != nil {
		d.logf(logrus.ErrorLevel, "Read error: %v", err)
		return nil, err
	}

	d.logf(logrus.DebugLevel, "Read %d bytes", len(readRes))

	return readRes, nil
}
