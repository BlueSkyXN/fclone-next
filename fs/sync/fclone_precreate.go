package sync

import (
	"context"
	"path"
	"sort"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/lib/transform"
)

// fcloneDirectoryPrecreator is intentionally optional and backend-neutral.
// Drive implements it; other backends retain upstream rclone behavior.
type fcloneDirectoryPrecreator interface {
	FclonePrecreateDirectories(ctx context.Context, directories []string, workers int) (int, error)
}

// fclonePrecreateDirectories reproduces historical fclone's --check-first
// optimization: after checking, create missing non-empty destination
// directories before any file transfer starts.
func (s *syncCopyMove) fclonePrecreateDirectories(ctx context.Context) {
	if !s.fclonePrecreateEnabled || ctx.Err() != nil {
		return
	}
	precreator, ok := s.fdst.(fcloneDirectoryPrecreator)
	if !ok {
		return
	}

	s.fcloneTransferDirsMu.Lock()
	directories := make([]string, 0, len(s.fcloneTransferDirs))
	for destination := range s.fcloneTransferDirs {
		directories = append(directories, destination)
	}
	s.fcloneTransferDirsMu.Unlock()

	if len(directories) == 0 {
		return
	}
	sort.Strings(directories)
	if s.ci.DryRun {
		fs.Infof(s.fdst, "fclone: would pre-create %d directories after checks (dry-run)", len(directories))
		return
	}

	created, err := precreator.FclonePrecreateDirectories(ctx, directories, s.ci.Transfers)
	if err != nil {
		fs.Errorf(s.fdst, "fclone: pre-creating directories failed; transfers will use normal lazy creation: %v", err)
		return
	}
	fs.Infof(s.fdst, "fclone: pre-created %d directories after checks", created)
}

// fclonePutTransfer records the directories required by a transfer only after
// the transfer has entered the queue. Rename-only and move-delete work never
// reaches this helper.
func (s *syncCopyMove) fclonePutTransfer(out *pipe, pair fs.ObjectPair) bool {
	ok := out.Put(s.inCtx, pair)
	if !ok || !s.fclonePrecreateEnabled || pair.Src == nil || pair.Src == pair.Dst {
		return ok
	}

	directory := path.Dir(transform.Path(s.ctx, pair.Src.Remote(), true))
	s.fcloneTransferDirsMu.Lock()
	for directory != "." && directory != "" && directory != "/" {
		s.fcloneTransferDirs[directory] = struct{}{}
		directory = path.Dir(directory)
	}
	s.fcloneTransferDirsMu.Unlock()
	return ok
}
