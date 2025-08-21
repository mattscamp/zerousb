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

// DeviceState represents the current state of a USB device
type DeviceState int

const (
	DeviceStateHealthy DeviceState = iota
	DeviceStateError
	DeviceStateRecovering
	DeviceStateDisconnected
)

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
	state              DeviceState
	errorCount         int32 // atomic
	lastError          error
	lastErrorTime      time.Time
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
		// End any watching first
		if b.endWatcher != nil {
			select {
			case b.endWatcher <- true:
			default:
			}
		}

		// Close current device with proper cleanup
		if b.currentConnectedDevice != nil {
			b.currentConnectedDevice.Close(false)
			b.currentConnectedDevice = nil
		}

		// Small delay to ensure cleanup completes
		time.Sleep(100 * time.Millisecond)

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

				// Check device health if we have a connected device
				if b.currentConnectedDevice != nil {
					state := b.currentConnectedDevice.GetState()
					errorCount := b.currentConnectedDevice.GetErrorCount()

					// Attempt recovery if device is in error state
					if state == DeviceStateError && errorCount > 0 {
						b.logf(logrus.WarnLevel, "Device in error state (errors: %d), attempting recovery", errorCount)
						if recoveryErr := b.currentConnectedDevice.RecoverDevice(); recoveryErr != nil {
							b.logf(logrus.ErrorLevel, "Device recovery failed: %v", recoveryErr)
							// Force disconnect and reconnect
							b.currentConnectedDevice.Close(true)
							b.currentConnectedDevice = nil
						} else {
							b.logf(logrus.InfoLevel, "Device recovery successful")
						}
					}
				}

				connectedDevices, err := b.usbContext.DeviceList()
				if err != nil {
					b.logf(logrus.ErrorLevel, "Getting devices: %+v", err)
					b.endWatcher <- true
					continue
				}

				watchedAndConnectedDevices := []DeviceWithId{}
				for _, device := range connectedDevices {
					index := indexOfVendorIDAndProductID(vendorAndProductIDs, []uint16{
						uint16(device.libusbDevice.device_descriptor.idVendor),
						uint16(device.libusbDevice.device_descriptor.idProduct),
					})
					if index != nil {
						watchedAndConnectedDevices = append(watchedAndConnectedDevices, DeviceWithId{
							Identifier: vendorAndProductIDs[*index].Identifier,
							Device:     device,
						})
					}
				}

				// Disconnect current device if gone
				shouldDisconnect := true
				if b.currentConnectedDevice != nil {
					for _, d := range watchedAndConnectedDevices {
						if b.currentConnectedDevice.handle != nil &&
							b.currentConnectedDevice.handle.libusbDeviceHandle.dev.device_descriptor.idVendor == d.Device.libusbDevice.device_descriptor.idVendor &&
							b.currentConnectedDevice.handle.libusbDeviceHandle.dev.device_descriptor.idProduct == d.Device.libusbDevice.device_descriptor.idProduct {
							shouldDisconnect = false
							break
						}
					}

					// Also disconnect if device state indicates it's disconnected
					if b.currentConnectedDevice.GetState() == DeviceStateDisconnected {
						shouldDisconnect = true
						b.logf(logrus.InfoLevel, "Device marked as disconnected, forcing disconnect")
					}
				} else {
					shouldDisconnect = false
				}

				if shouldDisconnect {
					b.logf(logrus.InfoLevel, "Detected UNPLUG event for device: %+v", b.currentConnectedDevice.Identifier)
					b.currentConnectedDevice.Close(true)
					b.currentConnectedDevice = nil
				}

				// Already connected and healthy
				if b.currentConnectedDevice != nil && b.currentConnectedDevice.IsHealthy() {
					continue
				}

				// No watched device found
				if len(watchedAndConnectedDevices) == 0 {
					continue
				}

				// Attempt reconnect
				devToConnect := watchedAndConnectedDevices[0]
				b.logf(logrus.InfoLevel, "Detected PLUG event for device: %+v", devToConnect.Identifier)
				deviceInstance, err := b.Connect(devToConnect.Identifier,
					uint16(devToConnect.Device.libusbDevice.device_descriptor.idVendor),
					uint16(devToConnect.Device.libusbDevice.device_descriptor.idProduct),
				)
				if err != nil {
					b.logf(logrus.WarnLevel, "Reconnect failed, will retry next tick: %+v", err)
					continue
				}
				b.currentConnectedDevice = deviceInstance
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
					state:              DeviceStateHealthy,
					errorCount:         0,
					lastErrorTime:      time.Time{},
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
		device.attach = true
	}

	err = usbDeviceHandle.ClaimInterface(device.ifaceNum)
	if err != nil {
		if strings.Contains(err.Error(), "LIBUSB_ERROR_BUSY") {
			b.logf(logrus.WarnLevel, "Interface busy, attempting to force release and reclaim")
			// Try to force release the interface first
			usbDeviceHandle.ReleaseInterface(device.ifaceNum)
			time.Sleep(100 * time.Millisecond)
			err = usbDeviceHandle.ClaimInterface(device.ifaceNum)
		}
		if err != nil {
			b.logf(logrus.ErrorLevel, "Failed to claim interface %d: %v", device.ifaceNum, err)
			// Clean up before returning error
			if device.attach {
				usbDeviceHandle.AttachKernelDriver(device.ifaceNum)
			}
			usbDeviceHandle.Close()
			return nil, errors.New("failed to claim interface")
		}
	}

	b.currentConnectedDevice = device

	return device, nil
}

func (d *ZeroUSBDevice) Close(disconnected bool) error {
	if !atomic.CompareAndSwapInt32(&d.closed, 0, 1) {
		// already closed
		return nil
	}

	if d != nil && d.handle != nil {
		// Clear buffer before releasing interface to prevent hanging transfers
		if !disconnected {
			d.ClearBuffer() // clear only if not a disconnect retry
		}

		// Release the interface with retry logic
		err := d.handle.ReleaseInterface(d.ifaceNum)
		if err != nil {
			d.logf(logrus.ErrorLevel, "Failed to release interface: %v", err)
			time.Sleep(50 * time.Millisecond)
			d.handle.ReleaseInterface(d.ifaceNum) // Ignore error on second attempt
		}

		// Re-attach kernel driver if we detached it
		if d.attach && runtime.GOOS != "windows" {
			if attachErr := d.handle.AttachKernelDriver(d.ifaceNum); attachErr != nil {
				d.logf(logrus.WarnLevel, "Failed to re-attach kernel driver: %v", attachErr)
			}
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

	// Retry logic with error recovery
	maxRetries := 3
	timeout := 500
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			d.logf(logrus.WarnLevel, "Write attempt %d/%d after error: %v", attempt+1, maxRetries, lastErr)
			time.Sleep(time.Duration(attempt*100) * time.Millisecond) // backoff
		}

		bytesWritten, err := d.handle.BulkTransferOut(*d.writer, buf, timeout)
		if err == nil {
			d.logf(logrus.DebugLevel, "Wrote %d bytes", bytesWritten)
			atomic.StoreInt32(&d.errorCount, 0)
			d.state = DeviceStateHealthy
			return bytesWritten, nil
		}

		lastErr = err
		atomic.AddInt32(&d.errorCount, 1)
		d.lastError = err
		d.lastErrorTime = time.Now()

		// Try to recover from pipe errors
		if strings.Contains(err.Error(), ErrorName(errorPipe)) {
			d.logf(logrus.WarnLevel, "Attempting to clear halt on write endpoint due to PIPE error")
			d.state = DeviceStateRecovering
			if clearErr := d.handle.ClearHalt(*d.writer); clearErr != nil {
				d.logf(logrus.ErrorLevel, "Failed to clear halt on write endpoint: %v", clearErr)
			}
		}

		// For timeout errors, try a longer timeout on retry
		if strings.Contains(err.Error(), ErrorName(errorTimeout)) && attempt < maxRetries-1 {
			timeout = timeout * 2
			if timeout > 2000 {
				timeout = 2000
			}
			d.logf(logrus.WarnLevel, "Increasing timeout to %dms for retry", timeout)
		}

		// Check if this is a disconnect error
		if IsErrorDisconnect(err) {
			d.logf(logrus.ErrorLevel, "Device disconnected during write: %v", err)
			d.state = DeviceStateDisconnected
			return bytesWritten, err
		}
	}

	d.logf(logrus.ErrorLevel, "Write failed after %d attempts: %v", maxRetries, lastErr)
	d.state = DeviceStateError
	return 0, lastErr
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

	// Retry logic with error recovery
	maxRetries := 3
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			d.logf(logrus.WarnLevel, "Read attempt %d/%d after error: %v", attempt+1, maxRetries, lastErr)
			time.Sleep(time.Duration(attempt*100) * time.Millisecond) // backoff
		}

		readRes, _, err := d.handle.BulkTransferIn(*d.reader, length, timeout)
		if err == nil {
			d.logf(logrus.DebugLevel, "Read %d bytes", len(readRes))
			// Reset error count on successful read
			atomic.StoreInt32(&d.errorCount, 0)
			d.state = DeviceStateHealthy
			return readRes, nil
		}

		lastErr = err
		atomic.AddInt32(&d.errorCount, 1)
		d.lastError = err
		d.lastErrorTime = time.Now()

		// Try to recover from pipe errors
		if strings.Contains(err.Error(), ErrorName(errorPipe)) {
			d.logf(logrus.WarnLevel, "Attempting to clear halt on read endpoint due to PIPE error")
			d.state = DeviceStateRecovering
			if clearErr := d.ClearHaltOnReader(); clearErr != nil {
				d.logf(logrus.ErrorLevel, "Failed to clear halt: %v", clearErr)
			}
		}

		// For timeout errors on Windows, try a shorter timeout on retry
		if strings.Contains(err.Error(), ErrorName(errorTimeout)) && attempt < maxRetries-1 {
			timeout = timeout / 2
			if timeout < 100 {
				timeout = 100
			}
			d.logf(logrus.WarnLevel, "Reducing timeout to %dms for retry", timeout)
		}

		// Check if this is a disconnect error
		if IsErrorDisconnect(err) {
			d.logf(logrus.ErrorLevel, "Device disconnected during read: %v", err)
			d.state = DeviceStateDisconnected
			return nil, err
		}
	}

	d.logf(logrus.ErrorLevel, "Read failed after %d attempts: %v", maxRetries, lastErr)
	d.state = DeviceStateError
	return nil, lastErr
}

func (d *ZeroUSBDevice) ClearHaltOnReader() error {
	if d.handle == nil {
		return fmt.Errorf("attempt to clear halt before opening connection")
	}

	d.logf(logrus.DebugLevel, "Attempting to clear halt on endpoint 0x%02x", d.reader)

	locked := d.lock.TryLock()
	if !locked {
		return fmt.Errorf("device lock busy")
	}
	defer d.lock.Unlock()

	err := d.handle.ClearHalt(*d.reader)
	if err != nil {
		d.logf(logrus.ErrorLevel, "Clear halt error: %v", err)
	} else {
		d.logf(logrus.DebugLevel, "Successfully cleared halt on endpoint 0x%02x", d.reader)
	}

	return err
}

// GetState returns the current state of the device
func (d *ZeroUSBDevice) GetState() DeviceState {
	d.lock.Lock()
	defer d.lock.Unlock()
	return d.state
}

// GetErrorCount returns the current error count
func (d *ZeroUSBDevice) GetErrorCount() int32 {
	return atomic.LoadInt32(&d.errorCount)
}

// GetLastError returns the last error and when it occurred
func (d *ZeroUSBDevice) GetLastError() (error, time.Time) {
	d.lock.Lock()
	defer d.lock.Unlock()
	return d.lastError, d.lastErrorTime
}

// IsHealthy returns true if the device is in a healthy state
func (d *ZeroUSBDevice) IsHealthy() bool {
	state := d.GetState()
	errorCount := d.GetErrorCount()
	return state == DeviceStateHealthy && errorCount < 5
}

// ResetErrorState resets the error count and state if the device is healthy
func (d *ZeroUSBDevice) ResetErrorState() {
	d.lock.Lock()
	defer d.lock.Unlock()
	if d.state != DeviceStateDisconnected {
		atomic.StoreInt32(&d.errorCount, 0)
		d.state = DeviceStateHealthy
		d.lastError = nil
		d.lastErrorTime = time.Time{}
	}
}

// RecoverDevice attempts to recover the device from an error state
func (d *ZeroUSBDevice) RecoverDevice() error {
	if atomic.LoadInt32(&d.closed) != 0 {
		return ErrDeviceClosed
	}

	d.lock.Lock()
	defer d.lock.Unlock()

	d.logf(logrus.InfoLevel, "Attempting device recovery from state: %v", d.state)
	d.state = DeviceStateRecovering

	// Step 1: Clear halt on both endpoints
	if d.reader != nil {
		if err := d.handle.ClearHalt(*d.reader); err != nil {
			d.logf(logrus.WarnLevel, "Failed to clear halt on read endpoint: %v", err)
		} else {
			d.logf(logrus.DebugLevel, "Cleared halt on read endpoint")
		}
	}

	if d.writer != nil {
		if err := d.handle.ClearHalt(*d.writer); err != nil {
			d.logf(logrus.WarnLevel, "Failed to clear halt on write endpoint: %v", err)
		} else {
			d.logf(logrus.DebugLevel, "Cleared halt on write endpoint")
		}
	}

	// Step 2: Reset the device (last resort)
	if d.GetErrorCount() > 10 {
		d.logf(logrus.WarnLevel, "High error count (%d), attempting device reset", d.GetErrorCount())
		if err := d.handle.ResetDevice(); err != nil {
			d.logf(logrus.ErrorLevel, "Device reset failed: %v", err)
			d.state = DeviceStateError
			return err
		}
		d.logf(logrus.InfoLevel, "Device reset successful")
	}

	// Step 3: Clear the buffer to remove stale data
	d.ClearBuffer()

	// Step 4: Reset error state
	atomic.StoreInt32(&d.errorCount, 0)
	d.state = DeviceStateHealthy
	d.lastError = nil
	d.lastErrorTime = time.Time{}

	d.logf(logrus.InfoLevel, "Device recovery completed successfully")
	return nil
}
