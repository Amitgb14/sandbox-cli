//go:build !unix

package image

import "errors"

const noFollow = 0

var errInvalid = errors.New("invalid argument")
