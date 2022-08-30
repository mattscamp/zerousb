// go:build none

package main

import (
	"fmt"
	"strings"

	"github.com/mattscamp/zerousb"
)

const ExampleVendorId = zerousb.ID(0x0483)
const ExampleProductId = zerousb.ID(0xa27e)

func main() {
	// Enumerate over all connected devices
	devices, err := zerousb.Find(ExampleVendorId, ExampleProductId)
	if err != nil {
		panic(err)
	}
	for i, dvc := range devices {
		fmt.Printf("DVC #%d\n", i)
		fmt.Printf("  OS Path:    %s\n", dvc.Path)
		fmt.Printf("  Vendor ID:  %#04x\n", dvc.VendorID)
		fmt.Printf("  Product ID: %#04x\n", dvc.ProductID)
		fmt.Printf("  Interface:  %d\n", dvc.Interface)
		fmt.Println(strings.Repeat("-", 128))
	}
	if len(devices) < 1 {
		panic("No device found.")
	}
	connectedDevice, err := devices[0].Open()
	if err != nil {
		panic(err)
	}
	connectedDevice.Close()
}
