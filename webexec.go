package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/tuzig/webexec/server"
)

func attachKillHandler() {
	// MT: Make this a buffered channel
	// c := make(chan os.Signal, 1)
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		// MT: log.Println?
		// MT: Other loggers: uber/zap, logrus
		fmt.Println("\r- Ctrl+C pressed in Terminal")
		os.Exit(0)
	}()
}
func main() {
	attachKillHandler()
	log.Printf("Starting http server on port 8888")
	server.HTTPGo("0.0.0.0:8888")
	// MT: This is a busy wait. Do select {} instead
	for {
	}
}
