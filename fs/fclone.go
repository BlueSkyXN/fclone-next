package fs

// FcloneVersion is the version of the fclone compatibility layer.
//
// Keep this separate from Version, which identifies the embedded rclone core.
// This makes bug reports unambiguous and lets fclone track upstream releases
// without pretending that an fclone release is an rclone release.
var FcloneVersion = "v0.1.1-dev"
