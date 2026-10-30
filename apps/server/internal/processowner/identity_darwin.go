//go:build darwin

package processowner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Darwin's libproc ABI, from Apple's bsd/sys/proc_info_private.h. The kernel
// validates exact sizes; an unsupported API fails closed. No entitlement or
// root permission is requested. Resource coalition ownership survives setsid.
const (
	pidInfo       = 2
	uniqueInfo    = 17
	coalitionInfo = 20
	signalAudit   = 0x11
)

type unique struct {
	UUID                    [16]byte
	ID, Parent              uint64
	Version, OriginalParent uint32
	Reserved                [2]uint64
}

func procInfo(call, pid, flavor int, out unsafe.Pointer, size uintptr) error {
	n, _, errno := unix.Syscall6(unix.SYS_PROC_INFO, uintptr(call), uintptr(pid), uintptr(flavor), 0, uintptr(out), size)
	if errno != 0 {
		return errno
	}
	if call == pidInfo && n != size {
		return fmt.Errorf("libproc returned %d of %d bytes: %w", n, size, ErrUnavailable)
	}
	return nil
}

func Observe(pid int) (Identity, error) {
	var id unique
	if pid <= 1 {
		return Identity{}, ErrUnavailable
	}
	if err := procInfo(pidInfo, pid, uniqueInfo, unsafe.Pointer(&id), unsafe.Sizeof(id)); err != nil {
		return Identity{}, err
	}
	var coalitions [5]uint64 // two coalition IDs and three reserved words
	if err := procInfo(pidInfo, pid, coalitionInfo, unsafe.Pointer(&coalitions), unsafe.Sizeof(coalitions)); err != nil {
		return Identity{}, err
	}
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil || boot == "" || id.ID == 0 || id.Version == 0 || coalitions[0] == 0 {
		return Identity{}, errors.Join(ErrUnavailable, err)
	}
	return Identity{PID: pid, UniqueID: id.ID, Version: id.Version, Coalition: coalitions[0], Boot: boot}, nil
}

// Members excludes zombies: they cannot write and are reaped by their real
// parent/launchd. Every unreadable live member makes cleanup unconfirmed.
func Members(root Identity) ([]Identity, error) {
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil || root.Boot == "" || boot != root.Boot || root.Coalition == 0 {
		return nil, errors.Join(ErrUnavailable, err)
	}
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	var result []Identity
	for _, proc := range processes {
		pid := int(proc.Proc.P_pid)
		if pid <= 1 || proc.Proc.P_stat == 5 {
			continue
		}
		var coalitions [5]uint64
		err := procInfo(pidInfo, pid, coalitionInfo, unsafe.Pointer(&coalitions), unsafe.Sizeof(coalitions))
		if errors.Is(err, unix.ESRCH) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if coalitions[0] != root.Coalition {
			continue
		}
		observed, err := Observe(pid)
		if errors.Is(err, unix.ESRCH) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if observed.Coalition == root.Coalition {
			result = append(result, observed)
		}
	}
	return result, nil
}

// Signal uses the kernel's audit-token signal operation. XNU resolves the
// PID/version to an exact proc identity and revalidates it while signalling;
// a stale version is ESRCH, including the lookup-to-signal race. Ordinary
// same-user and MAC checks still apply; zero token credentials grant nothing.
func Signal(id Identity, signal int) error {
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil || boot != id.Boot || id.PID <= 1 || id.Version == 0 {
		return errors.Join(ErrUnavailable, err)
	}
	token := [8]uint32{5: uint32(id.PID), 7: id.Version}
	err = procInfo(signalAudit, 0, signal, unsafe.Pointer(&token), unsafe.Sizeof(token))
	runtime.KeepAlive(token)
	return err
}

func terminate(ctx context.Context, o Owner) error {
	self, err := Observe(os.Getpid())
	if err != nil {
		return err
	}
	if o.Root.Coalition == 0 || o.Root.Boot != self.Boot {
		return ErrUnavailable
	}
	// A helper may terminate descendants of its own job, but only when it is
	// the exact trusted root. A host manager must be in a different coalition.
	inside := self.Coalition == o.Root.Coalition
	if inside && (self.UniqueID != o.Root.UniqueID || self.PID != o.Root.PID) {
		return ErrUnavailable
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		members, err := Members(o.Root)
		if err != nil {
			return err
		}
		pending := 0
		for _, member := range members {
			if inside && member.UniqueID == self.UniqueID {
				continue
			}
			pending++
			if err := Signal(member, int(unix.SIGSTOP)); err != nil && !errors.Is(err, unix.ESRCH) {
				return err
			}
		}
		if pending == 0 {
			return nil
		}
		// Frozen members cannot fork. Any fork that raced the first snapshot
		// remains in the same kernel coalition and is caught on the next pass.
		for _, member := range members {
			if inside && member.UniqueID == self.UniqueID {
				continue
			}
			if err := Signal(member, int(unix.SIGKILL)); err != nil && !errors.Is(err, unix.ESRCH) {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
