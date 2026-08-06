package render

import "golang.org/x/sys/unix"

// The termios ioctl for querying a tty differs between platforms.
const ioctlReadTermios = unix.TIOCGETA

const isDarwin = true
