package zerousb

import (
	"os"
	"runtime"
	"sync"
	"testing"
)

// Tests that generic enumeration can be called concurrently from multiple threads.
func TestThreadedFind(t *testing.T) {
	// Travis does not have usbfs enabled in the Linux kernel
	if os.Getenv("TRAVIS") != "" && runtime.GOOS == "linux" {
		t.Skip("Linux on Travis doesn't have usbfs, skipping test")
	}
	var pend sync.WaitGroup
	for i := 0; i < 8; i++ {
		pend.Add(1)

		go func(index int) {
			defer pend.Done()
			for j := 0; j < 512; j++ {
				if _, err := Find(ID(index), 0); err != nil {
					t.Errorf("thread %d, iter %d: failed to enumerate: %v", index, j, err)
				}
			}
		}(i)
	}
	pend.Wait()
}
