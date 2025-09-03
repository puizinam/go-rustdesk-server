package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"math/big"
	"net"
	"os"
	"os/signal"
	"slices"
	"sync"
	"time"

	pb "github.com/puizinam/go-rustdesk-server/server/internal/protocol"
	"golang.org/x/crypto/nacl/sign"
	"google.golang.org/protobuf/proto"
)

type Port struct {
	name        string
	number      string
	socket_type string
}

// Rendezvous server ports
var NAT_PORT = &Port{
	name:        "NAT_PORT",
	number:      ":21115",
	socket_type: "TCP",
}
var RENDEZVOUS_PORT_TCP = &Port{
	name:        "RENDEZVOUS_PORT",
	number:      ":21116",
	socket_type: "TCP",
}
var RENDEZVOUS_PORT_UDP = &Port{
	name:        "RENDEZVOUS_PORT",
	number:      ":21116",
	socket_type: "UDP",
}

// Relay server port
var RELAY_PORT = &Port{
	name:        "RELAY_PORT",
	number:      ":21117",
	socket_type: "TCP",
}

// The UDP listener on RENDEZVOUS_PORT is the sole UDP listener on the rendezvous server.
// After it gets bound to RENDEZVOUS_PORT, it is assigned to this global variable so
// that the rendezvous server can freely use it from anywhere to send UDP messages to clients.
var rendezvous_port_udp_listener net.PacketConn

// Context for graceful server shutdown instead of using os.Exit
var server_ctx, cleanup_and_terminate = signal.NotifyContext(context.Background(), os.Interrupt)

// For the sake of cleaner logs on the rendezvous server, the UDP listener will wait for the TCP listeners (connections) to close first
var wg_tcp_listeners sync.WaitGroup

// To ensure complete logs, the server will wait for all listeners to close before exiting
var wg_all_listeners sync.WaitGroup

func main() {
	invalid_args_message := `Failed to start server. The program expects a single command-line argument:
    'rendezvous': starts the rendezvous server
    'relay': starts the relay server`
	if len(os.Args) != 2 {
		fmt.Println(invalid_args_message)
		return
	}

	switch os.Args[1] {
	case "rendezvous":
		log.Print("Rendezvous server started.")
		defer log.Print("Rendezvous server terminated.")
		go listenOnTcp(NAT_PORT)
		go listenOnTcp(RENDEZVOUS_PORT_TCP)
		go listenOnUdp(RENDEZVOUS_PORT_UDP)
	case "relay":
		log.Print("Relay server started.")
		defer log.Print("Relay server terminated.")
		go listenOnTcp(RELAY_PORT) // Technically don't need a goroutine for a single port but it's easier to reuse listenOnTcp()
	default:
		fmt.Println(invalid_args_message)
		return
	}

	<-server_ctx.Done()
	wg_all_listeners.Wait()
}

func listenOnTcp(port *Port) {
	tcp_listener, err := net.Listen("tcp", port.number)
	if err != nil {
		logErrorOnPort(port, BINDING_ERROR, err)
		cleanup_and_terminate() // <-server_ctx.Done()
		return
	}
	// Binding successful
	// Increment the number of active listeners
	wg_all_listeners.Add(1)
	wg_tcp_listeners.Add(1)
	var wg_conn_goroutines sync.WaitGroup // WaitGroup counter for active connections
	// Goroutine that handles server shutdown
	go func() {
		<-server_ctx.Done()
		wg_conn_goroutines.Wait() // Wait for all connections to close
		closeListener(port, tcp_listener)
	}()
	logEventOnPort(port, "Listening for connections...")
	for {
		conn, err := tcp_listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				// Listener was closed by server shutdown
				// Decrement the number of active listeners and exit the goroutine
				wg_tcp_listeners.Done()
				wg_all_listeners.Done()
				return
			}
			logErrorOnPort(port, "Failed to accept a connection", err)
			continue
		}
		logEventOnPort(port, "Established a connection with "+conn.RemoteAddr().String())
		wg_conn_goroutines.Add(1) // Increment the number of active connections
		go handleTcpConnection(conn, port, &wg_conn_goroutines)
	}
}

func listenOnUdp(port *Port) {
	udp_listener, err := net.ListenPacket("udp", port.number)
	if err != nil {
		logErrorOnPort(port, BINDING_ERROR, err)
		cleanup_and_terminate() // <-server_ctx.Done()
		wg_tcp_listeners.Wait()
		return
	}
	// Binding successful
	// Assign the listener to the global variable
	rendezvous_port_udp_listener = udp_listener
	// Increment the number of active listeners
	wg_all_listeners.Add(1)
	// Goroutine that handles server shutdown
	go func() {
		<-server_ctx.Done()
		wg_tcp_listeners.Wait() // Wait for the TCP listeners to close first
		closeListener(port, udp_listener)
	}()
	// Start reading packets
	logEventOnPort(port, "Waiting for packets...")
	buffer := make([]byte, 1024)
	for {
		n, sender_addr, err := udp_listener.ReadFrom(buffer)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				// Listener was closed by server shutdown
				// Decrement the number of active listeners and exit the goroutine
				wg_all_listeners.Done()
				return
			}
			logErrorOnPort(RENDEZVOUS_PORT_UDP, "Reading error occurred on packet received from "+sender_addr.String(), err)
		}
		// If some bytes were read, ignore the reading error and handle what was read anyways
		if n > 0 {
			// All ports expect messages in RendezvousMessage format as defined in the rendezvous.proto file.
			rendezvous_message := parseRendezvousMessage(buffer[:n], port, sender_addr)
			if rendezvous_message == nil { // Packet is not a RendezvousMessage
				continue
			}
			// Received a valid RendezvousMessage, check if it is supported on this port
			message_type := getRendezvousMessageType(rendezvous_message)
			if !isSupportedMessageType(message_type, port, sender_addr) {
				continue
			}
			// A supported RendezvousMessage arrived on RENDEZVOUS_PORT_UDP
			RENDEZVOUS_PORT_UDP__handleRendezvousMessage(message_type, rendezvous_message, sender_addr)
		}
	}
}

func handleTcpConnection(conn net.Conn, port *Port, wg_conn_goroutines *sync.WaitGroup) {
	// Goroutine that handles server shutdown
	go func() {
		<-server_ctx.Done()
		conn.Close()
	}()
	// Continuously read from the connection
	for {
		// Unlike UDP (a packet-oriented protocol), TCP is a stream-oriented protocol. Therefore, we have to
		// construct messages from the incoming bytes ourselves.
		message, err := readMessageFromTcpStream(conn)
		if err != nil {
			// Connection was closed before a complete message was received, check who closed it
			if errors.Is(err, net.ErrClosed) {
				// The connection was closed by server shutdown
				logEventOnPort(port, "Closed connection with "+conn.RemoteAddr().String())
			} else {
				// The connection is still open on our end, which means that the client closed it
				conn.Close()
				logEventOnPort(port, "Connection with "+conn.RemoteAddr().String()+" has been closed by the client.")
			}
			wg_conn_goroutines.Done() // Decrement the number of active connections
			return                    // Exit the goroutine
		}
		// All ports expect messages in RendezvousMessage format as defined in the rendezvous.proto file
		rendezvous_message := parseRendezvousMessage(message, port, conn.RemoteAddr())
		if rendezvous_message == nil { // The message is not a RendezvousMessage
			continue
		}
		// Received a valid RendezvousMessage, check if it is supported on this port
		message_type := getRendezvousMessageType(rendezvous_message)
		if !isSupportedMessageType(message_type, port, conn.RemoteAddr()) {
			continue
		}
		switch port {
		case NAT_PORT: // A supported RendezvousMessage arrived on NAT_PORT
			NAT_PORT__handleRendezvousMessage(message_type, rendezvous_message, conn)
		case RENDEZVOUS_PORT_TCP: //  A supported RendezvousMessage arrived on RENDEZVOUS_PORT_TCP
			RENDEZVOUS_PORT_TCP__handleRendezvousMessage(message_type, rendezvous_message, conn)
		case RELAY_PORT: // A supported RendezvousMessage arrived on RELAY_PORT
			RELAY_PORT__handleRendezvousMessage(message_type, rendezvous_message, conn)
		}
	}
}

// By using a specific codec, read bytes from a TCP stream until they form a complete message.
// If a message is read successfully, it is returned as a slice of bytes with no error. If the
// connection closes before all of the message's bytes could be read, the function returns nil
// along with an error.
func readMessageFromTcpStream(conn net.Conn) ([]byte, error) {
	first_byte := make([]byte, 1)
	_, err := io.ReadFull(conn, first_byte)
	if err != nil {
		// Connection closed before the first byte was read
		return nil, err
	}
	// The first byte that we just read is part of the *length prefix*.
	// The length prefix is a number that can be from 1 to 4 bytes long.
	// The length prefix indicates the byte length of the message that follows it.
	// Because the size of the length prefix is variable, we first need to determine the
	// size of the prefix itself before we can read the message.
	// The size of the length prefix is stored in the *last two bits* of the first incoming byte:
	// 00: the prefix is 1 byte long (but in decimal 00 is 0)
	// 01: the prefix is 2 bytes long (but in decimal 01 is 1)
	// 10: the prefix is 3 bytes long (but in decimal 10 is 2)
	// 11: the prefix is 4 bytes long (but in decimal 11 is 3)
	// Bitwise AND with 0b11 to retrieve the last two bits and add 1 to describe the size
	length_prefix_size := (first_byte[0] & 0b11) + 1
	var length_prefix_bytes []byte // Slice of bytes that make up the prefix
	if length_prefix_size > 1 {
		// We only got the first byte of the length prefix, we need to read the rest
		remaining_length_prefix_bytes := make([]byte, length_prefix_size-1)
		_, err = io.ReadFull(conn, remaining_length_prefix_bytes)
		if err != nil {
			// Connection closed before the remaining length prefix bytes were read
			return nil, err
		}
		length_prefix_bytes = append(first_byte, remaining_length_prefix_bytes...)
	} else {
		// The length prefix is only 1 byte long and consists of the first byte that we had just read
		length_prefix_bytes = first_byte
	}
	// We have now read the bytes that make up the length prefix.
	// To determine the length of the incoming message, we need to convert those bytes into a number.
	// The length prefix bytes are transmitted in little-endian format.
	// We will convert the length prefix bytes to a number and shift it two bits to the right in order to
	// discard the last two bits that were only used to describe the length of the prefix itself.
	// Yes, this means the following:
	// If the length prefix is 1 byte long, the maximum incoming message length is 2^6 bits, not 2^8.
	// If the length prefix is 2 bytes long, the maximum incoming message length is 2^14 bits, not 2^16.
	// If the length prefix is 3 bytes long, the maximum incoming message length is 2^22 bits, not 2^24.
	// If the length prefix is 4 bytes long, the maximum incoming message length is 2^30 bits, not 2^32.
	if length_prefix_size < 4 {
		// Because the length prefix can at most be 4 bytes long, we will naturally express it as a 32-bit number.
		// In this case, the length prefix takes up fewer than 4 bytes so we need pad it with extra "zero" bytes to
		// make it 4 bytes long. The reason for this is that the conversion function binary.LittleEndian.Uint32() will
		// panic if the provided slice of bytes is fewer than 4 bytes long, resulting in an "index out of range" error.
		// We *append* the padding bytes because in little-endian the last bytes are the most significant ones, and
		// therefore after conversion our "zero" bytes will just sit in front of the actual length prefix bytes.
		padding_bytes := make([]byte, 4-length_prefix_size)
		length_prefix_bytes = append(length_prefix_bytes, padding_bytes...)
	}
	message_length := binary.LittleEndian.Uint32(length_prefix_bytes) >> 2
	message := make([]byte, message_length)
	_, err = io.ReadFull(conn, message)
	if err != nil {
		// Connection closed before all of the message's bytes were read
		return nil, err
	}
	return message, nil
}

// This function handles RendezvousMessages that arrive on the NAT_PORT.
func NAT_PORT__handleRendezvousMessage(message_type string, rendezvous_message *pb.RendezvousMessage, conn net.Conn) {
	switch message_type {
	case TEST_NAT_REQUEST:
		sendTestNatResponse(NAT_PORT, conn)
	}
}

func sendTestNatResponse(port *Port, conn net.Conn) {
	// TestNatResponse contains the client's port number
	test_nat_response := &pb.RendezvousMessage{
		Union: &pb.RendezvousMessage_TestNatResponse{
			TestNatResponse: &pb.TestNatResponse{
				Port: int32(conn.RemoteAddr().(*net.TCPAddr).Port),
			},
		},
	}
	// Send the response to the client
	err := sendRendezvousMessageOverTcp(conn, test_nat_response)
	if err == nil {
		logEventOnPort(port, "Sent a TestNatResponse to "+conn.RemoteAddr().String())
	} else {
		logSendingError(port, conn.RemoteAddr(), TEST_NAT_RESPONSE, err)
	}
}

// This function handles RendezvousMessages that arrive on the RENDEZVOUS_PORT over a TCP connection.
func RENDEZVOUS_PORT_TCP__handleRendezvousMessage(message_type string, rendezvous_message *pb.RendezvousMessage, conn net.Conn) {
	switch message_type {
	case TEST_NAT_REQUEST:
		sendTestNatResponse(RENDEZVOUS_PORT_TCP, conn)
	case PUNCH_HOLE_REQUEST:
		punch_hole_request := rendezvous_message.GetPunchHoleRequest()
		if punch_hole_request.LicenceKey != PUBLIC_KEY {
			logEventOnPort(RENDEZVOUS_PORT_TCP, "Received a PunchHoleRequest with a mismatched licence key from "+conn.RemoteAddr().String())
			sendPunchHoleFailure(RENDEZVOUS_PORT_TCP, conn, pb.PunchHoleResponse_LICENSE_MISMATCH)
			return
		}
		// Peer A wants to connect to peer B
		peer_A_addr := conn.RemoteAddr()
		peer_B_id := punch_hole_request.Id
		peer_map_mutex.Lock()
		peer_B, is_registered := peer_map[peer_B_id]
		peer_map_mutex.Unlock()
		if !is_registered {
			logEventOnPort(RENDEZVOUS_PORT_TCP,
				"Unsuccessful PunchHoleRequest from "+peer_A_addr.String()+" (Other peer is not registered on the server)",
			)
			sendPunchHoleFailure(RENDEZVOUS_PORT_TCP, conn, pb.PunchHoleResponse_ID_NOT_EXIST)
			return
		}
		if time.Now().Unix()-peer_B.last_registered > 30 {
			logEventOnPort(RENDEZVOUS_PORT_TCP,
				"Unsuccessful PunchHoleRequest from "+peer_A_addr.String()+" to "+peer_B.addr.String()+" (Target peer is offline)",
			)
			sendPunchHoleFailure(RENDEZVOUS_PORT_TCP, conn, pb.PunchHoleResponse_OFFLINE)
			return
		}
		// Peer B is registered and online. Store peer A's TCP connection in the signaling_sessions map so it can
		// later be retrieved to send the response.
		addSignalingSession(peer_A_addr, conn)
		// The server will now try to facilitate a direct connection between peer A and peer B.
		// First, check if peer A and peer B are behind the same NAT (and therefore on the same network) or if they are
		// behind different NATs (and therefore on different networks).
		// If they are behind the same NAT, they will have used the same public IP address to communicate with the server.
		// Peer A sent a PunchHoleRequest over a TCP connection, and peer B (like all peers) previously registered with
		// the server over UDP (RegisterPeer and RegisterPk).
		addr_A := peer_A_addr.(*net.TCPAddr)
		addr_B := peer_B.addr.(*net.UDPAddr)
		// The server will send a message to peer B (it will differ depending on if the peers are in the same network or not)
		var message_to_send *pb.RendezvousMessage
		var type_of_message_to_send string
		if addr_A.IP.Equal(addr_B.IP) {
			logEventOnPort(RENDEZVOUS_PORT_TCP,
				"Client "+peer_A_addr.String()+" wants to connect to peer "+peer_B.addr.String()+" who is in the same network",
			)
			type_of_message_to_send = FETCH_LOCAL_ADDR
			message_to_send = &pb.RendezvousMessage{
				Union: &pb.RendezvousMessage_FetchLocalAddr{
					FetchLocalAddr: &pb.FetchLocalAddr{
						SocketAddr: obfuscateSocketAddress(*addr_A),
						// Relay server runs on the same system as the rendezvous server
						RelayServer: conn.LocalAddr().(*net.TCPAddr).IP.String(),
					},
				},
			}
		} else {
			logEventOnPort(RENDEZVOUS_PORT_TCP,
				"Client "+peer_A_addr.String()+" wants to connect to peer "+peer_B.addr.String()+" who is in a different network",
			)
			type_of_message_to_send = PUNCH_HOLE
			message_to_send = &pb.RendezvousMessage{
				Union: &pb.RendezvousMessage_PunchHole{
					PunchHole: &pb.PunchHole{
						SocketAddr: obfuscateSocketAddress(*addr_A),
						// This lets peer B know if peer A is behind an (a)symmetric NAT
						NatType: punch_hole_request.NatType,
						// Relay server runs on the same system as the rendezvous server
						RelayServer: conn.LocalAddr().(*net.TCPAddr).IP.String(),
					},
				},
			}
		}
		err := sendRendezvousMessageOverUdp(message_to_send, peer_B.addr)
		if err != nil {
			logSendingError(RENDEZVOUS_PORT_TCP, peer_B.addr, type_of_message_to_send, err)
		}
	case LOCAL_ADDR:
		la := rendezvous_message.GetLocalAddr()
		peer_B_id := la.Id
		serialized_peer_B_IdPk := getSerializedIdPk(peer_B_id)
		punch_hole_response := &pb.RendezvousMessage{
			Union: &pb.RendezvousMessage_PunchHoleResponse{
				PunchHoleResponse: &pb.PunchHoleResponse{
					SocketAddr:  la.LocalAddr,
					Pk:          sign.Sign(nil, serialized_peer_B_IdPk, PRIVATE_KEY),
					RelayServer: la.RelayServer,
					Union: &pb.PunchHoleResponse_IsLocal{
						IsLocal: true,
					},
				},
			},
		}
		obfuscated_peer_A_addr := la.SocketAddr
		peer_A_addr := deobfuscateSocketAddress(obfuscated_peer_A_addr)
		logEventOnPort(RENDEZVOUS_PORT_TCP,
			"Peer "+conn.RemoteAddr().String()+" responded to PunchHoleRequest from "+peer_A_addr+" with a LocalAddr message",
		)
		sendPunchHoleResponse(peer_A_addr, punch_hole_response)
	case PUNCH_HOLE_SENT:
		punch_hole_sent := rendezvous_message.GetPunchHoleSent()
		peer_B_id := punch_hole_sent.Id
		serialized_peer_B_IdPk := getSerializedIdPk(peer_B_id)
		punch_hole_response := &pb.RendezvousMessage{
			Union: &pb.RendezvousMessage_PunchHoleResponse{
				PunchHoleResponse: &pb.PunchHoleResponse{
					SocketAddr:  obfuscateSocketAddress(*conn.RemoteAddr().(*net.TCPAddr)),
					Pk:          sign.Sign(nil, serialized_peer_B_IdPk, PRIVATE_KEY),
					RelayServer: punch_hole_sent.RelayServer,
					Union: &pb.PunchHoleResponse_NatType{
						NatType: punch_hole_sent.NatType,
					},
				},
			},
		}
		peer_A_addr := deobfuscateSocketAddress(punch_hole_sent.SocketAddr)
		logEventOnPort(RENDEZVOUS_PORT_TCP,
			"Peer "+conn.RemoteAddr().String()+" responded to PunchHoleRequest from "+peer_A_addr+" with a PunchHoleSent message",
		)
		sendPunchHoleResponse(peer_A_addr, punch_hole_response)
	case REQUEST_RELAY:
		request_relay := rendezvous_message.GetRequestRelay()
		// Peer A is requesting a relay to peer B
		peer_A_addr := conn.RemoteAddr()
		addSignalingSession(peer_A_addr, conn)
		peer_B_id := request_relay.Id
		peer_map_mutex.Lock()
		peer_B := peer_map[peer_B_id]
		peer_map_mutex.Unlock()
		logEventOnPort(RENDEZVOUS_PORT_TCP, peer_A_addr.String()+" is requesting a relay with "+peer_B.addr.String())
		request_relay.SocketAddr = obfuscateSocketAddress(*peer_A_addr.(*net.TCPAddr))
		message_to_send := &pb.RendezvousMessage{
			Union: &pb.RendezvousMessage_RequestRelay{
				RequestRelay: request_relay,
			},
		}
		err := sendRendezvousMessageOverUdp(message_to_send, peer_B.addr)
		if err != nil {
			logSendingError(RENDEZVOUS_PORT_TCP, peer_B.addr, REQUEST_RELAY, err)
		}
	case RELAY_RESPONSE:
		relay_response := rendezvous_message.GetRelayResponse()
		sender_id := relay_response.GetId()
		if sender_id != "" {
			serialized_sender_IdPk := getSerializedIdPk(sender_id)
			relay_response.Union = &pb.RelayResponse_Pk{
				Pk: sign.Sign(nil, serialized_sender_IdPk, PRIVATE_KEY),
			}
		}
		peer_A_addr := deobfuscateSocketAddress(relay_response.SocketAddr)
		relay_response.SocketAddr = nil
		logEventOnPort(RENDEZVOUS_PORT_TCP, "Peer "+conn.RemoteAddr().String()+" sent a RelayResponse for "+peer_A_addr)
		signaling_sessions_mutex.Lock()
		peer_A_conn := signaling_sessions[peer_A_addr]
		delete(signaling_sessions, peer_A_addr)
		signaling_sessions_mutex.Unlock()
		message_to_send := &pb.RendezvousMessage{
			Union: &pb.RendezvousMessage_RelayResponse{
				RelayResponse: relay_response,
			},
		}
		err := sendRendezvousMessageOverTcp(peer_A_conn, message_to_send)
		if err != nil {
			logSendingError(RENDEZVOUS_PORT_TCP, peer_A_conn.RemoteAddr(), RELAY_RESPONSE, err)
		}
	}
}

func sendPunchHoleFailure(port *Port, conn net.Conn, failure_type pb.PunchHoleResponse_Failure) {
	punch_hole_failure_response := &pb.RendezvousMessage{
		Union: &pb.RendezvousMessage_PunchHoleResponse{
			PunchHoleResponse: &pb.PunchHoleResponse{
				Failure: failure_type,
			},
		},
	}
	err := sendRendezvousMessageOverTcp(conn, punch_hole_failure_response)
	if err != nil {
		logSendingError(port, conn.RemoteAddr(), PUNCH_HOLE_RESPONSE, err)
	}
}

func getSerializedIdPk(peer_id string) []byte {
	peer_map_mutex.Lock()
	peer_pk := peer_map[peer_id].pk
	peer_map_mutex.Unlock()
	peer_B_IdPk := &pb.IdPk{
		Id: peer_id,
		Pk: peer_pk,
	}
	serialized_IdPk, _ := proto.Marshal(peer_B_IdPk)
	return serialized_IdPk
}

func sendPunchHoleResponse(peer_A_addr string, punch_hole_response *pb.RendezvousMessage) {
	signaling_sessions_mutex.Lock()
	peer_A_conn := signaling_sessions[peer_A_addr]
	// Peer A is about to get a response to their PunchHoleRequest, delete their "punch hole" session
	delete(signaling_sessions, peer_A_addr)
	signaling_sessions_mutex.Unlock()
	err := sendRendezvousMessageOverTcp(peer_A_conn, punch_hole_response)
	if err != nil {
		logSendingError(RENDEZVOUS_PORT_TCP, peer_A_conn.RemoteAddr(), PUNCH_HOLE_RESPONSE, err)
	}
}

// Obfuscate socket addresses when sending them inside packets because some NATs inspect
// payloads for these addresses and modify them (which could interfere with the signaling
// process). Of course, recipients will have to know how to decode these obfuscated addresses.
func obfuscateSocketAddress(address net.TCPAddr) []byte {
	IPv4 := address.IP.To4()
	var obfuscated_address []byte
	if IPv4 != nil {
		address.IP = IPv4
		// Some notes:
		// big.NewInt() expects int64 so we have to cast
		// UnixMicro() timestamp is reduced to uint32 to make it more manageable
		// To perform operations on the IP address, its 4 bytes are converted to a little-endian integer
		timestamp := big.NewInt(int64(uint32(time.Now().UnixMicro())))
		ip := big.NewInt(int64(binary.LittleEndian.Uint32([]byte(address.IP))))
		port := big.NewInt(int64(address.Port))
		obfuscation := new(big.Int)
		// obfuscate the 32-bit IP address by summing it with the 32-bit timestamp
		// ip + timestamp
		obfuscation.Add(ip, timestamp)
		// append 49 zeros at the end
		// 32 zeros will be replaced by the original 32-bit timestamp and 17 zeros will be replaced by the obsfuscated port
		// (ip + timestamp) << 49
		obfuscation.Lsh(obfuscation, 49)
		// append 17 zeros to the 32-bit timestamp
		// bitwise OR with the now 49-bit timestamp
		// the original 32-bit timestamp is now stored in place of zeros between bits 18 and 49
		// the leftover 17 zeros are for the port
		// ((ip + timestamp) << 49) | (timestamp << 17)
		obfuscation.Or(obfuscation, new(big.Int).Lsh(timestamp, 17))
		// perform bitwise AND with 0xFFFF on the timestamp to get the last 16 bits
		// obfuscate the port by summing it with the last 16 bits of the timestamp
		// the sum of the 16-bit port number and the last 16 bits of the timestamp cannot be larger than 17 bits
		// bitwise OR to embed the sum in the place of the 17 zeros
		// ((ip + timestamp) << 49) | (timestamp << 17) | (port + (timestamp & 0xFFFF))
		obfuscation.Or(obfuscation, new(big.Int).Add(port, new(big.Int).And(timestamp, big.NewInt(math.MaxUint16))))
		// big-endian
		obfuscated_address = obfuscation.Bytes()
		// reverse to get little-endian
		slices.Reverse(obfuscated_address)
	} else {
		// Obfuscation is focused on IPv4 addresses, for IPv6 we just append the port in little-endian
		ip_bytes := []byte(address.IP)
		port_bytes := make([]byte, 2)
		binary.LittleEndian.PutUint16(port_bytes, uint16(address.Port))
		obfuscated_address = append(ip_bytes, port_bytes...)
	}
	return obfuscated_address
}

func deobfuscateSocketAddress(obfuscated_bytes []byte) string {
	var deobfuscated_address net.TCPAddr
	if len(obfuscated_bytes) == 18 { // IPv6
		deobfuscated_address = net.TCPAddr{
			// IP is just first 16 bytes
			IP: obfuscated_bytes[:16],
			// Port is the last 2 bytes in little-endian
			Port: int(binary.LittleEndian.Uint16(obfuscated_bytes[16:])),
		}
	} else { // IPv4
		// Get the large obfuscation number in big-endian
		slices.Reverse(obfuscated_bytes)
		obfuscation := new(big.Int).SetBytes(obfuscated_bytes)
		// To get the IP and port, we first need to extract the 32-bit timestamp which was used to obfuscate them.
		// Because we know that the timestamp is stored between bits 18 and 49, we remove the last 17 bits by
		// shifting to the right and perform a bitwise AND with 32 binary 1s to retrieve the original 32-bit timestamp.
		// (obfuscation >> 17) & (0xFFFFFFFF)
		timestamp := new(big.Int).And(new(big.Int).Rsh(obfuscation, 17), big.NewInt(math.MaxUint32))
		// The IP address is stored in the bits before the 32-bit timestamp and the 17-bit obfuscated port. Therefore, we remove the last
		// 49 bits by shifting to the right. Because the IP address was obfuscated by summing it with the timestamp, we just subtract the
		// timestamp from the remaining bits and convert the result to a slice of bytes to get the original IP address.
		// (obfuscation >> 49) - timestamp
		ip := new(big.Int).Sub(new(big.Int).Rsh(obfuscation, 49), timestamp).Bytes()
		// Because the IP address bytes were converted to little-endian before obfuscation, we reverse them back to normal big-endian.
		slices.Reverse(ip)
		// Finally, the 16-bit port was also obfuscated by summing it with the timestamp's last 16 bits. We retrieve the last 17 bits by
		// performing a bitwise AND with 0x1FFFF and from that we subtract the last 16 bits of the timestamp to get the original port number.
		// (obfuscation & 0x1FFFF) - (timestamp & 0xFFFF)
		port := new(big.Int).Sub(new(big.Int).And(obfuscation, big.NewInt(0x1FFFF)), new(big.Int).And(timestamp, big.NewInt(math.MaxUint16)))
		deobfuscated_address = net.TCPAddr{
			IP:   ip,
			Port: int(port.Uint64()),
		}
	}

	return deobfuscated_address.String()
}

// This function handles RendezvousMessages that arrive on the RENDEZVOUS_PORT as UDP packets.
func RENDEZVOUS_PORT_UDP__handleRendezvousMessage(message_type string, rendezvous_message *pb.RendezvousMessage, sender_addr net.Addr) {
	switch message_type {
	case REGISTER_PEER:
		logEventOnPort(RENDEZVOUS_PORT_UDP, "RegisterPeer from "+sender_addr.String())
		register_peer := rendezvous_message.GetRegisterPeer()
		client_id := register_peer.Id
		peer_map_mutex.Lock()
		peer, is_registered := peer_map[client_id]
		if is_registered {
			peer.last_registered = time.Now().Unix()
		}
		peer_map_mutex.Unlock()
		need_public_key := !is_registered
		register_peer_response := &pb.RendezvousMessage{
			Union: &pb.RendezvousMessage_RegisterPeerResponse{
				RegisterPeerResponse: &pb.RegisterPeerResponse{
					RequestPk: need_public_key,
				},
			},
		}
		err := sendRendezvousMessageOverUdp(register_peer_response, sender_addr)
		if err == nil {
			if need_public_key {
				logEventOnPort(RENDEZVOUS_PORT_UDP, "Requesting public key from "+sender_addr.String())
			}
		} else {
			logSendingError(RENDEZVOUS_PORT_UDP, sender_addr, REGISTER_PEER_RESPONSE, err)
		}
	case REGISTER_PK: // PK = Public Key
		register_pk := rendezvous_message.GetRegisterPk()
		client_id := register_pk.Id
		client_pk := register_pk.Pk
		peer_map_mutex.Lock()
		_, is_registered := peer_map[client_id]
		if is_registered {
			logEventOnPort(RENDEZVOUS_PORT_UDP, "Received RegisterPk from an already registered client "+sender_addr.String())
		} else {
			peer_map[client_id] = &Peer{
				addr:            sender_addr,
				pk:              client_pk,
				last_registered: time.Now().Unix(),
			}
			logEventOnPort(RENDEZVOUS_PORT_UDP, "Registered a new peer "+sender_addr.String())
		}
		peer_map_mutex.Unlock()
		register_pk_response := &pb.RendezvousMessage{
			Union: &pb.RendezvousMessage_RegisterPkResponse{
				RegisterPkResponse: &pb.RegisterPkResponse{
					Result: pb.RegisterPkResponse_OK,
				},
			},
		}
		err := sendRendezvousMessageOverUdp(register_pk_response, sender_addr)
		if err != nil {
			logSendingError(RENDEZVOUS_PORT_UDP, sender_addr, REGISTER_PK_RESPONSE, err)
		}
	}
}

// This function handles RendezvousMessages that arrive on the RELAY_PORT.
func RELAY_PORT__handleRendezvousMessage(message_type string, rendezvous_message *pb.RendezvousMessage, conn net.Conn) {
	switch message_type {
	case REQUEST_RELAY:
		request_relay := rendezvous_message.GetRequestRelay()
		if request_relay.LicenceKey != PUBLIC_KEY {
			logEventOnPort(RELAY_PORT, "Received RequestRelay with a mismatched licence key from "+conn.RemoteAddr().String())
		} else {
			logEventOnPort(RELAY_PORT, "Received RequestRelay from "+conn.RemoteAddr().String())
		}
		relay_conn_id := request_relay.Uuid
		relay_exchange_channels_mutex.Lock()
		exchange_channel, ok := relay_exchange_channels[relay_conn_id]
		relay_exchange_channels_mutex.Unlock()
		if ok {
			exchange_channel <- conn              // Send the connection to the goroutine that handles the other peer
			other_peer_conn := <-exchange_channel // Wait for the goroutine to respond with the other peer's connection
			// Relay connection established, delete the map entry and start the relay
			relay_exchange_channels_mutex.Lock()
			delete(relay_exchange_channels, relay_conn_id)
			relay_exchange_channels_mutex.Unlock()
			logEventOnPort(RELAY_PORT,
				"Relay connection established between "+conn.RemoteAddr().String()+" and "+other_peer_conn.RemoteAddr().String(),
			)
			relay(conn, other_peer_conn)
		} else {
			// Other peer has not yet requested a relay from their side, make an entry in the relay_exchange_channels map
			exchange_channel := make(chan net.Conn)
			relay_exchange_channels_mutex.Lock()
			relay_exchange_channels[relay_conn_id] = exchange_channel
			relay_exchange_channels_mutex.Unlock()
			select {
			case <-server_ctx.Done():
				return
			case <-time.After(30 * time.Second): // Give the other peer 30 seconds to request the relay
				relay_exchange_channels_mutex.Lock()
				delete(relay_exchange_channels, relay_conn_id)
				relay_exchange_channels_mutex.Unlock()
				logEventOnPort(RELAY_PORT, "Other peer did not request a relay in time to pair with "+conn.RemoteAddr().String())
				return
			case other_peer_conn := <-exchange_channel:
				// The other peer has requested a relay and the goroutine that handles their connection just sent us
				// their relay conn, now we respond with our own connection and start the relay
				exchange_channel <- conn
				relay(conn, other_peer_conn)
			}
		}
	}
}

func relay(conn_A net.Conn, conn_B net.Conn) {
	bytes := make([]byte, 10*1024*1024) // 10 MB buffer
	for {
		n, reading_err := conn_A.Read(bytes)
		_, writing_err := conn_B.Write(bytes[:n])
		if reading_err != nil || writing_err != nil {
			// One of the connections closed, stop the relay. We can just return without any extra cleanup because
			// once we exit the relay, the server will go back to trying to read RendezvousMessages from the closed
			// connection. This will trigger an error which the goroutine will handle and exit gracefully.
			return
		}
	}
}
