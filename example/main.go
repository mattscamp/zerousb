// go:build none

package main

import (
	"fmt"

	"github.com/mattscamp/zerousb"
	"github.com/sirupsen/logrus"
)

const ExampleVendorId = zerousb.ID(0x0483)
const ExampleProductId = zerousb.ID(0xa27e)
const ExampleReadEndpointAddress = 0x81
const ExampleWriteEndpointAddress = 0x3
const ExampleInterfaceAddress = 0x1

var ExampleConfigAddress = uint8(0x1)

func main() {
	// Enumerate over all connected devices
	zerousb, err := zerousb.New(zerousb.Options{
		InterfaceAddress: ExampleInterfaceAddress,
	}, logrus.New())
	if err != nil {
		panic(err)
	}
	device, err := zerousb.Connect(ExampleVendorId, ExampleProductId, false)
	if err != nil {
		panic(err)
	}

	details, _ := device.Details()
	fmt.Printf("Found Vendor ID %v and Product ID %v\n",
		details.ProductID,
		details.VendorID,
	)

	wrote, err := device.Write([]byte{0x30, 0x02})
	if err != nil {
		panic(err)
	}

	fmt.Printf("Wrote: %v\n", wrote)
	buf := make([]byte, 32)
	readRes, err := device.Read(buf, 0)
	if err != nil {
		panic(err)
	}

	fmt.Printf("Read: %v\n", readRes)

	device.Close(false)
}
