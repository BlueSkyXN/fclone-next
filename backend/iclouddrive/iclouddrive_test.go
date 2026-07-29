//go:build !plan9 && !solaris

package iclouddrive_test

import (
	"context"
	"errors"
	"testing"

	"github.com/rclone/rclone/backend/iclouddrive"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fstest/fstests"
)

func TestCopyDisabled(t *testing.T) {
	_, err := new(iclouddrive.Fs).Copy(context.Background(), nil, "")
	if !errors.Is(err, fs.ErrorCantCopy) {
		t.Fatalf("got error %v, want %v", err, fs.ErrorCantCopy)
	}
}

// TestIntegration runs integration tests against the remote
func TestIntegration(t *testing.T) {
	fstests.Run(t, &fstests.Opt{
		RemoteName: "TestICloudDrive:",
		NilObject:  (*iclouddrive.Object)(nil),
	})
}
