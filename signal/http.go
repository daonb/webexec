package signal

import (
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
)

// HTTPSDPServer starts a HTTP Server that consumes SDPs
func HTTPSDPServer() chan string {
	/* MT: I prefer the non pointer version
	var port int
	flag.IntVar(&port, "port", 8080, "server port")
	*/

	port := flag.Int("port", 8080, "http server port")
	flag.Parse()

	// MT: Use buffered channel
	sdpChan := make(chan string)
	http.HandleFunc("/sdp", func(w http.ResponseWriter, r *http.Request) {
		body, _ := ioutil.ReadAll(r.Body)
		fmt.Fprintf(w, "done")
		sdpChan <- string(body)
	})

	go func() {
		// MT: addr := fmt.Sprintf(":%d", *port)
		err := http.ListenAndServe(":"+strconv.Itoa(*port), nil)
		if err != nil {
			// MT: Don't panic
			panic(err)
		}
	}()

	return sdpChan
}
