package c2db

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

var (
	testKeyMagic           = []byte("C2DBTEST")
	errKeyMode             = errors.New("c2db: key file mode")
	errKeyInDataDir        = errors.New("c2db: key in data dir")
	errKeyUnmarkedTestdata = errors.New("c2db: unmarked testdata key")
	errKeySize             = errors.New("c2db: key file size")
)

func WriteTestKey(path string, key [32]byte) error {
	var payload [40]byte
	copy(payload[:8], testKeyMagic)
	copy(payload[8:], key[:])
	return writeKeyFile(path, payload[:])
}

func WriteKey(path string, key [32]byte) error {
	return writeKeyFile(path, key[:])
}

func LoadKey(path string) (key [32]byte, test bool, err error) {
	mat, test, err := loadKeyMaterial(path)
	if err != nil {
		return key, false, err
	}
	return mat, test, nil
}

func LoadMLDSASeed(path string) (seed []byte, test bool, err error) {
	mat, test, err := loadKeyMaterial(path)
	if err != nil {
		return nil, false, err
	}
	seed = make([]byte, 32)
	copy(seed, mat[:])
	return seed, test, nil
}

func keyPathForbidden(path string) bool {
	abs, err := absClean(path)
	if err != nil {
		return true
	}
	if pathHasTestdata(abs) {
		return true
	}
	return keyBesideShardImage(abs)
}

func writeKeyFile(path string, payload []byte) error {
	if keyPathForbidden(path) {
		return errKeyInDataDir
	}
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	if err := unix.Fchmod(fd, 0o600); err != nil {
		_ = unix.Close(fd)
		_ = unix.Unlink(path)
		return err
	}
	n, err := unix.Write(fd, payload)
	if err != nil {
		_ = unix.Close(fd)
		_ = unix.Unlink(path)
		return err
	}
	if n != len(payload) {
		_ = unix.Close(fd)
		_ = unix.Unlink(path)
		return errKeySize
	}
	if err := unix.Close(fd); err != nil {
		_ = unix.Unlink(path)
		return err
	}
	return nil
}

func loadKeyMaterial(path string) (key [32]byte, test bool, err error) {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return key, false, err
	}
	if st.Mode&0o777 != 0o600 {
		return key, false, errKeyMode
	}
	abs, err := absClean(path)
	if err != nil {
		return key, false, err
	}
	if keyBesideShardImage(abs) {
		return key, false, errKeyInDataDir
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		return key, false, err
	}
	if pathHasTestdata(abs) {
		if len(buf) != 40 || !bytes.Equal(buf[:8], testKeyMagic) {
			return key, false, errKeyUnmarkedTestdata
		}
		copy(key[:], buf[8:])
		return key, true, nil
	}
	if len(buf) == 40 && bytes.Equal(buf[:8], testKeyMagic) {
		copy(key[:], buf[8:])
		return key, true, nil
	}
	if len(buf) == 32 {
		copy(key[:], buf)
		return key, false, nil
	}
	return key, false, errKeySize
}

func absClean(path string) (string, error) {
	return filepath.Abs(filepath.Clean(path))
}

func pathHasTestdata(abs string) bool {
	for {
		if filepath.Base(abs) == "testdata" {
			return true
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return false
		}
		abs = parent
	}
}

func keyBesideShardImage(abs string) bool {
	switch filepath.Base(abs) {
	case "data.img", "wal.img":
		return true
	}
	_, err := os.Stat(filepath.Join(filepath.Dir(abs), "data.img"))
	return err == nil
}
