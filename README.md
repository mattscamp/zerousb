[![Travis][travisimg]][travisurl]
[![AppVeyor][appveyorimg]][appveyorurl]
[![GoDoc][docimg]][docurl]

# zerousb

The `zerousb` package is the simplest wrapper around the native `libusb` library.

To use `zerousb`, no extra setup is required as the package bundles and links libusb.

The package supports Linux, macOS, Windows and FreeBSD.

## Cross-compiling

Using `go get`, the embedded C library is compiled into the binary format of your host OS.

## Acknowledgements

This library is based on and heavily uses code from the [`usb`](https://github.com/karalabe/usb) package by karalabe.

Error handling for the `libusb` integration originates from the [`gousb`](https://github.com/google/gousb) library.

## License

This USB library is licensed under the [GNU Lesser General Public License v3.0](https://www.gnu.org/licenses/lgpl-3.0.en.html) (dictated by libusb).
