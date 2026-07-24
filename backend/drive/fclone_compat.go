package drive

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var fcloneDriveIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{6,}$`)

type fcloneRootSpec struct {
	id          string
	path        string
	resourceKey string
}

// fcloneCanonicalRoot preserves the user's direct-ID expression for rclone's
// global Fs cache while the Drive dir cache uses only the relative suffix.
func fcloneCanonicalRoot(spec fcloneRootSpec) string {
	root := "{" + spec.id
	if spec.resourceKey != "" {
		root += "?resourcekey=" + url.QueryEscape(spec.resourceKey)
	}
	root += "}"
	if spec.path != "" {
		root += "/" + spec.path
	}
	return root
}

// parseFcloneRootSpec parses the historical fclone/gclone syntax:
//
//	remote:{OBJECT_ID}
//	remote:{OBJECT_ID}/sub/path
//	remote:{https://drive.google.com/drive/folders/OBJECT_ID}/sub/path
//
// The braces deliberately make the syntax opt-in and avoid changing normal
// rclone path interpretation.
func parseFcloneRootSpec(path string) (spec fcloneRootSpec, ok bool, err error) {
	if !strings.HasPrefix(path, "{") {
		return spec, false, nil
	}
	const open = 0
	closeOffset := strings.IndexByte(path[1:], '}')
	if closeOffset < 0 {
		return spec, false, fmt.Errorf("fclone: Drive ID expression is missing a closing brace")
	}
	close := open + 1 + closeOffset
	raw := strings.TrimSpace(path[1:close])
	if raw == "" {
		return spec, false, fmt.Errorf("fclone: Drive ID expression is empty")
	}

	id, resourceKey, err := fcloneIDAndResourceKeyFromString(raw)
	if err != nil {
		return spec, false, err
	}
	remainder := path[close+1:]
	if remainder != "" && !strings.HasPrefix(remainder, "/") {
		return spec, false, fmt.Errorf("fclone: characters after a Drive ID expression must start with '/'")
	}
	suffix := strings.Trim(remainder, "/")
	return fcloneRootSpec{id: id, path: suffix, resourceKey: resourceKey}, true, nil
}

func fcloneIDFromString(value string) (string, error) {
	id, _, err := fcloneIDAndResourceKeyFromString(value)
	return id, err
}

func fcloneIDAndResourceKeyFromString(value string) (id, resourceKey string, err error) {
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" {
		if parsed.Scheme != "https" && parsed.Scheme != "http" {
			return "", "", fmt.Errorf("fclone: %q is not a supported Google Drive URL", value)
		}
		host := strings.ToLower(parsed.Hostname())
		if host != "drive.google.com" && host != "docs.google.com" {
			return "", "", fmt.Errorf("fclone: %q is not a supported Google Drive URL", value)
		}
		query := parsed.Query()
		resourceKey = query.Get("resourcekey")
		if resourceKey == "" {
			resourceKey = query.Get("resourceKey")
		}
		if id := query.Get("id"); fcloneDriveIDPattern.MatchString(id) {
			return id, resourceKey, nil
		}
		segments := strings.FieldsFunc(parsed.Path, func(r rune) bool { return r == '/' })
		for i, segment := range segments {
			switch segment {
			case "folders", "files":
				mobileURL := containsString(segments[:i], "mobile")
				if !mobileURL && i+1 < len(segments) && fcloneDriveIDPattern.MatchString(segments[i+1]) {
					return segments[i+1], resourceKey, nil
				}
				// Some historical mobile URLs inserted an intermediate path
				// segment. Only use the compatibility scan for an actual mobile
				// URL, and exclude known UI action suffixes.
				if mobileURL {
					for j := len(segments) - 1; j > i; j-- {
						candidate := segments[j]
						if candidate == "preview" || candidate == "sharing" || candidate == "edit" || candidate == "view" {
							continue
						}
						if fcloneDriveIDPattern.MatchString(candidate) {
							return candidate, resourceKey, nil
						}
					}
				}
			case "file", "document", "spreadsheets", "presentation", "drawings", "forms":
				// File links and the Docs editors use /d/ID. Account-aware
				// links may insert /u/N before /d/, so scan only the next few
				// structural segments rather than treating a UI suffix as an ID.
				for j, last := i+1, min(i+4, len(segments)-1); j < last; j++ {
					if segments[j] == "d" && fcloneDriveIDPattern.MatchString(segments[j+1]) {
						return segments[j+1], resourceKey, nil
					}
				}
			}
		}
		return "", "", fmt.Errorf("fclone: %q does not contain a supported Google Drive ID", value)
	}
	id = value
	if rawID, rawQuery, found := strings.Cut(value, "?"); found {
		id = rawID
		query, parseErr := url.ParseQuery(rawQuery)
		if parseErr != nil {
			return "", "", fmt.Errorf("fclone: invalid Drive ID query: %w", parseErr)
		}
		for key := range query {
			if key != "resourcekey" && key != "resourceKey" {
				return "", "", fmt.Errorf("fclone: unsupported Drive ID query parameter %q", key)
			}
		}
		values := append([]string(nil), query["resourcekey"]...)
		values = append(values, query["resourceKey"]...)
		if len(values) != 1 || values[0] == "" {
			return "", "", fmt.Errorf("fclone: Drive ID query requires one non-empty resourcekey")
		}
		resourceKey = values[0]
	}
	if !fcloneDriveIDPattern.MatchString(id) {
		return "", "", fmt.Errorf("fclone: %q is not a valid Google Drive ID", id)
	}
	return id, resourceKey, nil
}

// FcloneFileName reports the actual name of an object addressed with {ID}.
// cmd.NewFsFile uses this instead of treating the opaque ID as a file name.
func (f *Fs) FcloneFileName() string {
	return f.fcloneFileName
}
