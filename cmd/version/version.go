// Package version provides the version command.
package version

import (
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/coreos/go-semver/semver"
	"github.com/rclone/rclone/cmd"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/flags"
	"github.com/rclone/rclone/fs/fshttp"
	"github.com/spf13/cobra"
)

var (
	check = false
	deps  = false
)

func init() {
	cmd.Root.AddCommand(commandDefinition)
	cmdFlags := commandDefinition.Flags()
	flags.BoolVarP(cmdFlags, &check, "check", "", false, "Report that upstream rclone version checks are unavailable", "")
	flags.BoolVarP(cmdFlags, &deps, "deps", "", false, "Show the Go dependencies", "")
}

var commandDefinition = &cobra.Command{
	Use:   "version",
	Short: `Show the version number.`,
	Long: `Show the fclone version, embedded rclone-core version, go version, build target
OS and architecture, the runtime OS and kernel version and bitness,
build tags and the type of executable (static or dynamic).

For example:

` + "```console" + `
$ fclone version
fclone v0.1.0
- rclone v1.75.0-DEV
- os/version: ubuntu 18.04 (64 bit)
- os/kernel: 4.15.0-136-generic (x86_64)
- os/type: linux
- os/arch: amd64
- go/version: go1.16
- go/linking: static
- go/tags: none
` + "```" + `

Note: before rclone version 1.55 the os/type and os/arch lines were merged,
      and the "go/version" line was tagged as "go version".

The --check flag is unavailable because official rclone downloads do not
contain the fclone compatibility layer. Use this project's GitHub Releases.

If you supply the --deps flag then fclone will print its embedded Go
dependencies and their versions along with some other
information about the build.`,
	Annotations: map[string]string{
		"versionIntroduced": "v1.33",
	},
	RunE: func(command *cobra.Command, args []string) error {
		cmd.CheckArgs(0, 0, command, args)
		if deps {
			return printDependencies()
		}
		if check {
			fmt.Println("fclone: online checks against official rclone releases are disabled; use https://github.com/BlueSkyXN/fclone-next/releases")
			cmd.ShowVersion()
		} else {
			cmd.ShowVersion()
		}
		return nil
	},
}

// strip a leading v off the string
func stripV(s string) string {
	if len(s) > 0 && s[0] == 'v' {
		return s[1:]
	}
	return s
}

// GetVersion gets an upstream rclone version for the unregistered selfupdate package.
func GetVersion(ctx context.Context, url string) (v *semver.Version, vs string, date time.Time, err error) {
	resp, err := fshttp.NewClient(ctx).Get(url)
	if err != nil {
		return v, vs, date, err
	}
	defer fs.CheckClose(resp.Body, &err)
	if resp.StatusCode != http.StatusOK {
		return v, vs, date, errors.New(resp.Status)
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return v, vs, date, err
	}
	vs = strings.TrimSpace(string(bodyBytes))
	vs = strings.TrimPrefix(vs, "rclone ")
	vs = strings.TrimRight(vs, "β")
	date, err = http.ParseTime(resp.Header.Get("Last-Modified"))
	if err != nil {
		return v, vs, date, err
	}
	v, err = semver.NewVersion(stripV(vs))
	return v, vs, date, err
}

// CheckVersion supports the unregistered upstream selfupdate package.
func CheckVersion(ctx context.Context) {
	vCurrent, err := semver.NewVersion(stripV(fs.Version))
	if err != nil {
		fs.Errorf(nil, "Failed to parse version: %v", err)
	}
	const timeFormat = "2006-01-02"

	printVersion := func(what, url string) {
		v, vs, t, err := GetVersion(ctx, url+"version.txt")
		if err != nil {
			fs.Errorf(nil, "Failed to get rclone %s version: %v", what, err)
			return
		}
		fmt.Printf("%-8s%-40v %20s\n",
			what+":",
			v,
			"(released "+t.Format(timeFormat)+")",
		)
		if v.Compare(*vCurrent) > 0 {
			fmt.Printf("  upgrade: %s\n", url+vs)
		}
	}
	fmt.Printf("yours:  %-13s\n", vCurrent)
	printVersion(
		"latest",
		"https://downloads.rclone.org/",
	)
	printVersion(
		"beta",
		"https://beta.rclone.org/",
	)
	if strings.HasSuffix(fs.Version, "-DEV") {
		fmt.Println("Your version is compiled from git so comparisons may be wrong.")
	}
}

// Print info about a build module
func printModule(module *debug.Module) {
	if module.Replace != nil {
		fmt.Printf("- %s %s (replaced by %s %s)\n",
			module.Path, module.Version, module.Replace.Path, module.Replace.Version)
	} else {
		fmt.Printf("- %s %s\n", module.Path, module.Version)
	}
}

// printDependencies shows the packages we use in a format like go.mod
func printDependencies() error {
	info, err := buildinfo.ReadFile(os.Args[0])
	if err != nil {
		return fmt.Errorf("error reading build info: %w", err)
	}
	fmt.Println("Go Version:")
	fmt.Printf("- %s\n", info.GoVersion)
	fmt.Println("Main package:")
	printModule(&info.Main)
	fmt.Println("Binary path:")
	fmt.Printf("- %s\n", info.Path)
	fmt.Println("Settings:")
	for _, setting := range info.Settings {
		fmt.Printf("- %s: %s\n", setting.Key, setting.Value)
	}
	fmt.Println("Dependencies:")
	for _, dep := range info.Deps {
		printModule(dep)
	}
	return nil
}
