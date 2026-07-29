package drive

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/rclone/rclone/fs"
	gdrive "google.golang.org/api/drive/v3"
)

func TestFcloneAddDriveHonorsDryRun(t *testing.T) {
	ctx, config := fs.AddConfig(context.Background())
	config.DryRun = true
	f := &Fs{}
	handled, out, err := f.fcloneSharedDriveCommand(ctx, "add-drive", []string{"Dry Run Drive"}, nil)
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	result, ok := out.(map[string]any)
	if !ok || result["dry_run"] != true || result["name"] != "Dry Run Drive" {
		t.Fatalf("unexpected dry-run result: %#v", out)
	}
}

func TestFormatFcloneSharedDrives(t *testing.T) {
	drives := []*gdrive.Drive{
		{Id: "drive-b", Name: "Beta"},
		{Id: "drive-a2", Name: "Alpha"},
		nil,
		{Id: "drive-a1", Name: "Alpha"},
	}
	want := []string{
		"drive-a1;Alpha",
		"drive-a2;Alpha",
		"drive-b;Beta",
	}
	got := formatFcloneSharedDrives(drives, ";")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected drive list\ngot:  %#v\nwant: %#v", got, want)
	}
	if drives[0].Id != "drive-b" {
		t.Fatal("formatFcloneSharedDrives changed the caller's input order")
	}
}

func TestParseFcloneListDrivesOptions(t *testing.T) {
	tests := []struct {
		name    string
		arg     []string
		opt     map[string]string
		want    string
		wantErr string
	}{
		{name: "default", want: "\t"},
		{name: "custom", opt: map[string]string{"separator": " | "}, want: " | "},
		{name: "argument", arg: []string{"extra"}, wantErr: "does not take arguments"},
		{name: "empty separator", opt: map[string]string{"separator": ""}, wantErr: "must not be empty"},
		{name: "newline separator", opt: map[string]string{"separator": "\n"}, wantErr: "must not contain a newline"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseFcloneListDrivesOptions(test.arg, test.opt)
			assertFcloneParseResult(t, got, err, test.want, test.wantErr)
		})
	}
}

func TestParseFcloneAddDriveOptions(t *testing.T) {
	tests := []struct {
		name        string
		arg         []string
		opt         map[string]string
		wantName    string
		wantSource  string
		wantReplace bool
		wantErr     string
	}{
		{name: "name only", arg: []string{"Archive"}, wantName: "Archive"},
		{name: "copy", arg: []string{"Archive"}, opt: map[string]string{"copy-members": "source:"}, wantName: "Archive", wantSource: "source:"},
		{name: "replace", arg: []string{"Archive"}, opt: map[string]string{"replace-members": "source:"}, wantName: "Archive", wantSource: "source:", wantReplace: true},
		{name: "no argument", wantErr: "exactly 1 argument"},
		{name: "too many arguments", arg: []string{"one", "two"}, wantErr: "exactly 1 argument"},
		{name: "blank name", arg: []string{"  "}, wantErr: "must not be empty"},
		{name: "both modes", arg: []string{"Archive"}, opt: map[string]string{"copy-members": "one:", "replace-members": "two:"}, wantErr: "mutually exclusive"},
		{name: "empty copy source", arg: []string{"Archive"}, opt: map[string]string{"copy-members": ""}, wantErr: "non-empty Drive remote"},
		{name: "blank replace source", arg: []string{"Archive"}, opt: map[string]string{"replace-members": "  "}, wantErr: "non-empty Drive remote"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseFcloneAddDriveOptions(test.arg, test.opt)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("got error %v, want one containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.name != test.wantName || got.sourceRemote != test.wantSource || got.replaceMember != test.wantReplace {
				t.Fatalf("got %#v, want name=%q source=%q replace=%v", got, test.wantName, test.wantSource, test.wantReplace)
			}
		})
	}
}

func TestParseFcloneDeleteDriveOptions(t *testing.T) {
	force, err := parseFcloneDeleteDriveOptions(nil, nil)
	if err != nil || force {
		t.Fatalf("default: force=%v err=%v", force, err)
	}
	force, err = parseFcloneDeleteDriveOptions(nil, map[string]string{"force": ""})
	if err != nil || !force {
		t.Fatalf("force option: force=%v err=%v", force, err)
	}
	force, err = parseFcloneDeleteDriveOptions(nil, map[string]string{"force": "false"})
	if err != nil || force {
		t.Fatalf("force=false option: force=%v err=%v", force, err)
	}
	_, err = parseFcloneDeleteDriveOptions(nil, map[string]string{"force": "not-a-bool"})
	if err == nil || !strings.Contains(err.Error(), "true or false") {
		t.Fatalf("invalid force option: got error %v", err)
	}
	_, err = parseFcloneDeleteDriveOptions([]string{"extra"}, nil)
	if err == nil || !strings.Contains(err.Error(), "does not take arguments") {
		t.Fatalf("extra argument: got error %v", err)
	}
}

func TestFcloneSharedDriveIDRequiresRoot(t *testing.T) {
	tests := []struct {
		name    string
		fs      Fs
		want    string
		wantErr string
	}{
		{
			name: "shared drive root",
			fs: Fs{
				isTeamDrive:  true,
				opt:          Options{TeamDriveID: "drive-id"},
				rootFolderID: "drive-id",
			},
			want: "drive-id",
		},
		{
			name: "empty shared drive ID",
			fs: Fs{
				isTeamDrive: true,
			},
			wantErr: "Shared Drive ID is empty",
		},
		{
			name: "remote subdirectory",
			fs: Fs{
				isTeamDrive:  true,
				opt:          Options{TeamDriveID: "drive-id"},
				rootFolderID: "drive-id",
				root:         "subdir",
			},
			wantErr: "not a subdirectory",
		},
		{
			name: "direct ID subfolder",
			fs: Fs{
				isTeamDrive:  true,
				opt:          Options{TeamDriveID: "drive-id"},
				rootFolderID: "folder-id",
			},
			wantErr: "folder inside a Shared Drive",
		},
		{
			name: "not shared drive",
			fs: Fs{
				rootFolderID: "folder-id",
			},
			wantErr: "does not target a Shared Drive",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.fs.fcloneSharedDriveID(true)
			assertFcloneParseResult(t, got, err, test.want, test.wantErr)
		})
	}
}

func TestFcloneCloneablePermission(t *testing.T) {
	tests := []struct {
		name       string
		permission *gdrive.Permission
		wantReason string
	}{
		{
			name:       "direct user",
			permission: &gdrive.Permission{Type: "user", Role: "writer", EmailAddress: "member@example.com"},
		},
		{
			name:       "owner",
			permission: &gdrive.Permission{Type: "user", Role: "owner", EmailAddress: "owner@example.com"},
			wantReason: "owner permission",
		},
		{
			name: "inherited",
			permission: &gdrive.Permission{
				Type:              "group",
				Role:              "reader",
				EmailAddress:      "group@example.com",
				PermissionDetails: []*gdrive.PermissionPermissionDetails{{Inherited: true}},
			},
			wantReason: "inherited permission",
		},
		{
			name:       "missing email",
			permission: &gdrive.Permission{Type: "user", Role: "reader"},
			wantReason: "member email is unavailable",
		},
		{
			name:       "domain is not a member",
			permission: &gdrive.Permission{Type: "domain", Role: "reader", Domain: "example.com"},
			wantReason: "Shared Drive members must be users or groups",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, reason := fcloneCloneablePermission(test.permission)
			if reason != test.wantReason {
				t.Fatalf("got reason %q, want %q", reason, test.wantReason)
			}
			if (got == nil) != (test.wantReason != "") {
				t.Fatalf("permission=%#v reason=%q", got, reason)
			}
		})
	}
}

func assertFcloneParseResult(t *testing.T, got string, err error, want, wantErr string) {
	t.Helper()
	if wantErr != "" {
		if err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Fatalf("got value %q and error %v, want error containing %q", got, err, wantErr)
		}
		return
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
