// go:build none

package main

import (
	"fmt"

	"github.com/mattscamp/zerousb.v2"
	"github.com/sirupsen/logrus"
)

const ExampleVendorId = uint16(0x0483)
const ExampleProductId = uint16(0xa27e)

func main() {
	// Enumerate over all connected devices
	zerousb, err := zerousb.New(zerousb.Options{
		LogLevel: zerousb.LogLevelDebug,
	}, logrus.New())
	if err != nil {
		panic(err)
	}

	device, err := zerousb.Connect("Aillio Bullet R1", ExampleVendorId, ExampleProductId)
	if err != nil {
		panic(err)
	}

	wrote, err := device.Write([]byte{0x30, 0x02})
	if err != nil {
		panic(err)
	}

	fmt.Printf("Wrote: %v\n", wrote)
	readRes, err := device.Read(32, 0)
	if err != nil {
		panic(err)
	}

	fmt.Printf("Read: %v\n", readRes)

	device.Close(false)
}
