package lookup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// referenceDirsEnv names extra directories reference_lookup may read from,
// comma-separated absolute paths, for files an operator places on the worker
// rather than uploads.
const referenceDirsEnv = "HERMOD_REFERENCE_DIRS"

// defaultUploadDir is where the upload endpoint's local storage writes when
// no directory is configured (filestorage.NewStorage).
const defaultUploadDir = "uploads"

// referenceRoots are the directories a reference file must resolve inside.
//
// A workflow's config is written by any Editor, and without a bound this node
// would read any file the worker can open into records they can see. Startup
// sets the roots from the file storage configuration; until it does, the
// default upload directory and the environment's directories apply.
var referenceRoots struct {
	sync.RWMutex
	set   bool
	roots []string
}

// SetReferenceRoots replaces the directories reference files may be read
// from. A relative directory is taken relative to the working directory.
func SetReferenceRoots(dirs ...string) {
	roots := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if strings.TrimSpace(d) == "" {
			continue
		}
		if abs, err := filepath.Abs(d); err == nil {
			roots = append(roots, abs)
		}
	}
	referenceRoots.Lock()
	defer referenceRoots.Unlock()
	referenceRoots.set, referenceRoots.roots = true, roots
}

// ConfigureReferenceRoots allows the upload storage's local directory, empty
// when uploads go to object storage, plus the absolute directories listed in
// HERMOD_REFERENCE_DIRS. A relative directory in the variable is ignored: it
// would mean something different for every working directory.
func ConfigureReferenceRoots(uploadDir string) {
	dirs := []string{uploadDir}
	for _, d := range strings.Split(os.Getenv(referenceDirsEnv), ",") {
		if d = strings.TrimSpace(d); filepath.IsAbs(d) {
			dirs = append(dirs, d)
		}
	}
	SetReferenceRoots(dirs...)
}

func currentReferenceRoots() []string {
	referenceRoots.RLock()
	set, roots := referenceRoots.set, referenceRoots.roots
	referenceRoots.RUnlock()
	if !set {
		ConfigureReferenceRoots(defaultUploadDir)
		return currentReferenceRoots()
	}
	return roots
}

// allowedReferencePath resolves path -- cleaning it and following every
// symlink in it -- and returns the result if it lies inside one of the
// roots, likewise resolved. Checking the resolved path is what keeps a ../
// walk or a link inside a root from reaching a file outside it.
func allowedReferencePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	for _, root := range currentReferenceRoots() {
		r, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		if rel, err := filepath.Rel(r, resolved); err == nil && rel != ".." &&
			!strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("%s is outside the directories reference files are read from: "+
		"upload the file, or have an operator add its directory to %s", path, referenceDirsEnv)
}
