# fclone compatibility reference

## Scope

fclone is based on the rclone v1.75.0 development line at upstream commit
`bd4c6571e` and restores selected behavior from the
historical fclone/gclone family as a new implementation. The goal is practical
configuration and command compatibility, not bug-for-bug reproduction of an
old rclone release.

Unless this document says otherwise, the embedded rclone core behavior and documentation
apply. In particular, fclone keeps the standard `rclone.conf` format,
`RCLONE_*` environment variables, remote syntax, exit codes, and config/cache
locations.

## Compatibility matrix

| Historical behavior | Embedded rclone core | fclone behavior |
|---|---|---|
| `fclone` binary and version | Reports only the rclone version | Reports the fclone version and embedded rclone-core version separately |
| Service Account directory pool | Supports one `service_account_file` per Drive remote | Restores directory discovery, preload limits, round-robin service use, and quota-triggered rotation |
| `remote:{ID}` and `{Drive URL}` | `root_folder_id` can configure a fixed root; `copyid` copies an object by ID | Restores opt-in inline file, folder, and Shared Drive addressing |
| `--check-first` directory batching | Completes checks before transfers but does not promise the historical directory-precreation phase | For Google Drive destinations, pre-creates required non-empty destination directories after checks and before transfers |
| `backend lsdrives` | `backend drives` returns structured JSON or generated config | Keeps `drives` and adds the historical parse-friendly `lsdrives` form |
| `backend add-drive` | No equivalent command | Restores Shared Drive creation and optional member copying |
| `backend delete-drive` | No equivalent command | Restores deletion of the selected Shared Drive, with confirmation unless forced |
| Copy by Drive object ID | `backend copyid` and `backend moveid` are available | Uses the upstream commands unchanged |
| File rate and file-count ETA | Reports byte rate, byte ETA, and item counts | Adds Files/s and an ETA based on the known file total |
| `selfupdate` | Downloads and installs official rclone | Not registered; fclone must be updated with fclone release artifacts |

## Google Drive Service Account pool

The following historical options are accepted on a Drive remote:

```ini
[drive]
type = drive
service_account_file_path = /path/to/service-accounts
service_account_min_sleep = 100ms
services_preload = 50
services_max = 100
```

- `service_account_file_path` is a directory containing JSON credential files.
- Files are discovered deterministically by filename. Non-JSON files and
  subdirectories are ignored.
- `services_preload` controls how many clients (including the primary client)
  are prepared at startup. Values 0 and 1 both keep only the required primary
  client ready. The value is capped by `services_max`.
- `services_max` is an in-memory client-cache bound, not a limit on the JSON
  files that can be used. Automatic rotation continues through the directory
  and evicts an inactive cached client when necessary.
- The primary identity remains selectable after cache eviction, including
  when `services_max = 1` or its credential file is outside the scanned directory.
- `service_account_min_sleep` limits repeated quota-triggered rotation of one
  operation or the main service. Independent quota-failed operations may each
  leave an exhausted account without blocking one another.
- If no explicit `service_account_file`, credential JSON, or environment
  authentication is configured, the first discovered JSON file becomes the
  initial account.
- High-volume Drive metadata and upload operations lease preloaded accounts in
  round-robin order. Before a paginated operation receives its first page
  token, a quota error may switch the lease. After a token is issued, retries
  stay on the same identity because Google does not guarantee that page tokens
  are portable between accounts.
- Small uploads use one lease for the request. A resumable upload also keeps
  one identity for its session; if a quota response causes rotation between
  chunks, fclone abandons that session and asks the high-level transfer loop to
  restart the file with a fresh session.

The credential directory is scanned when the Drive Fs is created. Recreate or
flush the cached Fs after adding or removing JSON files. `services_max` bounds
cached account slots; an already-running lease may temporarily retain an
evicted transport until that operation completes. `backend set` changes the
main client and subsequent leases, but deliberately does not rewrite an
already-running paginated operation, notification stream, or upload session.

The pool does not combine permissions or storage namespaces. Every credential
must independently have access to the files and Shared Drives used by the
remote. In particular, do not mix unrelated Service Accounts with different
My Drive namespaces in one pool.

## Direct Drive ID and URL paths

Inline Drive addressing is deliberately enclosed in braces so ordinary rclone
paths are unchanged:

```console
fclone lsf 'drive:{DRIVE_OBJECT_ID}'
fclone lsf 'drive:{DRIVE_FOLDER_ID}/relative/path'
fclone lsf 'drive:{https://drive.google.com/drive/folders/DRIVE_FOLDER_ID}'
fclone copy 'drive:{https://drive.google.com/file/d/DRIVE_FILE_ID/view}' ./download
fclone copy 'drive:{https://docs.google.com/document/d/DOCUMENT_ID/edit}' ./download
```

The referenced object is inspected at startup:

- a folder becomes the root of the remote;
- a Shared Drive ID selects that Shared Drive;
- a file behaves as a single-file source and retains its actual Drive name.

The configured account still needs permission to access the referenced ID.
Malformed brace expressions fail instead of being treated as literal paths.
Modern Google Drive URLs containing `resourcekey=` retain that key for the
initial lookup and subsequent child requests. The configured `resource_key`
option is also honored during the initial direct-ID lookup.

Canonicalized direct roots preserve a required resource key as
`{ID?resourcekey=URL_ESCAPED_KEY}` so cache eviction and reconstruction do not
drop access to link-shared objects.

For a fixed root used repeatedly, the upstream `root_folder_id` or
`team_drive` configuration remains the simpler choice. To copy or move known
file IDs into another location, prefer the upstream commands:

```console
fclone backend copyid drive: FILE_ID destination/path
fclone backend moveid drive: FILE_ID destination/path
```

## Shared Drive backend commands

### List Shared Drives

Use the upstream structured command for JSON or generated config:

```console
fclone backend drives drive:
fclone backend -o config drives drive:
```

Use the compatibility command for line-oriented output sorted by Drive name:

```console
fclone backend lsdrives drive:
fclone backend lsdrives drive: -o separator=','
```

The default output format is `ID<TAB>Name`, matching historical fclone. Set
`separator` explicitly when a script expects another delimiter.

### Create a Shared Drive

```console
fclone backend add-drive drive: NewDriveName
fclone backend add-drive drive: NewDriveName -o copy-members=source-drive:
fclone backend add-drive drive: NewDriveName -o replace-members=source-drive:
```

`copy-members` adds members and roles from the selected source Shared Drive.
`replace-members` also removes members of the new Drive that were not present
on the source where the Google API permits it, but always preserves existing
managers to avoid locking out the caller. Only direct user and group members
are copied. The authenticated principal must be allowed to create Shared
Drives and manage permissions. `--dry-run` validates options and performs no
creation or permission changes.

### Delete a Shared Drive

Select the Shared Drive as the remote root, then invoke the command:

```console
fclone backend delete-drive 'drive:{SHARED_DRIVE_ID}'
fclone backend delete-drive 'drive:{SHARED_DRIVE_ID}' -o force
```

Without `force`, interactive confirmation is required. Google Drive refuses
to delete a Shared Drive that still contains untrashed items.

## `--check-first` directory behavior

The embedded rclone core already performs the comparison phase before starting
transfers when `--check-first` is set. Historical fclone additionally collected
the destination directories needed by scheduled files and created those
directories as a batch before transfer workers started. fclone restores that
additional phase when the destination is Google Drive. Other destination
backends retain upstream rclone's behavior.

Only directories required by files actually queued for transfer are pre-created.
Rename workers finish before this phase, so successful server-side renames do
not race directory creation or add unnecessary directories.
This does not make empty source directories appear at the destination; use
`--create-empty-src-dirs` when that is required. The normal rclone warning also
applies: `--check-first` can use substantially more memory because the full
transfer backlog is retained.

Precreation runs parent-first and creates siblings concurrently. It is
best-effort: a precreation error is logged, then normal lazy directory creation
during transfer gets the final say. A cancelled check context skips the phase
entirely so a graceful max-duration stop cannot create directories by itself.

## Transfer statistics

When the total number of files is known, fclone augments the normal progress
display with:

- completed files per second; and
- an ETA calculated from remaining files and the observed file rate.

The byte-based speed and ETA remain authoritative for byte transfer progress.
File ETA is unavailable until there is enough activity to calculate a rate,
and it can be misleading when file sizes vary widely.

## Migrating an existing installation

1. Back up the existing `rclone.conf` and any service-account directory.
2. Install the fclone binary without replacing the existing rclone binary.
3. Confirm both versions with `fclone version`.
4. Point fclone at the existing config explicitly for the first test.
5. Run a read-only listing, followed by the intended write command with
   `--dry-run`.
6. Update scheduled jobs from `rclone` or an old `fclone` path only after the
   test output is understood.

Example:

```console
fclone --config /path/to/rclone.conf config file
fclone --config /path/to/rclone.conf lsd drive:
fclone --config /path/to/rclone.conf sync --dry-run source: destination:
```

The `selfupdate` command is absent because official rclone release archives do
not contain the fclone compatibility code. `version --check` remains a
successful read-only command for script compatibility, but reports that online
rclone release comparison is disabled and prints the installed versions.

## Build and runtime compatibility

- Minimum supported Go version: Go 1.25.
- Recommended and release Go version: Go 1.26.5.
- `selfupdate` is absent even from a default `go build`; `make fclone` also
  passes the `noselfupdate` tag as a defensive build constraint.
- Add `cmount` only when the target platform's FUSE development dependencies
  are installed.
- Go dynamic plugins must be compiled against the exact fclone source and Go
  toolchain. Go plugin loading is not available on Windows.

The project intentionally retains rclone's module path so the upstream source
tree and internal imports continue to build consistently. The executable and
release artifacts are named `fclone`.
