// go:build none

package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mattscamp/zerousb"
	"github.com/sirupsen/logrus"
)

const ExampleVendorId = uint16(0x0483)
const ExampleProductId = uint16(0xa27e)

// Payload types for the two-part request/response protocol
const (
	PAYLOAD_PART_A = iota
	PAYLOAD_PART_B
)

func main() {
	// Setup logging with detailed output
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
		ForceColors:   true,
	})

	fmt.Println("=== ZeroUSB Fault Tolerance Diagnostic Tool ===")
	fmt.Println("Protocol: Two-part request/response (Payload A & B)")
	fmt.Println()

	// Initialize zerousb
	zusbInstance, err := zerousb.New(zerousb.Options{
		LogLevel: zerousb.LogLevelDebug,
	}, logger)
	if err != nil {
		logger.Fatalf("Failed to initialize zerousb: %v", err)
	}
	defer zusbInstance.Close()

	// Interactive menu
	scanner := bufio.NewScanner(os.Stdin)

	for {
		printMenu()
		fmt.Print("Enter your choice: ")

		if !scanner.Scan() {
			break
		}

		choice := strings.TrimSpace(scanner.Text())

		switch choice {
		case "1":
			testDeviceConnection(zusbInstance, logger)
		case "2":
			testFaultTolerance(zusbInstance, logger)
		case "3":
			stressTest(zusbInstance, logger)
		case "4":
			showDeviceInfo(zusbInstance, logger)
		case "q", "Q":
			fmt.Println("Exiting...")
			return
		default:
			fmt.Println("Invalid choice. Please try again.")
		}

		fmt.Println()
	}
}

func printMenu() {
	fmt.Println("=== Diagnostic Menu ===")
	fmt.Println("1. Test Device Connection")
	fmt.Println("2. Test Fault Tolerance")
	fmt.Println("3. Stress Test")
	fmt.Println("4. Show Device Information")
	fmt.Println("Q. Quit")
	fmt.Println()
}

func getPayloadBytes(payloadType int) []byte {
	switch payloadType {
	case PAYLOAD_PART_A:
		return []byte{0x30, 0x01}
	case PAYLOAD_PART_B:
		return []byte{0x30, 0x03}
	default:
		return []byte{0x30, 0x01}
	}
}

func performTwoPartCommunication(device *zerousb.ZeroUSBDevice, logger *logrus.Logger) ([]byte, []byte, error) {
	var partAData, partBData []byte

	// Request Payload Part A
	logger.Debug("Requesting Payload Part A...")
	payloadARequest := getPayloadBytes(PAYLOAD_PART_A)

	wrote, err := device.Write(payloadARequest)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to write Payload A request: %v", err)
	}
	logger.Debugf("Sent Payload A request: %d bytes %v", wrote, payloadARequest)

	// Read Payload Part A response
	partAData, err = device.Read(64, 1000)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read Payload A response: %v", err)
	}
	logger.Debugf("Received Payload A: %d bytes", len(partAData))

	time.Sleep(10 * time.Millisecond)

	// Request Payload Part B
	logger.Debug("Requesting Payload Part B...")
	payloadBRequest := getPayloadBytes(PAYLOAD_PART_B)

	wrote, err = device.Write(payloadBRequest)
	if err != nil {
		return partAData, nil, fmt.Errorf("failed to write Payload B request: %v", err)
	}
	logger.Debugf("Sent Payload B request: %d bytes %v", wrote, payloadBRequest)

	// Read Payload Part B response
	partBData, err = device.Read(64, 1000)
	if err != nil {
		return partAData, nil, fmt.Errorf("failed to read Payload B response: %v", err)
	}
	logger.Debugf("Received Payload B: %d bytes", len(partBData))

	return partAData, partBData, nil
}

func testDeviceConnection(zusbInstance *zerousb.ZeroUSB, logger *logrus.Logger) {
	fmt.Println("=== Testing Device Connection ===")

	// Try direct connection
	device, err := zusbInstance.Connect(nil, ExampleVendorId, ExampleProductId)
	if err != nil {
		logger.Errorf("Direct connection failed: %v", err)
		fmt.Println("❌ Direct connection failed")
		return
	}
	defer device.Close(false)

	fmt.Println("✅ Direct connection successful")

	// Test two-part protocol
	partA, partB, err := performTwoPartCommunication(device, logger)
	if err != nil {
		logger.Errorf("Two-part communication failed: %v", err)
		fmt.Printf("❌ Protocol test failed: %v\n", err)
	} else {
		fmt.Printf("✅ Protocol test successful:\n")
		fmt.Printf("  - Payload A: %d bytes\n", len(partA))
		fmt.Printf("  - Payload B: %d bytes\n", len(partB))
	}
}

func testFaultTolerance(zusbInstance *zerousb.ZeroUSB, logger *logrus.Logger) {
	fmt.Println("=== Testing Fault Tolerance ===")
	fmt.Println("This test will start watching for devices.")
	fmt.Println("Please plug/unplug your device during this test to verify fault tolerance.")
	fmt.Println("Press Enter to start, then Enter again to stop...")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan() // Wait for user to press Enter

	// Start watching
	vendorProducts := []zerousb.VendorAndProduct{
		{
			VendorId:   ExampleVendorId,
			ProductId:  ExampleProductId,
			Identifier: &[]string{"TestDevice"}[0],
		},
	}

	err := zusbInstance.Watch(vendorProducts)
	if err != nil {
		logger.Errorf("Failed to start watching: %v", err)
		return
	}

	// Monitor in background
	stopChan := make(chan bool)
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				return
			case <-ticker.C:
				device, err := zusbInstance.Get()
				if err != nil {
					fmt.Printf("⚠️  No device: %v\n", err)
					continue
				}

				// Check device health
				state := device.GetState()
				errorCount := device.GetErrorCount()
				healthy := device.IsHealthy()

				fmt.Printf("📊 Device Status - State: %v, Errors: %d, Healthy: %v\n",
					state, errorCount, healthy)

				// Attempt two-part communication
				partA, partB, err := performTwoPartCommunication(device, logger)
				if err != nil {
					fmt.Printf("❌ Protocol failed: %v\n", err)

					// Check if we should attempt recovery
					if !healthy && state == zerousb.DeviceStateError {
						fmt.Println("🔧 Attempting automatic recovery...")
						if recoveryErr := device.RecoverDevice(); recoveryErr != nil {
							fmt.Printf("❌ Recovery failed: %v\n", recoveryErr)
						} else {
							fmt.Println("✅ Recovery successful")
						}
					}
				} else {
					fmt.Printf("✅ Protocol success: A=%d bytes, B=%d bytes\n", len(partA), len(partB))
				}
			}
		}
	}()

	// Wait for user to stop
	scanner.Scan()
	stopChan <- true
	zusbInstance.EndWatch()

	fmt.Println("✅ Fault tolerance test completed")
}

func monitorDeviceHealth(zusbInstance *zerousb.ZeroUSB, logger *logrus.Logger) {
	fmt.Println("=== Monitoring Device Health ===")
	fmt.Println("Press Enter to start monitoring, Enter again to stop...")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()

	// Connect to device
	device, err := zusbInstance.Connect(nil, ExampleVendorId, ExampleProductId)
	if err != nil {
		logger.Errorf("Connection failed: %v", err)
		return
	}
	defer device.Close(false)

	stopChan := make(chan bool)
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		cycleCount := 0
		for {
			select {
			case <-stopChan:
				return
			case <-ticker.C:
				cycleCount++
				state := device.GetState()
				errorCount := device.GetErrorCount()
				healthy := device.IsHealthy()
				lastError, lastErrorTime := device.GetLastError()

				fmt.Printf("[%s] Cycle %d - State: %v | Errors: %d | Healthy: %v",
					time.Now().Format("15:04:05"), cycleCount, state, errorCount, healthy)

				if lastError != nil {
					fmt.Printf(" | Last Error: %v (at %s)",
						lastError, lastErrorTime.Format("15:04:05"))
				}
				fmt.Println()

				// Test protocol every cycle
				partA, partB, err := performTwoPartCommunication(device, logger)
				if err != nil {
					fmt.Printf("  ❌ Communication failed: %v\n", err)
				} else {
					fmt.Printf("  ✅ Communication OK: A=%d, B=%d bytes\n", len(partA), len(partB))
				}

				// Trigger recovery if needed
				if !healthy && state == zerousb.DeviceStateError {
					fmt.Println("  🔧 Triggering automatic recovery...")
					if recoveryErr := device.RecoverDevice(); recoveryErr != nil {
						fmt.Printf("  ❌ Recovery failed: %v\n", recoveryErr)
					} else {
						fmt.Println("  ✅ Recovery successful")
					}
				}
			}
		}
	}()

	scanner.Scan()
	stopChan <- true

	fmt.Println("✅ Health monitoring completed")
}

func stressTest(zusbInstance *zerousb.ZeroUSB, logger *logrus.Logger) {
	fmt.Println("=== Stress Test ===")
	fmt.Print("Enter number of protocol cycles (default 50): ")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	iterationsStr := strings.TrimSpace(scanner.Text())

	iterations := 50
	if iterationsStr != "" {
		if i, err := strconv.Atoi(iterationsStr); err == nil && i > 0 {
			iterations = i
		}
	}

	device, err := zusbInstance.Connect(nil, ExampleVendorId, ExampleProductId)
	if err != nil {
		logger.Errorf("Connection failed: %v", err)
		return
	}
	defer device.Close(false)

	fmt.Printf("Running %d protocol cycles...\n", iterations)

	successfulCycles := 0
	partialSuccesses := 0
	errors := 0

	startTime := time.Now()

	for i := 0; i < iterations; i++ {
		partA, partB, err := performTwoPartCommunication(device, logger)

		if err != nil {
			errors++
			if zerousb.IsErrorDisconnect(err) {
				fmt.Printf("❌ Cycle %d: Device disconnected\n", i+1)
				break
			}
		} else if len(partA) > 0 && len(partB) > 0 {
			successfulCycles++
		} else {
			partialSuccesses++
		}

		if (i+1)%10 == 0 {
			fmt.Printf("Progress: %d/%d (Success: %d, Partial: %d, Errors: %d)\n",
				i+1, iterations, successfulCycles, partialSuccesses, errors)
		}

		// Very small delay to avoid overwhelming the device
		time.Sleep(50 * time.Millisecond)
	}

	duration := time.Since(startTime)

	fmt.Println("\n=== Stress Test Results ===")
	fmt.Printf("Duration: %v\n", duration)
	fmt.Printf("Successful Cycles: %d/%d (%.1f%%)\n", successfulCycles, iterations, float64(successfulCycles)/float64(iterations)*100)
	fmt.Printf("Partial Successes: %d/%d (%.1f%%)\n", partialSuccesses, iterations, float64(partialSuccesses)/float64(iterations)*100)
	fmt.Printf("Total Errors: %d (%.1f%%)\n", errors, float64(errors)/float64(iterations)*100)
	fmt.Printf("Average cycle time: %v\n", duration/time.Duration(iterations))

	// Show device health after stress test
	fmt.Printf("\nFinal Device Status:\n")
	fmt.Printf("  Error Count: %d\n", device.GetErrorCount())
	fmt.Printf("  State: %v\n", device.GetState())
	fmt.Printf("  Healthy: %v\n", device.IsHealthy())
}

func showDeviceInfo(zusbInstance *zerousb.ZeroUSB, logger *logrus.Logger) {
	fmt.Println("=== Device Information ===")

	device, err := zusbInstance.Connect(nil, ExampleVendorId, ExampleProductId)
	if err != nil {
		logger.Errorf("Connection failed: %v", err)
		return
	}
	defer device.Close(false)

	fmt.Printf("Device Identifier: %v\n", device.GetIdentifier())
	fmt.Printf("Current State: %v\n", device.GetState())
	fmt.Printf("Error Count: %d\n", device.GetErrorCount())
	fmt.Printf("Is Healthy: %v\n", device.IsHealthy())

	lastError, lastErrorTime := device.GetLastError()
	if lastError != nil {
		fmt.Printf("Last Error: %v\n", lastError)
		fmt.Printf("Last Error Time: %v\n", lastErrorTime)
	} else {
		fmt.Println("Last Error: None")
	}

	fmt.Printf("Vendor ID: 0x%04X\n", ExampleVendorId)
	fmt.Printf("Product ID: 0x%04X\n", ExampleProductId)

	// Test the protocol to show it's working
	fmt.Println("\nTesting current protocol...")
	partA, partB, err := performTwoPartCommunication(device, logger)
	if err != nil {
		fmt.Printf("❌ Protocol test failed: %v\n", err)
	} else {
		fmt.Printf("✅ Protocol working: A=%d bytes, B=%d bytes\n", len(partA), len(partB))
		fmt.Printf("Payload A preview: %v\n", partA[:min(16, len(partA))])
		fmt.Printf("Payload B preview: %v\n", partB[:min(16, len(partB))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
