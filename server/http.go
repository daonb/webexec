package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"

	"github.com/rs/cors"
)

type ConnectAPI struct {
	Offer string
}

// ConnectHandler lintnes for POST requests on /connect.
// A valid request should have an encoded WebRTC offer as its body.
func ConnectHandler() (h http.Handler, e error) {
	s, e := NewWebRTCServer()
	if e != nil {
		// MT: In general I don't like named return values. It's not clear what is returned here. A 'return nil, e' would be more explicit IMO
		return
	}
	mux := http.NewServeMux()
	// MT: When handlers are more than few lines, I write them in a "proper" function outside
	mux.HandleFunc("/connect", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			// MT: You set the error on every call to /connect? Do you assume there will be only one? How can the caller to ConnectHandler know *when* to check for error
			e = fmt.Errorf("Got an http request with bad method %q\n", r.Method)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		decoder := json.NewDecoder(r.Body)
		// MT: You can use anonymouse structure here
		// var c struct {
		//     Offer string
		// }
		var c ConnectAPI
		e := decoder.Decode(&c)
		log.Printf("Got a valid POST request with data: %v", c)
		if e != nil {
			e = fmt.Errorf("Failed to decode client's key: %v", e)
			return
		}
		peer := s.Listen(c.Offer)
		// reply with server's key
		w.Write(peer.Answer)
	})
	h = cors.Default().Handler(mux)
	return
}

func NewHTTPListner() (l net.Listener, p int, e error) {
	// MT: Why random port?
	l, e = net.Listen("tcp", ":0")
	if e != nil {
		return
	}
	p = l.Addr().(*net.TCPAddr).Port
	return
}

// MT: This function should in main or return the error from http.ListenAndServer
func HTTPGo(address string) (e error) {
	h, e := ConnectHandler()
	if e != nil {
		return
	}
	// MT: Only main should exit the program
	log.Fatal(http.ListenAndServe(address, h))

	return
}
