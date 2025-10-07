package zerousb

import (
	"context"
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
	LogLevel        LogLevel
	DisableRecovery bool
}

type ZeroUSB struct {
	usbContext             *Context
	canDetach              bool
	options                Options
	logger                 *logrus.Logger
	vendorID               ID
	productID              ID
	currentConnectedDevice *ZeroUSBDevice
	watcherCtx             context.Context    // watcher context
	watcherCancel          context.CancelFunc //  cancel function
	watcherDone            chan struct{}      // signals watcher has exited
	watcherActive          bool
	watcherMu              sync.Mutex
}

type ZeroUSBDevice struct {
	Identifier         *string
	dev                *Device
	options            Options
	logger             *logrus.Logger
	closed             int32 // atomic
	lock               sync.Mutex
	closeMu            sync.Mutex
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
		b.EndWatch()

		if b.watcherDone != nil {
			<-b.watcherDone
		}

		if b.currentConnectedDevice != nil {
			b.currentConnectedDevice.Close(false)
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

type EnumerateDetails struct {
	VID string
	PID string
}

func (b *ZeroUSB) Enumerate() []EnumerateDetails {
	if b.usbContext == nil {
		b.logf(logrus.ErrorLevel, "No context. Initialize ZeroUSB.")
		return nil
	}

	devices, err := b.usbContext.DeviceList()
	if err != nil {
		b.logf(logrus.ErrorLevel, "Getting devices: %+v", err)
		return nil
	}

	connectedDevices := []EnumerateDetails{}
	for _, device := range devices {
		connectedDevices = append(connectedDevices, EnumerateDetails{
			VID: fmt.Sprintf("%04x", uint16(device.libusbDevice.device_descriptor.idVendor)),
			PID: fmt.Sprintf("%04x", uint16(device.libusbDevice.device_descriptor.idProduct)),
		})
	}

	return connectedDevices
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
	b.watcherMu.Lock()
	defer b.watcherMu.Unlock()

	if b.watcherActive && b.watcherCancel != nil {
		b.watcherCancel()
		b.watcherCancel = nil
	}
}

func (dev *ZeroUSBDevice) matchesDevice(d DeviceWithId) bool {
	if dev.handle == nil || dev.handle.libusbDeviceHandle == nil {
		return false
	}
	return dev.handle.libusbDeviceHandle.dev.device_descriptor.idVendor == d.Device.libusbDevice.device_descriptor.idVendor &&
		dev.handle.libusbDeviceHandle.dev.device_descriptor.idProduct == d.Device.libusbDevice.device_descriptor.idProduct
}

func (b *ZeroUSB) IsWatching() bool {
	return b.watcherActive && b.watcherCancel != nil
}

func (b *ZeroUSB) Watch(vendorAndProductIDs []VendorAndProduct) error {
	if b.usbContext == nil {
		return errors.New("No context. Initialize ZeroUSB.")
	}

	b.watcherMu.Lock()
	if b.watcherActive {
		b.watcherMu.Unlock()
		return errors.New("Watcher is already running")
	}

	b.watcherActive = true
	b.watcherDone = make(chan struct{})
	b.watcherCtx, b.watcherCancel = context.WithCancel(context.Background())
	b.watcherMu.Unlock()

	ticker := time.NewTicker(1 * time.Second)

	go func() {
		defer func() {
			ticker.Stop()
			b.watcherMu.Lock()
			b.watcherActive = false
			b.watcherMu.Unlock()
			close(b.watcherDone)
		}()

		for {
			select {
			case <-b.watcherCtx.Done():
				return
			case <-ticker.C:
				// Periodically check context inside long operations
				select {
				case <-b.watcherCtx.Done():
					return
				default:
				}

				if b.usbContext == nil {
					b.logf(logrus.ErrorLevel, "USB context gone, stopping watcher")
					return
				}

				// Device health check
				if b.currentConnectedDevice != nil {
					state := b.currentConnectedDevice.GetState()
					errorCount := b.currentConnectedDevice.GetErrorCount()
					if !b.options.DisableRecovery && state == DeviceStateError && errorCount > 0 {
						b.logf(logrus.WarnLevel, "Device in error state, attempting recovery")
						if err := b.currentConnectedDevice.RecoverDevice(b.watcherCtx); err != nil {
							b.logf(logrus.ErrorLevel, "Recovery failed: %v", err)
							b.currentConnectedDevice.Close(true)
							b.currentConnectedDevice = nil
						} else {
							b.logf(logrus.InfoLevel, "Device recovery successful")
						}
					}
				}

				// List devices (transient failure → continue)
				connectedDevices, err := b.usbContext.DeviceList()
				if err != nil {
					b.logf(logrus.ErrorLevel, "Getting devices failed, retrying: %v", err)
					continue
				}

				// Filter watched devices
				watchedDevices := []DeviceWithId{}
				for _, dev := range connectedDevices {
					index := indexOfVendorIDAndProductID(vendorAndProductIDs, []uint16{
						uint16(dev.libusbDevice.device_descriptor.idVendor),
						uint16(dev.libusbDevice.device_descriptor.idProduct),
					})
					if index != nil {
						watchedDevices = append(watchedDevices, DeviceWithId{
							Identifier: vendorAndProductIDs[*index].Identifier,
							Device:     dev,
						})
					}
				}

				// Handle disconnects
				if b.currentConnectedDevice != nil {
					shouldDisconnect := true
					for _, d := range watchedDevices {
						if b.currentConnectedDevice.matchesDevice(d) {
							shouldDisconnect = false
							break
						}
					}
					if b.currentConnectedDevice.GetState() == DeviceStateDisconnected {
						shouldDisconnect = true
					}
					if shouldDisconnect {
						b.logf(logrus.InfoLevel, "Detected UNPLUG for %v", b.currentConnectedDevice.Identifier)
						b.currentConnectedDevice.Close(true)
						b.currentConnectedDevice = nil
					}
				}

				// If healthy device connected, continue
				if b.currentConnectedDevice != nil && b.currentConnectedDevice.IsHealthy() {
					continue
				}

				// Attempt reconnect if any watched device found
				if len(watchedDevices) > 0 {
					devToConnect := watchedDevices[0]
					b.logf(logrus.InfoLevel, "Detected PLUG for %v", devToConnect.Identifier)
					deviceInstance, err := b.Connect(
						b.watcherCtx,
						devToConnect.Identifier,
						uint16(devToConnect.Device.libusbDevice.device_descriptor.idVendor),
						uint16(devToConnect.Device.libusbDevice.device_descriptor.idProduct),
					)
					if err != nil {
						b.logf(logrus.WarnLevel, "Reconnect failed, will retry next tick: %v", err)
						continue
					}
					b.currentConnectedDevice = deviceInstance
				}
			}
		}
	}()

	return nil
}

func (b *ZeroUSB) Connect(ctx context.Context, name *string, vendorID, productID uint16) (*ZeroUSBDevice, error) {
	if b.usbContext == nil {
		return nil, errors.New("No context. Initialize ZeroUSB.")
	}

	var device *ZeroUSBDevice
	b.logf(logrus.InfoLevel, "Attempting to open device: %s", name)

	// Check for cancellation before attempting OS device open
	abortIfCancelled(ctx, nil)

	usbDevice, usbDeviceHandle, err := b.usbContext.OpenDeviceWithVendorProduct(vendorID, productID)
	if err != nil {
		b.logf(logrus.ErrorLevel, "Failed to find device %s (%v)", name, err)
		return nil, errors.New("Unable to find device.")
	}

	abortIfCancelled(ctx, func() { usbDeviceHandle.Close() })

	activeCfg, err := usbDevice.ActiveConfigDescriptor()
	if err != nil {
		usbDeviceHandle.Close()
		b.logf(logrus.ErrorLevel, "Failed get active config for %s (%v)", name, err)
		return nil, errors.New("Unable to get active config")
	}

	// Device interface parsing remains unchanged
	ifaces := activeCfg.SupportedInterfaces
	for _, iface := range ifaces {
		if iface.NumAltSettings == 0 {
			continue
		}

		for _, alt := range iface.InterfaceDescriptors {
			if alt.InterfaceClass == uint8(hid) {
				continue
			}

			var reader, writer *endpointAddress
			var readerTransferType, writerTransferType TransferType

			for _, end := range alt.EndpointDescriptors {
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

			if reader != nil && writer != nil {
				abortIfCancelled(ctx, func() { usbDeviceHandle.Close() })

				usbDeviceDescriptor, err := usbDevice.DeviceDescriptor()
				if err != nil {
					usbDeviceHandle.Close()
					b.logf(logrus.ErrorLevel, "Failed to get device descriptor for %s (%v)", name, err)
					return nil, errors.New("Failed to get device descriptor")
				}

				serialnum, _ := usbDeviceHandle.StringDescriptorASCII(usbDeviceDescriptor.SerialNumberIndex)
				manufacturer, _ := usbDeviceHandle.StringDescriptorASCII(usbDeviceDescriptor.ManufacturerIndex)
				product, _ := usbDeviceHandle.StringDescriptorASCII(usbDeviceDescriptor.ProductIndex)
				b.logf(logrus.InfoLevel, "Found %v %v S/N %s using Vendor ID %v and Product ID %v",
					manufacturer, product, serialnum, vendorID, productID)

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
		usbDeviceHandle.Close()
		b.logf(logrus.ErrorLevel, "Failed to find device: %s", name)
		return nil, errors.New("failed to find device")
	}

	// Detach kernel driver if supported
	if b.canDetach {
		abortIfCancelled(ctx, func() { device.handle.Close() })

		err := usbDeviceHandle.DetachKernelDriver(device.ifaceNum)
		if err != nil {
			b.logf(logrus.WarnLevel, "Failed to detach kernel driver: %v", err)
		}
		device.attach = true
	}

	abortIfCancelled(ctx, func() { device.handle.Close() })

	// Claim interface with retry logic
	err = usbDeviceHandle.ClaimInterface(device.ifaceNum)
	if err != nil {
		if strings.Contains(err.Error(), "LIBUSB_ERROR_BUSY") {
			b.logf(logrus.WarnLevel, "Interface busy, attempting to force release and reclaim")
			usbDeviceHandle.ReleaseInterface(device.ifaceNum)
			time.Sleep(100 * time.Millisecond)
			err = usbDeviceHandle.ClaimInterface(device.ifaceNum)
		}
		if err != nil {
			if device.attach {
				usbDeviceHandle.AttachKernelDriver(device.ifaceNum)
			}
			usbDeviceHandle.Close()
			b.logf(logrus.ErrorLevel, "Failed to claim interface %d: %v", device.ifaceNum, err)
			return nil, errors.New("failed to claim interface")
		}
	}

	b.currentConnectedDevice = device
	return device, nil
}

func (d *ZeroUSBDevice) Close(disconnected bool) error {
	d.closeMu.Lock()
	defer d.closeMu.Unlock()
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
	if d.reader == nil {
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
func (d *ZeroUSBDevice) RecoverDevice(ctx context.Context) error {
	if atomic.LoadInt32(&d.closed) != 0 {
		return ErrDeviceClosed
	}

	d.lock.Lock()
	defer d.lock.Unlock()

	// Check context before doing any work
	abortIfCancelled(ctx, func() { d.logf(logrus.InfoLevel, "Recovery aborted due to watcher shutdown") })

	d.logf(logrus.InfoLevel, "Attempting device recovery from state: %v", d.state)
	d.state = DeviceStateRecovering

	// Step 1: Clear halt on both endpoints
	if d.reader != nil {
		abortIfCancelled(ctx, nil)

		if err := d.handle.ClearHalt(*d.reader); err != nil {
			d.logf(logrus.WarnLevel, "Failed to clear halt on read endpoint: %v", err)
		} else {
			d.logf(logrus.DebugLevel, "Cleared halt on read endpoint")
		}
	}

	if d.writer != nil {
		abortIfCancelled(ctx, nil)

		if err := d.handle.ClearHalt(*d.writer); err != nil {
			d.logf(logrus.WarnLevel, "Failed to clear halt on write endpoint: %v", err)
		} else {
			d.logf(logrus.DebugLevel, "Cleared halt on write endpoint")
		}
	}

	// Step 2: Reset the device (last resort)
	if d.GetErrorCount() > 10 {
		abortIfCancelled(ctx, nil)

		d.logf(logrus.WarnLevel, "High error count (%d), attempting device reset", d.GetErrorCount())
		if err := d.handle.ResetDevice(); err != nil {
			d.logf(logrus.ErrorLevel, "Device reset failed: %v", err)
			d.state = DeviceStateError
			return err
		}
		d.logf(logrus.InfoLevel, "Device reset successful")
	}

	// Step 3: Clear the buffer to remove stale data
	abortIfCancelled(ctx, nil)
	d.ClearBuffer()

	// Step 4: Reset error state
	atomic.StoreInt32(&d.errorCount, 0)
	d.state = DeviceStateHealthy
	d.lastError = nil
	d.lastErrorTime = time.Time{}

	d.logf(logrus.InfoLevel, "Device recovery completed successfully")
	return nil
}

func abortIfCancelled(ctx context.Context, cleanup func()) error {
	select {
	case <-ctx.Done():
		if cleanup != nil {
			cleanup()
		}
		return ctx.Err()
	default:
		return nil
	}
}
