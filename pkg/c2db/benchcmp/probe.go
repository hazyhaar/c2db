// SPDX-License-Identifier: Apache-2.0 OR MIT

package benchcmp

import (
	"bufio"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

type KernelProbe struct {
	UserNs     int64  `json:"user_ns"`
	SysNs      int64  `json:"sys_ns"`
	MaxRSSKB   int64  `json:"max_rss_kb"`
	MinFlt     int64  `json:"min_flt"`
	MajFlt     int64  `json:"maj_flt"`
	Nvcsw      int64  `json:"nvcsw"`
	Nivcsw     int64  `json:"nivcsw"`
	SyscRead   uint64 `json:"sysc_read"`
	SyscWrite  uint64 `json:"sysc_write"`
	ReadBytes  uint64 `json:"read_bytes"`
	WriteBytes uint64 `json:"write_bytes"`
}

func snapProbe() KernelProbe {
	var ru unix.Rusage
	_ = unix.Getrusage(unix.RUSAGE_SELF, &ru)
	p := KernelProbe{
		UserNs:   ru.Utime.Nano(),
		SysNs:    ru.Stime.Nano(),
		MaxRSSKB: ru.Maxrss,
		MinFlt:   ru.Minflt,
		MajFlt:   ru.Majflt,
		Nvcsw:    ru.Nvcsw,
		Nivcsw:   ru.Nivcsw,
	}
	io := readProcIO()
	p.SyscRead = io["syscr"]
	p.SyscWrite = io["syscw"]
	p.ReadBytes = io["read_bytes"]
	p.WriteBytes = io["write_bytes"]
	return p
}

func (a KernelProbe) Sub(b KernelProbe) KernelProbe {
	return KernelProbe{
		UserNs:     a.UserNs - b.UserNs,
		SysNs:      a.SysNs - b.SysNs,
		MaxRSSKB:   a.MaxRSSKB,
		MinFlt:     a.MinFlt - b.MinFlt,
		MajFlt:     a.MajFlt - b.MajFlt,
		Nvcsw:      a.Nvcsw - b.Nvcsw,
		Nivcsw:     a.Nivcsw - b.Nivcsw,
		SyscRead:   a.SyscRead - b.SyscRead,
		SyscWrite:  a.SyscWrite - b.SyscWrite,
		ReadBytes:  a.ReadBytes - b.ReadBytes,
		WriteBytes: a.WriteBytes - b.WriteBytes,
	}
}

func readProcIO() map[string]uint64 {
	out := map[string]uint64{}
	f, err := os.Open("/proc/self/io")
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		if err != nil {
			continue
		}
		out[strings.TrimSpace(k)] = n
	}
	return out
}
