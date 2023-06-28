package zerousb

import (
	"errors"
	"fmt"
	"runtime"
	"sync"

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

type Options struct {
	InterfaceAddress uint8
	ConfigAddress    *uint8
	EpInAddress      byte
	EpOutAddress     byte
	EpInType         int
	EpOutType        int
	Debug            *bool
}

type ZeroUSB struct {
	usbContext *Context
	canDetach  bool
	options    Options
	logger     *logrus.Logger
	vendorID   ID
	productID  ID
}

type ZeroUSBDevice struct {
	dev     *Device
	options Options
	logger  *logrus.Logger
	closed  int32 // atomic
	lock    sync.Mutex
	attach  bool
	handle  *DeviceHandle
}

func New(options Options, logger *logrus.Logger) (*ZeroUSB, error) {
	var usb Context

	err := Init(&usb)
	if err != nil {
		return nil, fmt.Errorf(`[zerousb] error when initializing zerousb. %v \n`, err)
	}

	return &ZeroUSB{
		usbContext: &usb,
		options:    options,
		canDetach:  runtime.GOOS != "windows",
		logger:     logger,
	}, nil
}

func (b *ZeroUSB) Close() {
	if b.usbContext != nil {
		Exit(*b.usbContext)
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

func (b *ZeroUSB) Connect(vendorID ID, productID ID, reset bool) (*ZeroUSBDevice, error) {
	if b.usbContext == nil {
		return nil, errors.New("No context. Initialize ZeroUSB.")
	}

	handle := OpenDeviceWithVIDPID(*b.usbContext, uint16(vendorID), uint16(productID))
	if handle == nil {
		return nil, errors.New("Unable to open. Device not found.")
	}

	if b.canDetach {
		err := DetachKernelDriver(*handle, int(b.options.InterfaceAddress))
		if err != nil {
			b.Warn(fmt.Sprintf("detach of kernal driver failed: %s", err.Error()))
			// Fail softly. This is a newer MacOS feature any may not work everywhere.
		}
	}

	if b.options.ConfigAddress != nil {
		err := SetConfiguration(*handle, int(*b.options.ConfigAddress))
		if err != nil {
			b.Error(fmt.Sprint("setting active config descriptor failed"))
			Close(*handle)
			b.Close()
			return nil, err
		}
	}

	dev := GetDevice(*handle)
	configDescriptor, err := GetActiveConfigDescriptor(dev)
	if err != nil {
		b.Error(fmt.Sprint("getting active config descriptor failed"))
		Close(*handle)
		b.Close()
		return nil, err
	}

	defer FreeConfigDescriptor(configDescriptor)

	err = ClaimInterface(*handle, int(b.options.InterfaceAddress))
	if err != nil {
		b.Error(fmt.Sprint("claiming interface failed"))
		Close(*handle)
		b.Close()
		return nil, err
	}

	return &ZeroUSBDevice{
		dev:     &dev,
		options: b.options,
		logger:  b.logger,
		handle:  handle,
	}, nil
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

func (d *ZeroUSBDevice) Close(disconnected bool) error {
	if !disconnected {
		d.ClearBuffer()
	}

	err := ReleaseInterface(*d.handle, int(d.options.InterfaceAddress))
	if err != nil {
		d.Error(fmt.Sprintf("error at releasing interface: %s", err))
	}

	Close(*d.handle)

	return nil
}

func (d *ZeroUSBDevice) ClearBuffer() {
	var err error
	var buf [64]byte

	for err == nil {
		_, err = d.readWrite(buf[:], d.options.EpInAddress, &d.lock, 50, true)
	}
}

func (d *ZeroUSBDevice) readWrite(buf []byte, endpoint byte, mutex sync.Locker, timeout uint, ignoreErrors bool) (int, error) {
	var p []byte
	var err error

	if d.options.EpInAddress == endpoint && d.options.EpInType == TRANSFER_TYPE_BULK {
		p, err = BulkTransfer(*d.handle, endpoint, buf, uint(timeout))
	}

	if d.options.EpInAddress == endpoint && d.options.EpInType == TRANSFER_TYPE_INTERRUPT {
		p, err = InterruptTransfer(*d.handle, endpoint, buf, uint(timeout))
	}

	if d.options.EpOutAddress == endpoint && d.options.EpOutType == TRANSFER_TYPE_BULK {
		p, err = BulkTransfer(*d.handle, endpoint, buf, uint(timeout))
	}

	if d.options.EpOutAddress == endpoint && d.options.EpOutType == TRANSFER_TYPE_INTERRUPT {
		p, err = InterruptTransfer(*d.handle, endpoint, buf, uint(timeout))
	}

	if err != nil {
		if isErrorDisconnect(err) {
			return 0, ErrDeviceDisconnected
		} else if !ignoreErrors {
			d.Error(fmt.Sprintf("error seen in r/w: %s. Buffer: %b. Endpoint: %v. Res: %+v", err.Error(), buf, endpoint, len(p)))
			ResetDevice(*d.handle)
		}

		return 0, err
	}
	return len(p), err
}

func isErrorDisconnect(err error) bool {
	return (err.Error() == ErrorName(int(ERROR_IO)) ||
		err.Error() == ErrorName(int(ERROR_NO_DEVICE)) ||
		err.Error() == ErrorName(int(ERROR_OTHER)) ||
		err.Error() == ErrorName(int(ERROR_PIPE)))
}

func (d *ZeroUSBDevice) Details() *DeviceDescriptor {
	desc, _ := GetDeviceDescriptor(*d.dev)
	return desc
}

func (d *ZeroUSBDevice) Write(buf []byte) (int, error) {
	if d.options.Debug != nil && *d.options.Debug == true {
		d.Log(fmt.Sprintf("DEBUG. Write. %+v \n", buf))
	}

	return d.readWrite(buf, d.options.EpOutAddress, &d.lock, 0, false)
}

func (d *ZeroUSBDevice) Read(buf []byte, timeout int) (int, error) {
	if d.options.Debug != nil && *d.options.Debug == true {
		d.Log(fmt.Sprintf("DEBUG. Read. %+v \n", buf))
	}
	// default read timeout
	if timeout == 0 {
		timeout = 5000
	}
	return d.readWrite(buf, d.options.EpInAddress, &d.lock, uint(timeout), false)
}
