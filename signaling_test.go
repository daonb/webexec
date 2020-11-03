package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

const TestingHost = "127.0.0.1:7888"

func TestConnect(t *testing.T) {
	Logger = zaptest.NewLogger(t).Sugar()
	done := make(chan bool)
	offerChan := make(chan webrtc.SessionDescription, 1)
	// Start the https server
	go func() {

		err := HTTPGo(TestingHost)
		require.Nil(t, err, "HTTP Listen and Server returns an error: %q", err)
	}()
	// start the webrtc client
	client, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		}})
	require.Nil(t, err, "Failed to start a new server", err)
	iceGatheringState := client.ICEGatheringState()
	if iceGatheringState != webrtc.ICEGatheringStateComplete {
		client.OnICECandidate(func(candidate *webrtc.ICECandidate) {
			if candidate == nil {
				offerChan <- *client.PendingLocalDescription()
			}
		})
	}
	clientOffer, err := client.CreateOffer(nil)
	require.Nil(t, err, "Failed to create client offer: %q", err)
	err = client.SetLocalDescription(clientOffer)
	require.Nil(t, err, "Failed to set client's local Description client offer: %q", err)
	if iceGatheringState == webrtc.ICEGatheringStateComplete {
		offerChan <- clientOffer
	}
	select {
	case <-time.After(3 * time.Second):
		t.Errorf("timed out waiting to ice gathering to complete")
	case offer := <-offerChan:
		var sd webrtc.SessionDescription
		cob := EncodeOffer(&offer)
		offerReader := strings.NewReader(cob)
		url := fmt.Sprintf("http://%s/connect", TestingHost)
		r, err := http.Post(url, "application/json", offerReader)
		require.Nil(t, err, "Failed sending a post request: %q", err)
		defer r.Body.Close()
		require.Equal(t, r.StatusCode, http.StatusOK,
			"Server returned error status: %v", r)
		// read server offer
		serverOffer, err := ioutil.ReadAll(r.Body)
		sob := string(serverOffer)
		require.Nil(t, err, "Failed reading resonse body: %v", err)
		require.Less(t, 1000, len(serverOffer),
			"Got a bad length response: %d", len(serverOffer))
		err = DecodeOffer(sob, &sd)
		client.SetRemoteDescription(sd)
		cdc, err := client.CreateDataChannel("%", nil)
		require.Nil(t, err, "Failed to create the control data channel: %q", err)
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
}
