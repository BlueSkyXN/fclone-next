package sync

import (
	"context"
	"errors"
	"testing"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fstest/mockobject"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fclonePrecreateTestFs struct {
	fs.Fs
	calls       int
	directories []string
	workers     int
	err         error
}

func TestFclonePutTransferRecordsQueuedParents(t *testing.T) {
	ctx, _ := fs.AddConfig(context.Background())
	out, err := newPipe("", func(int, int64) {}, -1)
	require.NoError(t, err)
	s := &syncCopyMove{
		ctx:                    ctx,
		inCtx:                  ctx,
		fclonePrecreateEnabled: true,
		fcloneTransferDirs:     make(map[string]struct{}),
	}
	src := mockobject.New("parent/child/file.txt")
	require.True(t, s.fclonePutTransfer(out, fs.ObjectPair{Src: src}))
	assert.Equal(t, map[string]struct{}{
		"parent":       {},
		"parent/child": {},
	}, s.fcloneTransferDirs)

	// src == dst is a move-delete queue item, not a transfer.
	require.True(t, s.fclonePutTransfer(out, fs.ObjectPair{Src: src, Dst: src}))
	assert.Len(t, s.fcloneTransferDirs, 2)
}

func (f *fclonePrecreateTestFs) String() string { return "precreate test fs" }

func (f *fclonePrecreateTestFs) FclonePrecreateDirectories(_ context.Context, directories []string, workers int) (int, error) {
	f.calls++
	f.directories = append([]string(nil), directories...)
	f.workers = workers
	return len(directories), f.err
}

func TestFclonePrecreateDirectorySelection(t *testing.T) {
	destination := &fclonePrecreateTestFs{}
	s := &syncCopyMove{
		fdst:                   destination,
		ci:                     &fs.ConfigInfo{Transfers: 7},
		fclonePrecreateEnabled: true,
		fcloneTransferDirs: map[string]struct{}{
			"z/nonempty": {},
			"z":          {},
		},
	}

	s.fclonePrecreateDirectories(context.Background())
	require.Equal(t, 1, destination.calls)
	assert.Equal(t, []string{"z", "z/nonempty"}, destination.directories)
	assert.Equal(t, 7, destination.workers)
}

func TestFclonePrecreateSkipsCancelledAndDryRun(t *testing.T) {
	destination := &fclonePrecreateTestFs{}
	s := &syncCopyMove{
		fdst:                   destination,
		ci:                     &fs.ConfigInfo{DryRun: true, Transfers: 1},
		fclonePrecreateEnabled: true,
		fcloneTransferDirs:     map[string]struct{}{"directory": {}},
	}
	s.fclonePrecreateDirectories(context.Background())
	assert.Zero(t, destination.calls)

	s.ci.DryRun = false
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.fclonePrecreateDirectories(ctx)
	assert.Zero(t, destination.calls)
}

func TestFclonePrecreateFailureIsBestEffort(t *testing.T) {
	destination := &fclonePrecreateTestFs{err: errors.New("precreate failed")}
	s := &syncCopyMove{
		fdst:                   destination,
		ci:                     &fs.ConfigInfo{Transfers: 1},
		fclonePrecreateEnabled: true,
		fcloneTransferDirs:     map[string]struct{}{"directory": {}},
	}
	s.fclonePrecreateDirectories(context.Background())
	assert.Equal(t, 1, destination.calls)
	assert.NoError(t, s.currentError(), "lazy transfer fallback must determine final command status")
}
