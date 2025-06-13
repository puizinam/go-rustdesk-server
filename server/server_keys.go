package main

import (
	"encoding/base64"
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"github.com/puizinam/go-rustdesk-server/internal"
)

var PUBLIC_KEY string
var PRIVATE_KEY *[64]byte

// This function runs before main()
func init() {
	err := godotenv.Load(internal.DOTENV_FILENAME)
	if err != nil {
		fmt.Printf("Failed to start server: could not set public and private key [%v]\n", err)
		os.Exit(1)
	}
	// The public key can stay as a base64 encoded string, whereas the private key needs to be decoded into
	// bytes so it can be used to sign data
	PUBLIC_KEY = os.Getenv("PUBLIC_KEY")
	private_key_bytes, _ := base64.StdEncoding.DecodeString(os.Getenv("PRIVATE_KEY"))
	PRIVATE_KEY = (*[64]byte)(private_key_bytes)
	fmt.Println("Public and private key set.")
}
