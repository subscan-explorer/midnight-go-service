//go:build darwin

package compile

import "syscall"

func procAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
