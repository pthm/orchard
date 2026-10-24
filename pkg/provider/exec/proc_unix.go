//go:build !windows

package exec

import (
	osexec "os/exec"
	"syscall"
)

// killGroupOnCancel runs cmd in its own process group and makes
// cancellation kill the whole group, so a grandchild holding stdout
// dies with the shell instead of keeping Run waiting. The own group
// also keeps a terminal Ctrl-C from reaching the child directly;
// orchard cancels it through ctx instead.
func killGroupOnCancel(cmd *osexec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
