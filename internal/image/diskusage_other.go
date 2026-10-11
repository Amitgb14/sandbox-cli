//go:build !unix

package image

import "io/fs"

func diskUsage(fi fs.FileInfo) int64 { return fi.Size() }
