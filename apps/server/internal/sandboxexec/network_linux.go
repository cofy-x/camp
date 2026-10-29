//go:build linux

package sandboxexec

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// restrictCommandNetwork blocks IP sockets in the command supervisor and every
// child. LayerFS's authenticated daemon remains reachable to the trusted host,
// but model commands cannot connect to or flood its container-local TCP port.
// The first runtime supports Linux amd64 only; compatibility ABIs are killed
// rather than letting socketcall or x32 syscall numbers bypass the filter.
func restrictCommandNetwork() error {
	if runtime.GOARCH != "amd64" {
		return problem("unsupported_architecture", "Command supervision currently requires Linux x86_64.")
	}
	filters := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 4},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: unix.AUDIT_ARCH_X86_64, Jt: 1},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_KILL_PROCESS},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0},
		{Code: unix.BPF_JMP | unix.BPF_JGE | unix.BPF_K, K: 0x40000000, Jf: 1},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_KILL_PROCESS},
		// io_uring can create sockets without another socket(2) syscall. No
		// ring descriptor is inherited, and new rings are prohibited.
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: unix.SYS_IO_URING_SETUP, Jt: 5},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: unix.SYS_SOCKET, Jf: 3},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 16},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: unix.AF_INET, Jt: 2},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: unix.AF_INET6, Jt: 1},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)},
	}
	program := unix.SockFprog{Len: uint16(len(filters)), Filter: &filters[0]}
	// no_new_privs is per-thread. Hold the thread until TSYNC has installed the
	// filter and propagated the restriction to every existing Go runtime thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return problem("network_isolation_unavailable", "Could not disable command privilege gain.")
	}
	result, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, unix.SECCOMP_FILTER_FLAG_TSYNC, uintptr(unsafe.Pointer(&program)))
	runtime.KeepAlive(filters)
	if errno != 0 || result != 0 {
		return problem("network_isolation_unavailable", "Could not restrict command network access across all supervisor threads.")
	}
	return nil
}
