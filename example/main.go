// go:build none

package main

import (
	"fmt"

	"github.com/mattscamp/zerousb"
)

const ExampleVendorId = zerousb.ID(0x0483)
const ExampleProductId = zerousb.ID(0xa27e)
const ExampleReadEndpointAddress = 0x81
const ExampleWriteEndpointAddress = 0x3
const ExampleInterfaceAddress = 0x1
const ExampleConfigAddress = 0x1

func main() {
	// Enumerate over all connected devices
	zerousb, err := zerousb.New(zerousb.Options{
		InterfaceAddress: ExampleInterfaceAddress,
		ConfigAddress:    ExampleConfigAddress,
		EpInAddress:      ExampleReadEndpointAddress,
		EpOutAddress:     ExampleWriteEndpointAddress,
	}, true)
	if err != nil {
		panic(err)
	}
	device, err := zerousb.Connect(ExampleVendorId, ExampleProductId, false)
	if err != nil {
		panic(err)
	}

	fmt.Printf("%v\n", device.Details())

	device.Close(false)
}
