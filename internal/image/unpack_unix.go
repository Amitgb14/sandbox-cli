//go:build unix

package image

import "syscall"

const noFollow = syscall.O_NOFOLLOW
