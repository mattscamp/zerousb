// go:build none

package main

import (
	"log"
	"time"

	"github.com/mattscamp/zerousb"
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

	log.Println("Connect or disconnect any USB device...")
	zerousb.Watch([][]uint16{
		{ExampleVendorId, ExampleProductId},
		{ExampleVendorId, uint16(0xa4cd)},
	})

	for range time.Tick(time.Second * 1) {
		_, _ = zerousb.Get()
	}

	zerousb.EndWatch()
}
