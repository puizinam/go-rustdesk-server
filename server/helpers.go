package main

import (
	"io"
	"log"
)

const BINDING_ERROR = "Failed to bind listener"

func logEventOnPort(port *Port, event string) {
	log.Printf("%s(%s): %s", port.name, port.socket_type, event)
}

func logErrorOnPort(port *Port, message string, err error) {
	log.Printf("%s(%s): %s [%v]", port.name, port.socket_type, message, err)
}

func closeListener[L io.Closer](port *Port, listener L) {
	logEventOnPort(port, "Listener closed")
	listener.Close()
}
