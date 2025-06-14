package internal

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

const REPOSITORY_NAME = "go-rustdesk-server"
const BUILD_DIR_NAME = "dist"

var BUILD_DIRECTORY_ERROR = fmt.Sprintf("failed to create the %s/ directory", BUILD_DIR_NAME)

// This function creates a <build_directory>/ at the root of the repository and returns its absolute path.
// If the operations fails, an error is returned instead.
func CreateBuildDirectory() (string, error) {
	working_directory, err := os.Getwd()
	if err != nil {
		return "", err
	}

	repository_root_dir := findRepositoryRootDir(working_directory)
	if repository_root_dir == "" {
		return "", fmt.Errorf(
			"failed to find a directory containing %q in the current working directory %s",
			REPOSITORY_NAME, working_directory,
		)
	}

	// Create the build directory
	build_dir := repository_root_dir + string(os.PathSeparator) + BUILD_DIR_NAME
	err = os.MkdirAll(build_dir, os.ModePerm)
	if err != nil {
		return "", err
	}

	return build_dir, nil
}

// This function takes a file path and, within that file path, tries to find the REPOSITORY_ROOT/ directory.
// If REPOSITORY_ROOT/ is found, its absolute path is returned. Otherwise, the function returns an empty string.
func findRepositoryRootDir(path string) string {
	// Keep trimming the last element of the path until it points to REPOSITORY_ROOT/ or
	// until the system's root directory is reached.
	for {
		if strings.HasSuffix(path, string(os.PathSeparator)) {
			return "" // Reached the file system's root directory
		}
		// Call strings.Contains() because directory names like "REPOSITORY_NAME-main" can exist for branches.
		if strings.Contains(filepath.Base(path), REPOSITORY_NAME) {
			return path // Found the REPOSITORY_ROOT/ directory
		}
		path = filepath.Dir(path)
	}
}

func LogFatalError(message string, err error) {
	if err != nil {
		log.Fatalf("Error: %s [%v]", message, err)
	}
}
