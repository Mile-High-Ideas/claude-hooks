package commitqueue

import (
	"crypto/sha1"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// RepoLabel is a short human name for a repository, from its git common
// directory: the folder holding ".git", or a bare repository's own folder.
func RepoLabel(commonDir string) string {
	if filepath.Base(commonDir) == ".git" {
		return filepath.Base(filepath.Dir(commonDir))
	}
	return filepath.Base(commonDir)
}

// RepoKey is a file-safe name unique to a repository: its label plus a
// short hash of the full common directory, so two clones that share a
// folder name do not share run logs.
func RepoKey(commonDir string) string {
	sum := sha1.Sum([]byte(commonDir))
	return FileSafe(RepoLabel(commonDir)) + "-" + hex.EncodeToString(sum[:4])
}

// FileSafe replaces every character other than letters, digits, '.', '_'
// and '-' with '_'.
func FileSafe(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// ProcessAlive reports whether a process with this id is running.
func ProcessAlive(pid int) bool { return processAlive(pid) }
