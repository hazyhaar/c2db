// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

var (
	errDeviceSize     = errors.New("c2db: size not a positive multiple of LBA")
	errDeviceGrow     = errors.New("c2db: grow smaller than current size")
	ErrDevicePoisoned = errors.New("c2db: device poisoned by previous I/O error")
)

const LBASize = 4096

type Device struct {
	fd       int
	size     uint64
	preadN   atomic.Uint64
	pwriteN  atomic.Uint64
	poisoned atomic.Bool
}

func Create(path string, size uint64) (*Device, error) {
	if size == 0 || size%LBASize != 0 {
		return nil, fmt.Errorf("c2db: size %d not a positive multiple of %d", size, LBASize)
	}
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Ftruncate(fd, int64(size)); err != nil {
		_ = unix.Close(fd)
		_ = unix.Unlink(path)
		return nil, err
	}
	if err := unix.Close(fd); err != nil {
		return nil, err
	}
	return Open(path)
}

func Open(path string) (*Device, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_DIRECT|unix.O_NOATIME|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	if st.Size%LBASize != 0 {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("c2db: size %d not multiple of %d", st.Size, LBASize)
	}
	return &Device{fd: fd, size: uint64(st.Size)}, nil
}

func (d *Device) Write(lba uint64, buf []byte) error {
	if err := d.checkReady(); err != nil {
		return err
	}
	if err := checkXfer(buf); err != nil {
		return err
	}
	off, err := d.xferOff(lba, uint64(len(buf)))
	if err != nil {
		return err
	}
	return d.pwriteAll(buf, off)
}

func (d *Device) Read(lba uint64, buf []byte) error {
	if err := d.checkReady(); err != nil {
		return err
	}
	if err := checkXfer(buf); err != nil {
		return err
	}
	off, err := d.xferOff(lba, uint64(len(buf)))
	if err != nil {
		return err
	}
	return d.preadAll(buf, off)
}

func (d *Device) Size() uint64 {
	if d == nil {
		return 0
	}
	return d.size
}

func (d *Device) Grow(newSize uint64) error {
	if err := d.checkReady(); err != nil {
		return err
	}
	if newSize == 0 || newSize%LBASize != 0 {
		return errDeviceSize
	}
	if newSize < d.size {
		return errDeviceGrow
	}
	if newSize == d.size {
		return nil
	}
	if newSize > uint64(^uint(0)>>1) {
		return errDeviceSize
	}
	if err := unix.Fallocate(d.fd, 0, 0, int64(newSize)); err != nil {
		if err == unix.ENOSPC || err == unix.EOPNOTSUPP || err == unix.EINVAL {
			if truncErr := unix.Ftruncate(d.fd, int64(newSize)); truncErr != nil {
				return truncErr
			}
		} else {
			return err
		}
	}
	d.size = newSize
	return nil
}

func (d *Device) lockOFD(wait bool) error {
	if err := d.checkReady(); err != nil {
		return err
	}
	fl := unix.Flock_t{Type: unix.F_WRLCK, Whence: int16(unix.SEEK_SET), Start: 0, Len: 1}
	cmd := unix.F_OFD_SETLK
	if wait {
		cmd = unix.F_OFD_SETLKW
	}
	return unix.FcntlFlock(uintptr(d.fd), cmd, &fl)
}

func (d *Device) unlockOFD() error {
	if err := d.checkReady(); err != nil {
		return err
	}
	fl := unix.Flock_t{Type: unix.F_UNLCK, Whence: int16(unix.SEEK_SET), Start: 0, Len: 1}
	return unix.FcntlFlock(uintptr(d.fd), unix.F_OFD_SETLK, &fl)
}

func (d *Device) Flush() error {
	if err := d.checkReady(); err != nil {
		return err
	}
	t0 := time.Now()
	err := unix.Fdatasync(d.fd)
	dur := time.Since(t0)
	if err != nil {
		d.poisoned.Store(true)
		probeEmit(0, ProbeOpPoison, 0, 0, 0, dur, "fdatasync error: "+err.Error())
		return err
	}
	probeEmit(0, ProbeOpFdatasync, 0, 0, 0, dur, "fdatasync ok")
	return nil
}

func (d *Device) Close() error {
	if d == nil || d.fd < 0 {
		return unix.EBADF
	}
	err := unix.Close(d.fd)
	d.fd = -1
	return err
}

func (d *Device) PreadN() uint64 {
	if d == nil {
		return 0
	}
	return d.preadN.Load()
}

func (d *Device) PwriteN() uint64 {
	if d == nil {
		return 0
	}
	return d.pwriteN.Load()
}

func (d *Device) IsPoisoned() bool {
	if d == nil {
		return false
	}
	return d.poisoned.Load()
}

func (d *Device) checkReady() error {
	if d == nil || d.fd < 0 {
		return unix.EBADF
	}
	if d.poisoned.Load() {
		return ErrDevicePoisoned
	}
	return nil
}

func checkXfer(buf []byte) error {
	n := len(buf)
	if n == 0 || n%LBASize != 0 {
		return fmt.Errorf("c2db: length %d not a positive multiple of %d", n, LBASize)
	}
	if uintptr(unsafe.Pointer(&buf[0]))&(LBASize-1) != 0 {
		return unix.EINVAL
	}
	return nil
}

func (d *Device) xferOff(lba, nbytes uint64) (int64, error) {
	maxLBA := d.size / LBASize
	nLBA := nbytes / LBASize
	if lba >= maxLBA || nLBA > maxLBA-lba {
		return 0, fmt.Errorf("c2db: lba %d len %d out of range (size %d)", lba, nbytes, d.size)
	}
	return int64(lba * LBASize), nil
}

func (d *Device) pwriteAll(buf []byte, off int64) error {
	for len(buf) > 0 {
		n, err := unix.Pwrite(d.fd, buf, off)
		if n > 0 {
			d.pwriteN.Add(1)
			buf = buf[n:]
			off += int64(n)
		}
		if err != nil {
			d.poisoned.Store(true)
			probeEmit(0, ProbeOpPoison, uint64(off/LBASize), 0, 0, 0, "pwrite error: "+err.Error())
			return err
		}
		if n == 0 {
			d.poisoned.Store(true)
			return fmt.Errorf("c2db: short pwrite")
		}
	}
	return nil
}

func (d *Device) preadAll(buf []byte, off int64) error {
	for len(buf) > 0 {
		n, err := unix.Pread(d.fd, buf, off)
		if n > 0 {
			d.preadN.Add(1)
			buf = buf[n:]
			off += int64(n)
		}
		if err != nil {
			d.poisoned.Store(true)
			probeEmit(0, ProbeOpPoison, uint64(off/LBASize), 0, 0, 0, "pread error: "+err.Error())
			return err
		}
		if n == 0 {
			return fmt.Errorf("c2db: short pread")
		}
	}
	return nil
}
