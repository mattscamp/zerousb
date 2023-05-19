package zerousb

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
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
	EpOutAddress     uint8
	Debug            bool
}

type ZeroUSB struct {
	usb       Context
	canDetach bool
	options   Options
}

func New(options Options, detach bool) (*ZeroUSB, error) {
	var usb Context

	err := Init(&usb)
	if err != nil {
		return nil, fmt.Errorf(`[zerousb] error when initializing zerousb. %v \n`, err)
	}

	return &ZeroUSB{
		usb:       usb,
		options:   options,
		canDetach: detach,
	}, nil
}

func (b *ZeroUSB) Close() {
	Exit(b.usb)
}

func hasIface(dev Device, options Options) (bool, error) {
	config, err := GetConfigDescriptor(dev, usbConfigIndex)
	if err != nil {
		return false, err
	}
	defer FreeConfigDescriptor(config)

	ifaces := config.Interface
	for _, iface := range ifaces {
		for _, alt := range iface.Altsetting {
			if alt.BNumEndpoints == 2 &&
				(alt.Endpoint[0].BEndpointAddress == options.EpInAddress || alt.Endpoint[1].BEndpointAddress == options.EpInAddress) &&
				(alt.Endpoint[0].BEndpointAddress == options.EpOutAddress || alt.Endpoint[1].BEndpointAddress == options.EpOutAddress) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (b *ZeroUSB) Connect(vendorID ID, productID ID, reset bool) (*ZeroUSBDevice, error) {
	deviceList, err := GetDeviceList(b.usb)
	if err != nil {
		return nil, err
	}

	defer func() {
		fmt.Print("[zerousb] freeing device list \n")
		FreeDeviceList(deviceList, 1)
	}()

	devices := make([]Device, 0)
	descs := make([]*DeviceDescriptor, 0)
	for _, dev := range deviceList {
		match, desc := b.isMatch(dev, vendorID, productID)
		if match {
			devices = append(devices, dev)
			descs = append(descs, desc)
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
		fmt.Printf("[zerousb] current configuration err %s \n", err.Error())
	}

	if currConf != int(b.options.ConfigAddress) {
		err = SetConfiguration(d, int(b.options.ConfigAddress))
		if err != nil {
			fmt.Printf("[zerousb] error at configuration set: %s \n", err.Error())
		}

		currConf, err = GetConfiguration(d)
		if err != nil {
			fmt.Printf("[zerousb] error at configuration get: %s \n", err.Error())
		}
	}
}

func (b *ZeroUSB) claimInterface(d DeviceHandle) (bool, error) {
	attach := false
	usbIfaceNum := int(b.options.InterfaceAddress)

	if b.canDetach {
		kernel, err := KernelDriverActive(d, usbIfaceNum)
		if err != nil {
			fmt.Print("[zerousb] detecting kernel driver failed \n")
			Close(d)
			return false, err
		}

		if kernel {
			attach = true
			fmt.Print("[zerousb] kernel driver active, detaching \n")
			err := DetachKernelDriver(d, usbIfaceNum)
			if err != nil {
				fmt.Print("[zerousb] detach of kernel driver failed \n")
				Close(d)
				return false, err
			}
		}
	}

	err := ClaimInterface(d, usbIfaceNum)
	if err != nil {
		fmt.Print("[zerousb] claiming interface failed \n")
		Close(d)
		return false, err
	}
	return attach, nil
}

func (b *ZeroUSB) connect(dev Device, reset bool, desc *DeviceDescriptor) (*ZeroUSBDevice, error) {
	d, err := Open(dev)
	if err != nil {
		return nil, err
	}

	if reset {
		err = ResetDevice(d)
		if err != nil {
			fmt.Printf("[zerousb] warning at device reset: %s \n", err)
		}
	}

	b.setConfiguration(d)

	attach, err := b.claimInterface(d)
	if err != nil {
		return nil, err
	}

	return &ZeroUSBDevice{
		dev:     d,
		closed:  0,
		attach:  attach,
		options: b.options,
		desc:    desc,
	}, nil
}

func (b *ZeroUSB) isMatch(dev Device, vendorID ID, productID ID) (bool, *DeviceDescriptor) {
	desc, err := GetDeviceDescriptor(dev)
	if err != nil {
		fmt.Printf("[zerousb] error getting device descriptor %v \n", err.Error())
		return false, nil
	}

	// Skip HID devices, they are handled directly by OS libraries
	if desc.BDeviceClass == CLASS_HID {
		return false, nil
	}

	vid := desc.IDVendor
	pid := desc.IDProduct
	if vid != vendorID || pid != productID {
		return false, nil
	}

	conf, err := GetActiveConfigDescriptor(dev)
	if err != nil {
		fmt.Printf("[zerousb] error getting config descriptor %v \n", err.Error())
		return false, nil
	}

	defer FreeConfigDescriptor(conf)

	exists, err := hasIface(dev, b.options)
	if err != nil {
		return false, nil
	}

	return exists, desc
}

type ZeroUSBDevice struct {
	dev       DeviceHandle
	options   Options
	closed    int32 // atomic
	readLock  sync.Mutex
	writeLock sync.Mutex
	attach    bool
	desc      *DeviceDescriptor
}

func (d *ZeroUSBDevice) Close(disconnected bool) error {
	atomic.StoreInt32(&d.closed, 1)

	if !disconnected {
		CancelSyncTransfersOnDevice(d.dev)
		d.clearBuffer()
	}

	iface := int(d.options.InterfaceAddress)
	err := ReleaseInterface(d.dev, iface)
	if err != nil {
		fmt.Printf("[zerusb]: error at releasing interface: %s", err)
	}

	if d.attach {
		err = AttachKernelDriver(d.dev, iface)
		if err != nil {
			// do not throw error, it is just re-attach anyway
			fmt.Printf("[zerusb]: error re-attaching driver: %s", err)
		}
	}

	Close(d.dev)

	return nil
}

func (d *ZeroUSBDevice) clearBuffer() {
	mutex := &d.readLock

	mutex.Lock()
	var err error
	var buf [64]byte

	for err == nil {
		_, err = BulkTransfer(d.dev, d.options.EpInAddress, buf[:], 50)
	}

	mutex.Unlock()
}

func (d *ZeroUSBDevice) readWrite(buf []byte, endpoint uint8, mutex sync.Locker, timeout int) (int, error) {
	for {
		closed := (atomic.LoadInt32(&d.closed)) == 1
		if closed {
			return 0, ErrDeviceClosed
		}

		mutex.Lock()
		p, err := BulkTransfer(d.dev, endpoint, buf, uint(timeout))
		mutex.Unlock()

		if err != nil {
			fmt.Sprintf("[zerousb] error seen in r/w: %s", err.Error())

			if isErrorDisconnect(err) {
				return 0, ErrDeviceDisconnected
			}

			return 0, err
		}

		if len(p) > 0 {
			return len(p), err
		}
	}
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
	mutex := &d.writeLock
	if d.options.Debug {
		fmt.Printf("[zerousb] DEBUG. Write. %+v \n", buf)
	}
	return d.readWrite(buf, d.options.EpOutAddress, mutex, 0)
}

func (d *ZeroUSBDevice) Read(buf []byte, timeout int) (int, error) {
	mutex := &d.readLock
	if d.options.Debug {
		fmt.Printf("[zerousb] DEBUG. Read. %+v \n", buf)
	}
	return d.readWrite(buf, d.options.EpInAddress, mutex, timeout)
}
