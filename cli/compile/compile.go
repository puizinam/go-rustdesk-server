package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	internalcli "github.com/puizinam/go-rustdesk-server/cli/internal"
)

const SOURCE_CODE_DIR_NAME = "server"

// This program compiles the server's source code and places the resulting binary in the <build_directory>/
// The program expects two command line arguments which specify the target OS and CPU architecture.
func main() {
	if len(os.Args) != 3 {
		fmt.Println("Usage: go run . <targetOS> <targetArch>")
		fmt.Println("Example: go run . linux amd64")
		return
	}
	target_OS := os.Args[1]
	target_arch := os.Args[2]

	// Create the build directory
	build_dir, err := internalcli.CreateBuildDirectory()
	if err != nil {
		internalcli.LogFatalError(internalcli.BUILD_DIRECTORY_ERROR, err)
	}

	// Retrieve the repository's root directory (the build directory's parent)
	repository_root_dir := filepath.Dir(build_dir)

	source_code_dir := repository_root_dir + string(os.PathSeparator) + SOURCE_CODE_DIR_NAME
	cmd := exec.Command("go", "build", "-o", build_dir, source_code_dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("GOOS=%s", target_OS),
		fmt.Sprintf("GOARCH=%s", target_arch),
	)
	if err := cmd.Run(); err != nil {
		internalcli.LogFatalError("failed to execute \"go build\"", err)
	}
	fmt.Printf(
		"Compilation successful.\nThe executable has been placed in the %s/ directory.\n",
		internalcli.BUILD_DIR_NAME,
	)
}
