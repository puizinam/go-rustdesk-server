package main

import (
	"net"
	"sync"
)

// This map is used to map client socket addresses to the TCP connections over which they sent a PunchHoleRequest
// or RelayRequest to initiate a signaling session.
var signaling_sessions = make(map[string]net.Conn)
var signaling_sessions_mutex sync.Mutex

// Every client's TCP connection is handled by a separate goroutine. Therefore, when the relay server needs to faciliate
// a connection between two peers, the two goroutines need access to each other's TCP connections so that they can read
// from their connection and write to the other one. This map is used to map a specific relay connection UUID to a channel
// over which the goroutines will exchange the TCP connections.
var relay_exchange_channels = make(map[string]chan net.Conn)
var relay_exchange_channels_mutex sync.Mutex

func addSignalingSession(addr net.Addr, conn net.Conn) {
	socket_address := addr.(*net.TCPAddr)
	IPv4 := socket_address.IP.To4()
	if IPv4 != nil {
		socket_address.IP = IPv4
	}
	signaling_sessions_mutex.Lock()
	signaling_sessions[addr.String()] = conn
	signaling_sessions_mutex.Unlock()
}
