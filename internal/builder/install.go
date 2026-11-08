package builder

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrFolderExists means a new automation would overwrite a folder.
var ErrFolderExists = errors.New("folder already exists")

// Install writes files as the automation folder root/folder. With replace,
// an existing folder is swapped for the new one in a single rename, so the
// scheduler never sees half a version; hidden files in it are kept.
func Install(root, folder string, files Files, replace bool) (dir string, err error) {
	if !filePathRe.MatchString(folder) || strings.Contains(folder, "/") {
		return "", fmt.Errorf("invalid folder name %q", folder)
	}
	if probs := CheckPaths(files); HasErrors(probs) {
		return "", fmt.Errorf("invalid files: %s", probs[0])
	}
	target := filepath.Join(root, folder)
	_, statErr := os.Lstat(target)
	exists := statErr == nil
	if exists && !replace {
		return "", fmt.Errorf("%s: %w", target, ErrFolderExists)
	}
	tmp, err := os.MkdirTemp(root, ".pendolo-new-")
	if err != nil {
		return "", fmt.Errorf("cannot write to the automations folder: %w", err)
	}
	defer os.RemoveAll(tmp) // a no-op once renamed
	// Automations are code, not secrets: the runner, possibly another user, reads them.
	if err := os.Chmod(tmp, 0o755); err != nil { //nolint:gosec // see above
		return "", err
	}
	if err := Write(tmp, files); err != nil {
		return "", err
	}
	if !exists {
		return target, os.Rename(tmp, target)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			if err := os.Rename(filepath.Join(target, e.Name()), filepath.Join(tmp, e.Name())); err != nil {
				return "", err
			}
		}
	}
	old, err := os.MkdirTemp(root, ".pendolo-old-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(old)
	prev := filepath.Join(old, "prev")
	if err := os.Rename(target, prev); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Rename(prev, target)
		return "", err
	}
	return target, nil
}
