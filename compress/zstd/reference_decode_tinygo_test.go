//go:build tinygo

package zstd

import "testing"

// Reference CLI interoperability is covered by the host-Go test because
// TinyGo does not implement os/exec. Golden frame and hash checks still run.
func assertReferenceDecode(_ *testing.T, _, _ []byte) {}

func assertReferenceAgrees(_ *testing.T, _ string, _ []byte, _ bool) {}
