package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/pion/webrtc/v3"
	"github.com/stretchr/testify/require"
)

func TestConnect(t *testing.T) {
	initTest(t)
	done := make(chan bool)
	port, err := GetFreePort()
	require.Nil(t, err, "Fauiled to find a free tcp port", err)
	host := fmt.Sprintf("127.0.0.1:%d", port)
	// Start the https server
	go HTTPGo(host)
	// start the webrtc client
	client, cert, err := NewClient(true)
	require.Nil(t, err, "Failed to start a new server", err)
	cdc, err := client.CreateDataChannel("%", nil)
	require.Nil(t, err, "Failed to create the control data channel: %q", err)
	clientOffer, err := client.CreateOffer(nil)
	require.Nil(t, err, "Failed to create client offer: %q", err)
	gatherComplete := webrtc.GatheringCompletePromise(client)
	err = client.SetLocalDescription(clientOffer)
	require.Nil(t, err, "Failed to set client's local Description client offer: %q", err)
	select {
	case <-time.After(3 * time.Second):
		t.Errorf("timed out waiting to ice gathering to complete")
	case <-gatherComplete:
		var sd webrtc.SessionDescription
		buf := make([]byte, 4096)
		l, err := EncodeOffer(buf, *client.LocalDescription())
		require.Nil(t, err, "Failed ending an offer: %v", clientOffer)
		p := ConnectRequest{cert, 1, string(buf[:l])}
		b, err := json.Marshal(p)
		require.Nil(t, err, "Failed to marshal the connect request: %s", err)
		url := fmt.Sprintf("http://%s/connect", host)
		r, err := http.Post(url, "application/json", bytes.NewBuffer(b))
		require.Nil(t, err, "Failed sending a post request: %q", err)
		defer r.Body.Close()
		require.Equal(t, r.StatusCode, http.StatusOK,
			"Server returned error status: %r", r)
		// read server offer
		serverOffer := make([]byte, 4096)
		l, err = r.Body.Read(serverOffer)
		require.Equal(t, err, io.EOF, "Failed reading resonse body: %v", err)
		require.Less(t, 600, l,
			"Got a bad length response: %d", l)
		err = DecodeOffer(&sd, serverOffer[:l])
		require.Nil(t, err, "Failed decoding an offer: %v", clientOffer)
		client.SetRemoteDescription(sd)
		// count the incoming messages
		cdc.OnOpen(func() {
			done <- true
		})
	}
	select {
	case <-time.After(3 * time.Second):
		t.Errorf("Timeouton cdc open")
	case <-done:
	}
	/*
		// There's t.Cleanup in go 1.15+
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		err := Shutdown(ctx)
		require.Nil(t, err, "Failed shutting the http server: %v", err)
	*/
	Shutdown()
	// TODO: be smarter, this is just a hack to get github action to pass
	time.Sleep(500 * time.Millisecond)
}
