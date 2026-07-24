package drive

import (
	"context"
	"errors"
	"path"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/rclone/rclone/fs"
	"golang.org/x/sync/errgroup"
)

// FclonePrecreateDirectories creates directories parent-first and runs
// siblings concurrently. It is called by sync only after --check-first has
// drained all checker work.
func (f *Fs) FclonePrecreateDirectories(ctx context.Context, directories []string, workers int) (int, error) {
	levels := fcloneDirectoryLevels(directories)
	if workers < 1 {
		workers = 1
	}

	var created atomic.Int64
	var errsMu sync.Mutex
	var errs []error
	for _, directoriesAtLevel := range levels {
		group, groupCtx := errgroup.WithContext(ctx)
		group.SetLimit(workers)
		for _, directory := range directoriesAtLevel {
			directory := directory
			group.Go(func() error {
				parent, leaf := path.Split(directory)
				parent = strings.Trim(parent, "/")
				parentID, ok := f.dirCache.Get(parent)
				if !ok {
					var err error
					parentID, err = f.dirCache.FindDir(groupCtx, parent, false)
					if err != nil {
						errsMu.Lock()
						errs = append(errs, err)
						errsMu.Unlock()
						fs.Errorf(fs.LogDirName(f, directory), "fclone: couldn't find parent during directory pre-creation: %v", err)
						return nil
					}
				}

				newID, found, err := f.FindLeaf(groupCtx, parentID, leaf)
				if err == nil && !found {
					newID, err = f.CreateDir(groupCtx, parentID, leaf)
				}
				if err != nil {
					errsMu.Lock()
					errs = append(errs, err)
					errsMu.Unlock()
					fs.Errorf(fs.LogDirName(f, directory), "fclone: failed to pre-create directory: %v", err)
				} else {
					f.dirCache.Put(directory, newID)
					if !found {
						created.Add(1)
					}
				}
				return nil // Try all siblings; normal transfer creation remains the fallback.
			})
		}
		_ = group.Wait()
		if err := ctx.Err(); err != nil {
			return int(created.Load()), err
		}
	}
	return int(created.Load()), errors.Join(errs...)
}

func fcloneDirectoryLevels(directories []string) [][]string {
	unique := make(map[string]struct{}, len(directories))
	maxLevel := 0
	for _, directory := range directories {
		directory = strings.Trim(path.Clean(directory), "/")
		if directory == "" || directory == "." {
			continue
		}
		unique[directory] = struct{}{}
		level := strings.Count(directory, "/")
		if level > maxLevel {
			maxLevel = level
		}
	}
	levels := make([][]string, maxLevel+1)
	for directory := range unique {
		level := strings.Count(directory, "/")
		levels[level] = append(levels[level], directory)
	}
	for _, level := range levels {
		sort.Strings(level)
	}
	return levels
}
