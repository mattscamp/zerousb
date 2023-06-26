package zerousb

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"

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
	ConfigAddress    uint8
	EpInAddress      uint8
	EpInType         uint8
	EpOutAddress     uint8
	EpOutType        uint8
	Debug            bool
}

type ZeroUSB struct {
	usb       Context
	canDetach bool
	options   Options
	logger    *logrus.Logger
}

func New(options Options, logger *logrus.Logger) (*ZeroUSB, error) {
	var usb Context

	err := Init(&usb)
	if err != nil {
		return nil, fmt.Errorf(`error when initializing zerousb. %v \n`, err)
	}

	return &ZeroUSB{
		usb:       usb,
		options:   options,
		canDetach: runtime.GOOS != "windows",
		logger:    logger,
	}, nil
}

func (b *ZeroUSB) Close() {
	Exit(b.usb)
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
	deviceList, err := GetDeviceList(b.usb)
	if err != nil {
		return nil, err
	}

	defer func() {
		b.Log("freeing device list")
		FreeDeviceList(deviceList, 1)
	}()

	devices := make([]Device, 0)
	descs := make([]*DeviceDescriptor, 0)
	for _, dev := range deviceList {
		matchingDescriptor := b.isMatch(dev, vendorID, productID)
		if matchingDescriptor != nil {
			devices = append(devices, dev)
			descs = append(descs, matchingDescriptor)
			break
		}
	}

	err = ErrNotFound
	for i, dev := range devices {
		res, errConn := b.connect(dev, reset, descs[i])
		if errConn == nil {
			return res, nil
		}
		err = errConn
	}

	return nil, err
}

func (b *ZeroUSB) setConfiguration(d DeviceHandle) {
	currConf, err := GetConfiguration(d)
	if err != nil {
		b.Error(fmt.Sprintf("current configuration err %s", err.Error()))
	}

	if currConf != int(b.options.ConfigAddress) {
		err = SetConfiguration(d, int(b.options.ConfigAddress))
		if err != nil {
			b.Error(fmt.Sprintf("error at configuration set: %s", err.Error()))
		}

		currConf, err = GetConfiguration(d)
		if err != nil {
			b.Error(fmt.Sprintf("error at configuration get: %s", err.Error()))
		}
	}
}

func (b *ZeroUSB) claimInterface(d DeviceHandle) error {
	usbIfaceNum := int(b.options.InterfaceAddress)

	if b.canDetach {
		err := DetachKernelDriver(d, usbIfaceNum)
		if err != nil {
			b.Warn(fmt.Sprint("detach of kernel driver failed"))
			// Fail softly. This is a newer MacOS feature any may not work everywhere.
		}
	}

	err := ClaimInterface(d, usbIfaceNum)
	if err != nil {
		b.Error(fmt.Sprint("claiming interface failed"))
		Close(d)
		return err
	}
	return nil
}

func (b *ZeroUSB) connect(dev Device, reset bool, desc *DeviceDescriptor) (*ZeroUSBDevice, error) {
	d, err := Open(dev)
	if err != nil {
		return nil, err
	}

	if reset {
		err = ResetDevice(d)
		if err != nil {
			b.Warn(fmt.Sprintf("warning at device reset: %s", err))
		}
	}

	err = b.claimInterface(d)
	if err != nil {
		return nil, err
	}

	return &ZeroUSBDevice{
		dev:     d,
		closed:  0,
		options: b.options,
		logger:  b.logger,
		desc:    desc,
	}, nil
}

func (b *ZeroUSB) isMatch(dev Device, vendorID ID, productID ID) *DeviceDescriptor {
	desc, err := GetDeviceDescriptor(dev)
	if err != nil {
		b.Error(fmt.Sprintf("error getting device descriptor %v", err.Error()))
		return nil
	}

	// Skip HID devices, they are handled directly by OS libraries
	if desc.BDeviceClass == CLASS_HID {
		return nil
	}

	vid := desc.IDVendor
	pid := desc.IDProduct
	if vid != vendorID || pid != productID {
		return nil
	}

	var matchingDescriptor *DeviceDescriptor
	// Iterate over all the configurations and find raw interfaces
	for cfgnum := 0; cfgnum < int(desc.BNumConfigurations); cfgnum++ {
		// Retrieve the all the possible USB configurations of the device
		conf, err := GetConfigDescriptor(dev, uint8(cfgnum))
		if err != nil {
			b.Error(fmt.Sprintf("error getting config descriptor %v", err.Error()))
			continue
		}
		defer FreeConfigDescriptor(conf)

		// Drill down into each advertised interface
		for _, iface := range conf.Interface {
			if iface.NumAltsetting == 0 {
				continue
			}
			for _, alt := range iface.Altsetting {
				// Skip HID interfaces, they are handled directly by OS libraries
				if alt.BInterfaceClass == CLASS_HID {
					continue
				}

				// Find the endpoints that can speak libusb bulk or interrupt
				for _, end := range alt.Endpoint {
					// Skip any non-interrupt and bulk endpoints
					if end.BmAttributes != TRANSFER_TYPE_INTERRUPT && end.BmAttributes != TRANSFER_TYPE_BULK {
						continue
					}
					if end.BEndpointAddress&ENDPOINT_IN == ENDPOINT_IN {
						b.options.EpInAddress = end.BEndpointAddress
						b.options.EpInType = end.BmAttributes
					} else {
						b.options.EpOutAddress = end.BEndpointAddress
						b.options.EpOutType = end.BmAttributes
					}
				}
				// If both in and out interrupts are available, match the device
				if b.options.EpInAddress != 0 && b.options.EpOutAddress != 0 {
					// Enumeration matched, bump the device refcount to avoid cleaning it up
					matchingDescriptor = desc
					b.options.InterfaceAddress = alt.BInterfaceNumber
					break
				}
			}
		}
	}

	return matchingDescriptor
}

type ZeroUSBDevice struct {
	dev     DeviceHandle
	options Options
	logger  *logrus.Logger
	closed  int32 // atomic
	lock    sync.Mutex
	attach  bool
	desc    *DeviceDescriptor
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
	atomic.StoreInt32(&d.closed, 1)

	if !disconnected {
		CancelSyncTransfersOnDevice(d.dev)
		d.ClearBuffer()
	}

	iface := int(d.options.InterfaceAddress)
	err := ReleaseInterface(d.dev, iface)
	if err != nil {
		d.Error(fmt.Sprintf("error at releasing interface: %s", err))
	}

	Close(d.dev)

	return nil
}

func (d *ZeroUSBDevice) ClearBuffer() {
	var err error
	var buf [64]byte

	for err == nil {
		_, err = d.Read(buf[:], 50)
	}
}

func (d *ZeroUSBDevice) readWrite(buf []byte, endpoint uint8, mutex sync.Locker, timeout int) (int, error) {
	mutex.Lock()
	defer mutex.Unlock()

	var p []byte
	var err error

	if d.options.EpInAddress == endpoint && d.options.EpInType == TRANSFER_TYPE_BULK {
		p, err = BulkTransfer(d.dev, endpoint, buf, uint(timeout))
	}

	if d.options.EpInAddress == endpoint && d.options.EpInType == TRANSFER_TYPE_INTERRUPT {
		p, err = InterruptTransfer(d.dev, endpoint, buf, uint(timeout))
	}

	if d.options.EpOutAddress == endpoint && d.options.EpOutType == TRANSFER_TYPE_BULK {
		p, err = BulkTransfer(d.dev, endpoint, buf, uint(timeout))
	}

	if d.options.EpOutAddress == endpoint && d.options.EpOutType == TRANSFER_TYPE_INTERRUPT {
		p, err = InterruptTransfer(d.dev, endpoint, buf, uint(timeout))
	}

	if err != nil {
		d.Error(fmt.Sprintf("error seen in r/w: %s. Buffer: %b. Endpoint: %v. Res: %+v", err.Error(), buf, endpoint, len(p)))

		if isErrorDisconnect(err) {
			return 0, ErrDeviceDisconnected
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
	return d.desc
}

func (d *ZeroUSBDevice) Write(buf []byte) (int, error) {
	if d.options.Debug {
		d.Log(fmt.Sprintf("DEBUG. Write. %+v \n", buf))
	}
	return d.readWrite(buf, d.options.EpOutAddress, &d.lock, 0)
}

func (d *ZeroUSBDevice) Read(buf []byte, timeout int) (int, error) {
	if d.options.Debug {
		d.Log(fmt.Sprintf("DEBUG. Read. %+v \n", buf))
	}
	// default read timeout
	if timeout == 0 {
		timeout = 5000
	}
	return d.readWrite(buf, d.options.EpInAddress, &d.lock, timeout)
}
