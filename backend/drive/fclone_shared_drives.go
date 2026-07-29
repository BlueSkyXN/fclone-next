package drive

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/cache"
	"github.com/rclone/rclone/fs/config"
	"github.com/rclone/rclone/fs/operations"
	gdrive "google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
)

const fcloneDefaultDriveListSeparator = "\t"

var fcloneSharedDriveCommandHelp = []fs.CommandHelp{{
	Name:  "add-drive",
	Short: "Create a Shared Drive, optionally copying its members.",
	Long: `This compatibility command creates a Google Shared Drive.

Optionally, copy direct, reusable member permissions from another Drive remote.
Permissions which Google does not allow clients to recreate, such as inherited
or owner permissions, are skipped. Individual permission failures are logged
and do not remove the newly created Shared Drive.

Usage examples:

` + "```console" + `
fclone backend add-drive drive: "New Drive"
fclone backend add-drive drive: "New Drive" -o copy-members=source-drive:
fclone backend add-drive drive: "New Drive" -o replace-members=source-drive:
` + "```",
	Opts: map[string]string{
		"copy-members":    "Copy direct members and their roles from this Drive remote.",
		"replace-members": "Copy members, then remove other removable direct members from the new drive.",
	},
}, {
	Name:  "delete-drive",
	Short: "Permanently delete the targeted Shared Drive.",
	Long: `This compatibility command permanently deletes the Shared Drive targeted
by the remote. The remote must point at the root of a configured Shared Drive
or at a Shared Drive selected with fclone's direct-ID syntax.

Confirmation is required by default. The force option skips that confirmation;
--dry-run is still honored. Google refuses to delete a drive containing
untrashed items unless an administrator uses separate API facilities.

Usage examples:

` + "```console" + `
fclone backend delete-drive shared-drive:
fclone backend delete-drive 'drive:{0123ABCDEF-6Uk9PVA}'
fclone backend delete-drive shared-drive: -o force
` + "```",
	Opts: map[string]string{
		"force": "Delete without the command's confirmation prompt.",
	},
}, {
	Name:  "lsdrives",
	Short: "List Shared Drives in the legacy ID<separator>Name format.",
	Long: `This compatibility command returns one line per visible Shared Drive,
sorted by name and then ID. The default separator is a tab.

Usage examples:

` + "```console" + `
fclone backend lsdrives drive:
fclone backend lsdrives drive: -o separator=","
` + "```",
	Opts: map[string]string{
		"separator": `Separator between ID and name (default "[TAB]").`,
	},
}}

type fcloneAddDriveOptions struct {
	name          string
	sourceRemote  string
	replaceMember bool
}

func parseFcloneAddDriveOptions(arg []string, opt map[string]string) (fcloneAddDriveOptions, error) {
	var parsed fcloneAddDriveOptions
	if len(arg) != 1 {
		return parsed, errors.New("add-drive needs exactly 1 argument: the new Shared Drive name")
	}
	if strings.TrimSpace(arg[0]) == "" {
		return parsed, errors.New("add-drive name must not be empty")
	}
	parsed.name = arg[0]

	copyRemote, copyMembers := opt["copy-members"]
	replaceRemote, replaceMembers := opt["replace-members"]
	if copyMembers && replaceMembers {
		return parsed, errors.New("copy-members and replace-members are mutually exclusive")
	}
	if copyMembers {
		if strings.TrimSpace(copyRemote) == "" {
			return parsed, errors.New("copy-members needs a non-empty Drive remote")
		}
		parsed.sourceRemote = copyRemote
	}
	if replaceMembers {
		if strings.TrimSpace(replaceRemote) == "" {
			return parsed, errors.New("replace-members needs a non-empty Drive remote")
		}
		parsed.sourceRemote = replaceRemote
		parsed.replaceMember = true
	}
	return parsed, nil
}

func parseFcloneListDrivesOptions(arg []string, opt map[string]string) (string, error) {
	if len(arg) != 0 {
		return "", errors.New("lsdrives does not take arguments")
	}
	separator := fcloneDefaultDriveListSeparator
	if value, ok := opt["separator"]; ok {
		separator = value
	}
	if separator == "" {
		return "", errors.New("separator must not be empty")
	}
	if strings.ContainsAny(separator, "\r\n") {
		return "", errors.New("separator must not contain a newline")
	}
	return separator, nil
}

func parseFcloneDeleteDriveOptions(arg []string, opt map[string]string) (force bool, err error) {
	if len(arg) != 0 {
		return false, errors.New("delete-drive does not take arguments")
	}
	value, set := opt["force"]
	if !set {
		return false, nil
	}
	if value == "" {
		return true, nil
	}
	force, err = strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("force must be true or false: %w", err)
	}
	return force, nil
}

func formatFcloneSharedDrives(drives []*gdrive.Drive, separator string) []string {
	nonNil := make([]*gdrive.Drive, 0, len(drives))
	for _, sharedDrive := range drives {
		if sharedDrive != nil {
			nonNil = append(nonNil, sharedDrive)
		}
	}
	drives = nonNil
	sort.SliceStable(drives, func(i, j int) bool {
		if drives[i].Name != drives[j].Name {
			return drives[i].Name < drives[j].Name
		}
		return drives[i].Id < drives[j].Id
	})
	lines := make([]string, 0, len(drives))
	for _, sharedDrive := range drives {
		lines = append(lines, sharedDrive.Id+separator+sharedDrive.Name)
	}
	return lines
}

func (f *Fs) fcloneSharedDriveCommand(ctx context.Context, name string, arg []string, opt map[string]string) (handled bool, out any, err error) {
	switch name {
	case "lsdrives":
		separator, err := parseFcloneListDrivesOptions(arg, opt)
		if err != nil {
			return true, nil, err
		}
		drives, err := f.listTeamDrives(ctx)
		if err != nil {
			return true, nil, err
		}
		return true, formatFcloneSharedDrives(drives, separator), nil

	case "add-drive":
		parsed, err := parseFcloneAddDriveOptions(arg, opt)
		if err != nil {
			return true, nil, err
		}
		var sourcePermissions []*gdrive.Permission
		if parsed.sourceRemote != "" {
			sourceFs, err := fcloneSharedDriveRemote(ctx, parsed.sourceRemote)
			if err != nil {
				return true, nil, err
			}
			sourceID, err := sourceFs.fcloneSharedDriveID(false)
			if err != nil {
				return true, nil, fmt.Errorf("member source: %w", err)
			}
			sourceLease, err := sourceFs.fcloneNewServiceLease(ctx)
			if err != nil {
				return true, nil, err
			}
			sourcePermissions, err = sourceFs.fcloneListDrivePermissions(ctx, sourceLease, sourceID)
			if err != nil {
				return true, nil, fmt.Errorf("couldn't list source Shared Drive members: %w", err)
			}
			if parsed.replaceMember && len(fcloneCloneablePermissions(sourcePermissions)) == 0 {
				return true, nil, errors.New("replace-members source has no directly copyable members; refusing to create an inaccessible drive")
			}
		}
		if fs.GetConfig(ctx).DryRun {
			fs.Logf(f, "Would create Shared Drive %q (dry-run)", parsed.name)
			return true, map[string]any{
				"dry_run":             true,
				"name":                parsed.name,
				"permissions_to_copy": len(fcloneCloneablePermissions(sourcePermissions)),
			}, nil
		}
		lease, err := f.fcloneNewServiceLease(ctx)
		if err != nil {
			return true, nil, err
		}
		newDrive, err := f.fcloneCreateSharedDrive(ctx, lease, parsed.name)
		if err != nil {
			return true, nil, err
		}
		permissionResult := fclonePermissionCopyResult{}
		if len(sourcePermissions) != 0 {
			permissionResult = f.fcloneCopyDrivePermissions(ctx, lease, newDrive, sourcePermissions, parsed.replaceMember)
		}
		return true, map[string]any{
			"id":                         newDrive.Id,
			"name":                       newDrive.Name,
			"permissions_copied":         permissionResult.Copied,
			"permissions_skipped":        permissionResult.Skipped,
			"permissions_failed":         permissionResult.Failed,
			"permissions_removed":        permissionResult.Removed,
			"permission_removals_failed": permissionResult.RemoveFailed,
		}, nil

	case "delete-drive":
		force, err := parseFcloneDeleteDriveOptions(arg, opt)
		if err != nil {
			return true, nil, err
		}
		return true, nil, f.fcloneDeleteSharedDrive(ctx, force)
	}
	return false, nil, nil
}

func fcloneSharedDriveRemote(ctx context.Context, remote string) (*Fs, error) {
	target, err := cache.Get(ctx, remote)
	if err != nil {
		return nil, fmt.Errorf("couldn't open member source remote %q: %w", remote, err)
	}
	driveFs, ok := target.(*Fs)
	if !ok {
		return nil, fmt.Errorf("member source %q is not a Google Drive backend", remote)
	}
	if !driveFs.isTeamDrive {
		return nil, fmt.Errorf("member source %q does not target a Shared Drive", remote)
	}
	return driveFs, nil
}

func (f *Fs) fcloneSharedDriveID(requireRoot bool) (string, error) {
	if !f.isTeamDrive {
		return "", errors.New("remote does not target a Shared Drive")
	}
	id := f.opt.TeamDriveID
	if id == "" && f.rootFolderID != "" {
		// Direct-ID roots identify the Shared Drive at run time rather than in
		// the persisted remote options.
		id = f.rootFolderID
	}
	if id == "" {
		return "", errors.New("shared drive ID is empty")
	}
	if requireRoot {
		if f.root != "" {
			return "", errors.New("delete-drive must target the Shared Drive root, not a subdirectory")
		}
		if f.opt.RootFolderID != "" && f.opt.RootFolderID != id {
			return "", errors.New("delete-drive cannot be used with a subdirectory root_folder_id")
		}
		if f.rootFolderID != "" && actualID(f.rootFolderID) != id {
			return "", errors.New("delete-drive direct-ID target is a folder inside a Shared Drive, not the Shared Drive root")
		}
	}
	return id, nil
}

func (f *Fs) fcloneCreateSharedDrive(ctx context.Context, lease *fcloneServiceLease, name string) (newDrive *gdrive.Drive, err error) {
	err = f.pacer.Call(func() (bool, error) {
		newDrive, err = lease.service.Drives.Create(uuid.NewString(), &gdrive.Drive{Name: name}).
			Fields("id,name,createdTime").
			Context(ctx).
			Do()
		return f.shouldRetryLease(ctx, err, lease)
	})
	if err != nil {
		return nil, fmt.Errorf("couldn't create Shared Drive %q: %w", name, err)
	}
	fs.Logf(f, "Created Shared Drive %q (%s)", newDrive.Name, newDrive.Id)
	return newDrive, nil
}

func (f *Fs) fcloneListDrivePermissions(ctx context.Context, lease *fcloneServiceLease, driveID string) (permissions []*gdrive.Permission, err error) {
	pageToken := ""
	for {
		var page *gdrive.PermissionList
		err = f.pacer.Call(func() (bool, error) {
			call := lease.service.Permissions.List(driveID).
				PageSize(100).
				SupportsAllDrives(true).
				Fields(googleapi.Field("nextPageToken,permissions(id,type,role,emailAddress,domain,allowFileDiscovery,expirationTime,deleted,pendingOwner,permissionDetails)"))
			if pageToken != "" {
				call.PageToken(pageToken)
			}
			page, err = call.Context(ctx).Do()
			return f.shouldRetryLeaseWithToken(ctx, err, lease, pageToken != "")
		})
		if err != nil {
			return nil, fmt.Errorf("listing permissions for Shared Drive %q failed: %w", driveID, err)
		}
		permissions = append(permissions, page.Permissions...)
		if page.NextPageToken == "" {
			return permissions, nil
		}
		pageToken = page.NextPageToken
	}
}

func fcloneCloneablePermissions(permissions []*gdrive.Permission) []*gdrive.Permission {
	cloneable := make([]*gdrive.Permission, 0, len(permissions))
	for _, permission := range permissions {
		if cloned, _ := fcloneCloneablePermission(permission); cloned != nil {
			cloneable = append(cloneable, cloned)
		}
	}
	return cloneable
}

func fcloneCloneablePermission(permission *gdrive.Permission) (*gdrive.Permission, string) {
	if permission == nil {
		return nil, "empty permission"
	}
	if permission.Deleted {
		return nil, "deleted member"
	}
	if permission.PendingOwner || permission.Role == "owner" {
		return nil, "owner permission"
	}
	for _, detail := range permission.PermissionDetails {
		if detail != nil && detail.Inherited {
			return nil, "inherited permission"
		}
	}
	allowedRoles := map[string]bool{
		"organizer":     true,
		"fileOrganizer": true,
		"writer":        true,
		"commenter":     true,
		"reader":        true,
	}
	if !allowedRoles[permission.Role] {
		return nil, "unsupported role"
	}
	cloned := &gdrive.Permission{
		Type:               permission.Type,
		Role:               permission.Role,
		EmailAddress:       permission.EmailAddress,
		Domain:             permission.Domain,
		AllowFileDiscovery: permission.AllowFileDiscovery,
		ExpirationTime:     permission.ExpirationTime,
	}
	switch permission.Type {
	case "user", "group":
		if permission.EmailAddress == "" {
			return nil, "member email is unavailable"
		}
	case "domain", "anyone":
		return nil, "Shared Drive members must be users or groups"
	default:
		return nil, "unsupported permission type"
	}
	return cloned, ""
}

func fclonePermissionKey(permission *gdrive.Permission) string {
	if permission == nil {
		return ""
	}
	switch permission.Type {
	case "user", "group":
		return permission.Type + ":" + strings.ToLower(permission.EmailAddress)
	case "domain":
		return permission.Type + ":" + strings.ToLower(permission.Domain)
	case "anyone":
		return permission.Type
	default:
		return ""
	}
}

type fclonePermissionCopyResult struct {
	Copied       int
	Skipped      int
	Failed       int
	Removed      int
	RemoveFailed int
}

func (f *Fs) fcloneCopyDrivePermissions(ctx context.Context, lease *fcloneServiceLease, newDrive *gdrive.Drive, sourcePermissions []*gdrive.Permission, replace bool) (result fclonePermissionCopyResult) {
	desired := make(map[string]struct{})
	for _, source := range sourcePermissions {
		permission, reason := fcloneCloneablePermission(source)
		if permission == nil {
			result.Skipped++
			fs.Infof(f, "Skipping %s while copying Shared Drive member %q", reason, sourcePermissionLabel(source))
			continue
		}
		desired[fclonePermissionKey(permission)] = struct{}{}
		var created *gdrive.Permission
		var err error
		err = f.pacer.Call(func() (bool, error) {
			created, err = lease.service.Permissions.Create(newDrive.Id, permission).
				SupportsAllDrives(true).
				SendNotificationEmail(false).
				Fields("id,type,role,emailAddress,domain").
				Context(ctx).
				Do()
			return f.shouldRetryLease(ctx, err, lease)
		})
		if err != nil {
			result.Failed++
			fs.Errorf(f, "Couldn't copy Shared Drive member %q: %v", sourcePermissionLabel(source), err)
			continue
		}
		result.Copied++
		fs.Infof(f, "Copied Shared Drive member %q with role %q", sourcePermissionLabel(created), created.Role)
	}

	if !replace {
		return result
	}
	current, err := f.fcloneListDrivePermissions(ctx, lease, newDrive.Id)
	if err != nil {
		result.Failed++
		fs.Errorf(f, "Couldn't list new Shared Drive members for replacement: %v", err)
		return result
	}
	for _, permission := range current {
		// Never remove an existing Shared Drive manager automatically. This
		// prevents replace-members from locking the caller out if Google omits
		// an identity field or a source permission couldn't be recreated.
		if permission != nil && permission.Role == "organizer" {
			fs.Infof(f, "Keeping Shared Drive manager %q while replacing members", sourcePermissionLabel(permission))
			continue
		}
		_, reason := fcloneCloneablePermission(permission)
		if reason != "" {
			fs.Infof(f, "Keeping %s %q while replacing Shared Drive members", reason, sourcePermissionLabel(permission))
			continue
		}
		if _, keep := desired[fclonePermissionKey(permission)]; keep {
			continue
		}
		if permission.Id == "" {
			fs.Infof(f, "Keeping Shared Drive member %q because its permission ID is unavailable", sourcePermissionLabel(permission))
			continue
		}
		err = f.pacer.Call(func() (bool, error) {
			err = lease.service.Permissions.Delete(newDrive.Id, permission.Id).
				SupportsAllDrives(true).
				Context(ctx).
				Do()
			return f.shouldRetryLease(ctx, err, lease)
		})
		if err != nil {
			result.RemoveFailed++
			fs.Errorf(f, "Couldn't remove Shared Drive member %q: %v", sourcePermissionLabel(permission), err)
			continue
		}
		result.Removed++
		fs.Infof(f, "Removed Shared Drive member %q", sourcePermissionLabel(permission))
	}
	return result
}

func sourcePermissionLabel(permission *gdrive.Permission) string {
	if permission == nil {
		return "<unknown>"
	}
	if permission.EmailAddress != "" {
		return permission.EmailAddress
	}
	if permission.Domain != "" {
		return permission.Domain
	}
	if permission.Type != "" {
		return permission.Type
	}
	return "<unknown>"
}

func (f *Fs) fcloneDeleteSharedDrive(ctx context.Context, force bool) (err error) {
	driveID, err := f.fcloneSharedDriveID(true)
	if err != nil {
		return err
	}
	lease, err := f.fcloneNewServiceLease(ctx)
	if err != nil {
		return err
	}
	var target *gdrive.Drive
	err = f.pacer.Call(func() (bool, error) {
		target, err = lease.service.Drives.Get(driveID).
			Fields("id,name,createdTime").
			Context(ctx).
			Do()
		return f.shouldRetryLease(ctx, err, lease)
	})
	if err != nil {
		return fmt.Errorf("couldn't fetch Shared Drive %q: %w", driveID, err)
	}

	if fs.GetConfig(ctx).DryRun {
		fs.Logf(f, "Skipped deleting Shared Drive %q (%s) because --dry-run is set", target.Name, target.Id)
		return nil
	}
	if !force {
		operations.SyncPrintf("Permanently delete Shared Drive?\n")
		operations.SyncPrintf("Name    : %s\n", target.Name)
		operations.SyncPrintf("ID      : %s\n", target.Id)
		if target.CreatedTime != "" {
			operations.SyncPrintf("Created : %s\n", target.CreatedTime)
		}
		if !config.Confirm(false) {
			fs.Infof(f, "Shared Drive deletion cancelled")
			return nil
		}
	}

	err = f.pacer.Call(func() (bool, error) {
		err = lease.service.Drives.Delete(driveID).Context(ctx).Do()
		return f.shouldRetryLease(ctx, err, lease)
	})
	if err != nil {
		return fmt.Errorf("couldn't delete Shared Drive %q: %w", driveID, err)
	}
	fs.Infof(f, "Deleted Shared Drive %q (%s)", target.Name, target.Id)
	return nil
}
