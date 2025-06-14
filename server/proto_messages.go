package main

import (
	"fmt"
	"net"
	"slices"

	pb "github.com/puizinam/go-rustdesk-server/server/internal/protocol"
	"google.golang.org/protobuf/proto"
)

// These string constants represent the types of RendezvousMessages that are defined in the .proto file.
const (
	TEST_NAT_REQUEST       = "TestNatRequest"
	TEST_NAT_RESPONSE      = "TestNatResponse"
	REGISTER_PEER          = "RegisterPeer"
	REGISTER_PEER_RESPONSE = "RegisterPeerResponse"
	REGISTER_PK            = "RegisterPk"
	REGISTER_PK_RESPONSE   = "RegisterPkResponse"
	PUNCH_HOLE_REQUEST     = "PunchHoleRequest"
	FETCH_LOCAL_ADDR       = "FetchLocalAddr"
	LOCAL_ADDR             = "LocalAddr"
	PUNCH_HOLE             = "PunchHole"
	PUNCH_HOLE_SENT        = "PunchHoleSent"
	PUNCH_HOLE_RESPONSE    = "PunchHoleResponse"
	REQUEST_RELAY          = "RequestRelay"
	RELAY_RESPONSE         = "RelayResponse"
	CONFIG_UPDATE          = "ConfigUpdate"
	SOFTWARE_UPDATE        = "SoftwareUpdate"
	PEER_DISCOVERY         = "PeerDiscovery"
	ONLINE_REQUEST         = "OnlineRequest"
	ONLINE_RESPONSE        = "OnlineResponse"
	KEY_EXCHANGE           = "KeyExchange"
	HEALTH_CHECK           = "HealthCheck"
)

// This map contains the types of RendezvousMessages that each port supports.
var SUPPORTED_MESSAGES = map[*Port][]string{
	NAT_PORT: {
		TEST_NAT_REQUEST,
	},
	RENDEZVOUS_PORT_TCP: {
		TEST_NAT_REQUEST,
		PUNCH_HOLE_REQUEST,
		LOCAL_ADDR,
		PUNCH_HOLE_SENT,
		REQUEST_RELAY,
		RELAY_RESPONSE,
	},
	RENDEZVOUS_PORT_UDP: {
		REGISTER_PEER,
		REGISTER_PK,
	},
	RELAY_PORT: {
		REQUEST_RELAY,
	},
}

// This function takes a slice of bytes as an argument—if the bytes represent a RendezvousMessage,
// the deserialized RendezvousMessage is returned. Otherwise, the function returns nil.
func parseRendezvousMessage(bytes []byte, port *Port, sender_addr net.Addr) *pb.RendezvousMessage {
	rendezvous_message := &pb.RendezvousMessage{}
	err := proto.Unmarshal(bytes, rendezvous_message)
	if err != nil {
		logErrorOnPort(port, "Received a non-RendezvousMessage from "+sender_addr.String(), err)
		return nil
	}
	return rendezvous_message
}

func getRendezvousMessageType(rendezvous_message *pb.RendezvousMessage) string {
	switch rendezvous_message.Union.(type) {
	case *pb.RendezvousMessage_TestNatRequest:
		return TEST_NAT_REQUEST
	case *pb.RendezvousMessage_TestNatResponse:
		return TEST_NAT_RESPONSE
	case *pb.RendezvousMessage_RegisterPeer:
		return REGISTER_PEER
	case *pb.RendezvousMessage_RegisterPeerResponse:
		return REGISTER_PEER_RESPONSE
	case *pb.RendezvousMessage_RegisterPk:
		return REGISTER_PK
	case *pb.RendezvousMessage_RegisterPkResponse:
		return REGISTER_PK_RESPONSE
	case *pb.RendezvousMessage_PunchHoleRequest:
		return PUNCH_HOLE_REQUEST
	case *pb.RendezvousMessage_FetchLocalAddr:
		return FETCH_LOCAL_ADDR
	case *pb.RendezvousMessage_LocalAddr:
		return LOCAL_ADDR
	case *pb.RendezvousMessage_PunchHole:
		return PUNCH_HOLE
	case *pb.RendezvousMessage_PunchHoleSent:
		return PUNCH_HOLE_SENT
	case *pb.RendezvousMessage_PunchHoleResponse:
		return PUNCH_HOLE_RESPONSE
	case *pb.RendezvousMessage_RequestRelay:
		return REQUEST_RELAY
	case *pb.RendezvousMessage_RelayResponse:
		return RELAY_RESPONSE
	case *pb.RendezvousMessage_ConfigureUpdate:
		return CONFIG_UPDATE
	case *pb.RendezvousMessage_SoftwareUpdate:
		return SOFTWARE_UPDATE
	case *pb.RendezvousMessage_PeerDiscovery:
		return PEER_DISCOVERY
	case *pb.RendezvousMessage_OnlineRequest:
		return ONLINE_REQUEST
	case *pb.RendezvousMessage_OnlineResponse:
		return ONLINE_RESPONSE
	case *pb.RendezvousMessage_KeyExchange:
		return KEY_EXCHANGE
	case *pb.RendezvousMessage_Hc:
		return HEALTH_CHECK
	default:
		return "" // This should never happen
	}
}

// This function checks if the specific type of RendezvousMessage is expected by the port it is received on.
func isSupportedMessageType(message_type string, port *Port, sender_addr net.Addr) bool {
	if slices.Contains(SUPPORTED_MESSAGES[port], message_type) {
		return true
	}
	event_msg := fmt.Sprintf("Received an unsupported RendezvousMessage (%s) from %s", message_type, sender_addr)
	logEventOnPort(port, event_msg)
	return false
}
