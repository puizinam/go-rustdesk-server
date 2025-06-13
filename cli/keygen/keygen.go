package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"

	"github.com/joho/godotenv"
	internalcli "github.com/puizinam/go-rustdesk-server/cli/internal"
	"github.com/puizinam/go-rustdesk-server/internal"
)

// This function generates a new ed25519 key pair and saves the public and private keys
// in a .env file located in the <build_directory>/. The keys are encoded in base64.
func main() {
	// Generate a new ed25519 key pair
	public_key, private_key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		internalcli.LogFatalError("failed to generate key pair", err)
	}

	// Encode the keys in base64
	public_key_base64 := base64.StdEncoding.EncodeToString(public_key)
	private_key_base64 := base64.StdEncoding.EncodeToString(private_key)

	// Create the build directory
	build_dir, err := internalcli.CreateBuildDirectory()
	if err != nil {
		internalcli.LogFatalError(internalcli.BUILD_DIRECTORY_ERROR, err)
	}

	// Create the dotenv file in the build directory
	keys_map := map[string]string{
		"PUBLIC_KEY":  public_key_base64,
		"PRIVATE_KEY": private_key_base64,
	}
	dotenv_path := build_dir + string(os.PathSeparator) + internal.DOTENV_FILENAME
	godotenv.Write(keys_map, dotenv_path)

	fmt.Printf(
		"Successfully generated %s in the %s/ directory.\n",
		internal.DOTENV_FILENAME, internalcli.BUILD_DIR_NAME,
	)
}
