# go-rustdesk-server
A rendezvous/relay server for the open-source remote desktop software RustDesk (https://github.com/rustdesk/rustdesk), written in Go.

Only the necessary functionality to connect clients is implemented. There are no extra features such as those offered by the official RustDesk server (https://github.com/rustdesk/rustdesk-server).

## Testing the server
If you want to test the server yourself, you can follow the steps below:

#### 1. Create a VM in the cloud with a public IP address
- Allow TCP on ports 21115-21117 and UDP on port 21116

#### 2. Build the executable
- Download or clone this repository on your local machine
- Install Go if you don't have it
- Build the executable for the correct OS and CPU architecture

    - Find out which `GOOS` and `GOARCH` values correspond to **your VM's OS and CPU architecture**
    - Most likely you will be compiling for `linux` and `amd64`
    - Assuming you are in the `go-rustdesk-server/` directory, execute the following commands

        ```sh
        go run ./cli/keygen
        go run ./cli/compile linux amd64
        ```

    - As a result of these commands, a `dist/` directory will be created at `go-rustdesk-server/`

        ```
        dist/
        |—— keys.env
        |—— (compiled executable for target platform)
        ```

#### 3. Configure both RustDesk clients you want to connect
- On each client, open the RustDesk network settings

    <p>
    <img src="https://rustdesk.com/docs/en/self-host/client-configuration/images/network-config.png" alt="RustDesk network settings" width="400">
    </p>

- Set the "ID server" and "Relay server" fields to your VM's public IP address (e.g. 142.250.190.14)
- Before setting the "Key" field, you first need to open the `keys.env` file that was generated at `dist/` in the previous step

    ```
    PRIVATE_KEY="some_base64_encoded_private_key"
    PUBLIC_KEY="some_base64_encoded_public_key"
    ```

- Back in the network settings, set the "Key" field to the **PUBLIC_KEY** (without the quotation marks)

#### 4. Transfer the contents of `dist/` (the executable and `keys.env`) to your VM
- You can use a tool like `scp`

    `scp keys.env <executable> user@remote_host:/home/user/my-server/`
- The files **must be placed in the same directory**

#### 5. Run the server
- Assuming you are on Linux and positioned in the same directory as the executable

    - `chmod +x server` to make `server` executable
    - `./server rendezvous` to run the rendezvous server
    - `./server relay` to run the relay server