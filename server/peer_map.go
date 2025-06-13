package main

import (
	"net"
	"sync"
)

type Peer struct {
	addr            net.Addr
	uuid            []byte
	pk              []byte // Public key
	last_registered int64
}

var peer_map_mutex sync.Mutex
var peer_map = make(map[string]*Peer) // Maps client IDs to Peer structs
