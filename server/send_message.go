package main

import (
	"errors"
	"net"

	pb "github.com/puizinam/go-rustdesk-server/server/internal/protocol"
	"google.golang.org/protobuf/proto"
)

// This function sends a RendezvousMessage over a TCP connection and returns an error if the write fails.
func sendRendezvousMessageOverTcp(conn net.Conn, rendezvous_message *pb.RendezvousMessage) error {
	serialized_rendezvous_message, _ := proto.Marshal(rendezvous_message)
	// The message has been serialized to bytes, but before we can send it we need to prepend it
	// with a *length prefix* so that the recipient knows how many bytes to expect.
	// The maximum message size is limited to 2^30 bits which means that the length of the message
	// can always be represented by a length prefix of 4 bytes.
	// Because we don't want to transmit unnecessary bytes, the length prefix can be anywhere from
	// 1 to 4 bytes long. In other words, it will always use the least amount of bytes necessary
	// to describe the length of the message that follows it.
	// Because the size of the length prefix is variable, we also need a way to let the recipient know
	// how many bytes make up the length prefix itself.
	// In order to achieve this, we append *two bits* to the length prefix by shifting to the left:
	// 00: the prefix is 1 byte long
	// 01: the prefix is 2 bytes long
	// 10: the prefix is 3 bytes long
	// 11: the prefix is 4 bytes long
	// Because those two bits don't actually carry information about the length of the message,
	// the following applies:
	// If the length prefix is 1 byte long, the maximum incoming message length is actually 2^6 bits, not 2^8.
	// If the length prefix is 2 bytes long, the maximum incoming message length is actually 2^14 bits, not 2^16.
	// If the length prefix is 3 bytes long, the maximum incoming message length is actually 2^22 bits, not 2^24.
	// If the length prefix is 4 bytes long, the maximum incoming message length is actually 2^30 bits, not 2^32.
	// The recipient will know this and discard those two bits to get the correct message length.
	// Right now we get the length of the serialized message (which is the number of bytes it is made up of—exactly
	// what the length prefix needs to be) and append two 0 bits which will later be used indicate the size
	// of the length prefix itself.
	length_prefix := len(serialized_rendezvous_message) << 2
	var length_prefix_bytes []byte // We need to convert the prefix to a slice of bytes
	// Convert to a slice of bytes in little-endian format
	// NOTE: Converting to little-endian means that the two 0s we appended at the end will now be the last two bits of the first byte.
	for length_prefix > 0 {
		least_significant_byte := byte(length_prefix & 0xFF) // Bitwise AND with a byte full of 1s to get the last byte
		length_prefix_bytes = append(length_prefix_bytes, least_significant_byte)
		length_prefix >>= 8
	}
	// Because we now have the length prefix as a slice of bytes in litte-endian format, we can just call len() to get
	// the number of bytes necessary for the length prefix.
	length_prefix_size := byte(len(length_prefix_bytes))
	if length_prefix_size > 4 {
		return errors.New("message too large")
	}
	// We are now sure that the length prefix size is within the allowed range of 1 to 4 bytes.
	// This means that length_prefix_size is a byte where only the last two bits can be non-zero.
	// We perform bitwise OR on the least significant byte of the prefix (which is the first byte in
	// little-endian)—the first six bits won't be changed by the six 0s, but the last two bits will be
	// modified to indicate the size of the length prefix itself.
	// The only thing we need to be careful about is the following mismatch:
	// The length prefix can be 1 to 4 bytes long, but two bits can represent numbers from 0 to 3.
	// This is why we subtract 1 from the length prefix size before going through with the bitwise OR.
	// This results in the following mapping:
	// Prefix is 1 byte long: 00
	// Prefix is 2 bytes long: 01
	// Prefix is 3 bytes long: 10
	// Prefix is 4 bytes long: 11
	length_prefix_bytes[0] |= length_prefix_size - 1
	message := append(length_prefix_bytes, serialized_rendezvous_message...)
	_, err := conn.Write(message)
	return err
}

// This function sends a RendezvousMessage as a UDP packet to a provided destination address. It uses the global UDP listener
// on RENDEZVOUS_PORT. It returns an error if the write fails.
func sendRendezvousMessageOverUdp(rendezvous_message *pb.RendezvousMessage, destination_addr net.Addr) error {
	serialized_rendezvous_message, _ := proto.Marshal(rendezvous_message)
	_, err := rendezvous_port_udp_listener.WriteTo(serialized_rendezvous_message, destination_addr)
	return err
}

func logSendingError(port *Port, destination_addr net.Addr, message_type string, err error) {
	logErrorOnPort(port, "Failed to send a "+message_type+" message to "+destination_addr.String(), err)
}
