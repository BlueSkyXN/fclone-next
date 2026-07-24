# Updating the rclone baseline

fclone is a full-source fork because direct Drive IDs, check-first directory
precreation, and progress compatibility require small hooks in rclone's
private backend and sync internals. The compatibility code is kept in
`fclone_*.go` files wherever possible so upstream updates remain reviewable.

## Repository model

- `upstream` points to `https://github.com/rclone/rclone.git` and is fetch-only.
- `master` contains an upstream baseline plus the fclone compatibility commit(s).
- The Go module path remains `github.com/rclone/rclone`; changing it would
  break a large set of internal imports and plugin compatibility.
- The command registry omits `selfupdate` in every build because rclone's
  updater installs an official rclone binary rather than an fclone release.
  Release builds retain `noselfupdate` as an additional build constraint.

## Update procedure

1. Fetch the next stable rclone tag and verify its release notes, Go version,
   and signed/tagged source identity.
2. Create an update branch from `master` and merge the new upstream release tag.
3. Resolve conflicts without renaming `rclone.conf`, `RCLONE_*`, backend names,
   command names, or the Go module path.
4. Re-run the compatibility tests for direct IDs/URLs, Service Account
   discovery and switching, directory level ordering, Shared Drive command
   formatting, and transfer statistics.
5. Run the full upstream unit suite, a race-enabled targeted suite, the six
   release cross-builds, and opt-in Google Drive integration tests.
6. Update the rclone baseline in `README.md`, `NOTICE`, CI, and release notes;
   bump the independent fclone version separately.

Do not cherry-pick the historical 2020 implementation. Its useful observable
behavior is captured by current tests, while its old concurrency model and API
assumptions are intentionally not retained.

## fclone release procedure

1. Merge the reviewed release commit into `master` and read back the exact SHA.
2. Create and push a tag named `fclone-vX.Y.Z` at that `master` commit.
3. Wait for the `fclone` workflow to pass its test, race, vet, build, archive,
   and checksum gates. The workflow rejects non-fclone tags and tags which are
   not ancestors of `origin/master`.
4. The workflow creates a **draft** GitHub Release. Read back the tag SHA, all
   six platform archives, per-archive checksums, combined `SHA256SUMS`, and
   release notes before publishing it.
5. Publish the draft explicitly only after the readback matches the validated
   commit. A tag or successful build by itself is not a published release.

Do not create release tags with the default `GITHUB_TOKEN` in a separate
workflow and assume another workflow will start; GitHub suppresses recursive
workflow triggers from that token. The approved path is a human-authorized tag
push followed by this repository's single tag workflow.

## Deliberate patch surface

The expected upstream-conflict surface is limited to:

- `backend/drive/drive.go`: option registration and small compatibility hooks;
- `backend/drive/upload.go`: operation-scoped resumable upload client;
- `fs/sync/sync.go`: queued-transfer directory collection and the post-check hook;
- `fs/cache/cache.go`: optional backend-private cache identity for direct files;
- `fs/accounting/stats.go`: legacy progress fields;
- `cmd/cmd.go` and `cmd/help.go`: version, single-file naming, and branding;
- build and release metadata.

New logic should stay in adjacent `fclone_*.go` files unless a public rclone
extension point makes an out-of-tree implementation possible.
