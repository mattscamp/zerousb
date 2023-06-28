package zerousb

import (
	"errors"
	"fmt"
	"runtime"
	"sync"

	libusb "github.com/gotmc/libusb/v2"
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
	EpInAddress      *byte
	EpOutAddress     *byte
	Debug            *bool
}

type ZeroUSB struct {
	usbContext *libusb.Context
	canDetach  bool
	options    Options
	logger     *logrus.Logger
}

type ZeroUSBDevice struct {
	dev         *libusb.Device
	options     Options
	logger      *logrus.Logger
	closed      int32 // atomic
	lock        sync.Mutex
	attach      bool
	handle      *libusb.DeviceHandle
	endpointIn  *libusb.EndpointDescriptor
	endpointOut *libusb.EndpointDescriptor
}

func New(options Options, logger *logrus.Logger) (*ZeroUSB, error) {
	usb, err := libusb.NewContext()
	if err != nil {
		return nil, fmt.Errorf(`error when initializing zerousb. %v \n`, err)
	}

	return &ZeroUSB{
		usbContext: usb,
		options:    options,
		canDetach:  runtime.GOOS != "windows",
		logger:     logger,
	}, nil
}

func (b *ZeroUSB) Close() {
	b.usbContext.Close()
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
	dev, handle, err := b.usbContext.OpenDeviceWithVendorProduct(uint16(vendorID), uint16(productID))
	if err != nil {
		b.Error(fmt.Sprintf("opening device failed: %s", err.Error()))
		return nil, err
	}

	if b.canDetach {
		err := handle.DetachKernelDriver(int(b.options.InterfaceAddress))
		if err != nil {
			b.Warn(fmt.Sprintf("detach of kernal driver failed: %s", err.Error()))
			// Fail softly. This is a newer MacOS feature any may not work everywhere.
		}
	}

	if b.options.ConfigAddress != nil {
		err := handle.SetConfiguration(int(*b.options.ConfigAddress))
		if err != nil {
			b.Error(fmt.Sprint("setting active config descriptor failed"))
			handle.Close()
			b.Close()
			return nil, err
		}
	}

	configDescriptor, err := dev.ActiveConfigDescriptor()
	if err != nil {
		b.Error(fmt.Sprint("getting active config descriptor failed"))
		handle.Close()
		b.Close()
		return nil, err
	}

	err = handle.ClaimInterface(int(b.options.InterfaceAddress))
	if err != nil {
		b.Error(fmt.Sprint("claiming interface failed"))
		handle.Close()
		b.Close()
		return nil, err
	}

	in, out := FindEndpoints(configDescriptor.SupportedInterfaces)
	if in == nil || out == nil {
		b.Error(fmt.Sprint("getting endpoints failed"))
		handle.Close()
		b.Close()
		return nil, errors.New("Failed to connect: Get endpoints.")
	}

	return &ZeroUSBDevice{
		dev:         dev,
		options:     b.options,
		logger:      b.logger,
		handle:      handle,
		endpointIn:  in,
		endpointOut: out,
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

	err := d.handle.ReleaseInterface(int(d.options.InterfaceAddress))
	if err != nil {
		d.Error(fmt.Sprintf("error at releasing interface: %s", err))
	}

	d.handle.Close()

	return nil
}

func (d *ZeroUSBDevice) ClearBuffer() {
	var err error
	var buf [64]byte

	for err == nil {
		_, err = d.readWrite(buf[:], d.endpointOut, &d.lock, 50, true)
	}
}

func (d *ZeroUSBDevice) readWrite(buf []byte, endpoint *libusb.EndpointDescriptor, mutex sync.Locker, timeout int, ignoreErrors bool) (int, error) {
	mutex.Lock()
	defer mutex.Unlock()

	var transferred int
	var err error

	if endpoint.TransferType() == libusb.BulkTransfer {
		transferred, err = d.handle.BulkTransfer(endpoint.EndpointAddress, buf, len(buf), timeout)
	}

	if endpoint.TransferType() == libusb.InterruptTransfer {
		transferred, err = d.handle.InterruptTransfer(endpoint.EndpointAddress, buf, len(buf), timeout)
	}

	if err != nil {
		if !ignoreErrors {
			d.Error(fmt.Sprintf("error seen in r/w: %s. Buffer: %b. Endpoint: %v. Res: %+v", err.Error(), buf, endpoint, transferred))
		}
		return 0, err
	}

	return transferred, err
}

func (d *ZeroUSBDevice) Details() (*libusb.Descriptor, error) {
	return d.dev.DeviceDescriptor()
}

func (d *ZeroUSBDevice) Write(buf []byte) (int, error) {
	if d.options.Debug != nil && *d.options.Debug == true {
		d.Log(fmt.Sprintf("DEBUG. Write. %+v \n", buf))
	}

	return d.readWrite(buf, d.endpointOut, &d.lock, 0, false)
}

func (d *ZeroUSBDevice) Read(buf []byte, timeout int) (int, error) {
	if d.options.Debug != nil && *d.options.Debug == true {
		d.Log(fmt.Sprintf("DEBUG. Read. %+v \n", buf))
	}
	// default read timeout
	if timeout == 0 {
		timeout = 5000
	}
	return d.readWrite(buf, d.endpointIn, &d.lock, timeout, false)
}
