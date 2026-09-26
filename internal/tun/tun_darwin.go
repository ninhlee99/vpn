// Package tun opens a macOS utun virtual network interface directly via the
// PF_SYSTEM/SYSPROTO_CONTROL kernel control socket — the same low-level
// mechanism used by WireGuard-go, Tailscale, and other non-App-Store macOS
// VPN clients that run as a plain root process instead of a signed Network
// Extension. It needs root (utun creation is root-only) but no Xcode, no
// codesigning, and no Network Extension entitlement.
package tun

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

const (
	utunControlName = "com.apple.net.utun_control"
	// afInetPrefixLen is the 4-byte protocol-family header macOS prepends
	// to every packet read from, and expects prepended on every write to,
	// a utun device.
	afInetPrefixLen = 4
	utunOptIfname   = 2 // UTUN_OPT_IFNAME, from <net/if_utun.h>
	// sysprotoControl is SYSPROTO_CONTROL from <sys/kern_control.h>. It is
	// not exported by golang.org/x/sys/unix, but its value is a fixed
	// kernel ABI constant, not something macOS versions change.
	sysprotoControl = 2
)

// Device is an open utun interface. Read/Write carry raw IPv4/IPv6 packets;
// the protocol-family framing utun uses internally is handled transparently.
type Device struct {
	fd   int
	Name string

	// Reused across calls: Read used to allocate a fresh buffer the size of
	// the caller's (64 KiB) for every single packet, and Write one per
	// packet too — pure GC churn on a path that runs for every packet.
	rmu  sync.Mutex
	rbuf []byte
	wbuf sync.Pool
}

// Open creates a new utun device, letting the kernel assign the next free
// utunN name (Unit: 0 in the control-socket connect means "pick one").
func Open() (*Device, error) {
	fd, err := unix.Socket(unix.AF_SYSTEM, unix.SOCK_DGRAM, sysprotoControl)
	if err != nil {
		return nil, fmt.Errorf("open PF_SYSTEM socket (need root): %w", err)
	}

	var info unix.CtlInfo
	copy(info.Name[:], utunControlName)
	if err := unix.IoctlCtlInfo(fd, &info); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("CTLIOCGINFO for %s: %w", utunControlName, err)
	}

	sa := &unix.SockaddrCtl{ID: info.Id, Unit: 0}
	if err := unix.Connect(fd, sa); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("connect utun control socket: %w", err)
	}

	name, err := unix.GetsockoptString(fd, sysprotoControl, utunOptIfname)
	if err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("read assigned utun name: %w", err)
	}
	name = string(bytes.TrimRight([]byte(name), "\x00"))

	if err := unix.SetNonblock(fd, false); err != nil {
		unix.Close(fd)
		return nil, err
	}

	// Maximize socket buffer sizes (8MB - macOS kernel maxsockbuf limit) to prevent buffer drops under Gigabit throughput.
	_ = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, 8*1024*1024)
	_ = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_SNDBUF, 8*1024*1024)

	return &Device{fd: fd, Name: name}, nil
}

// Close releases the utun file descriptor; the interface disappears from
// `ifconfig -a` as soon as the last fd referencing it closes.
func (d *Device) Close() error {
	return unix.Close(d.fd)
}

// Read returns one raw IP packet (no protocol-family prefix) into buf,
// which must be large enough for MTU-sized packets plus the 4-byte header.
func (d *Device) Read(buf []byte) (int, error) {
	d.rmu.Lock()
	defer d.rmu.Unlock()
	if need := len(buf) + afInetPrefixLen; cap(d.rbuf) < need {
		d.rbuf = make([]byte, need)
	}
	raw := d.rbuf[:len(buf)+afInetPrefixLen]
	n, err := unix.Read(d.fd, raw)
	if err != nil {
		return 0, err
	}
	if n <= afInetPrefixLen {
		return 0, nil
	}
	copy(buf, raw[afInetPrefixLen:n])
	return n - afInetPrefixLen, nil
}

// Write sends one raw IP packet, prefixing the protocol-family word utun
// requires (AF_INET/AF_INET6, detected from the packet's IP version nibble).
func (d *Device) Write(pkt []byte) (int, error) {
	if len(pkt) == 0 {
		return 0, nil
	}
	var family uint32
	switch pkt[0] >> 4 {
	case 4:
		family = unix.AF_INET
	case 6:
		family = unix.AF_INET6
	default:
		return 0, fmt.Errorf("write: not an IP packet (first nibble %d)", pkt[0]>>4)
	}
	bp, _ := d.wbuf.Get().(*[]byte)
	if bp == nil || cap(*bp) < afInetPrefixLen+len(pkt) {
		b := make([]byte, 0, 2048+len(pkt))
		bp = &b
	}
	raw := (*bp)[:afInetPrefixLen+len(pkt)]
	defer d.wbuf.Put(bp)
	binary.BigEndian.PutUint32(raw[:4], family)
	copy(raw[afInetPrefixLen:], pkt)
	n, err := unix.Write(d.fd, raw)
	if err != nil {
		return 0, err
	}
	if n < afInetPrefixLen {
		return 0, nil
	}
	return n - afInetPrefixLen, nil
}

// File exposes the fd as an *os.File for callers that want to select/poll
// it alongside UDP sockets via a context-cancelable read loop.
func (d *Device) File() *os.File {
	return os.NewFile(uintptr(d.fd), d.Name)
}
