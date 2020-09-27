// This files holds the structure and utility functions used by the
// Command & Control data channel (aka cdc

package main

import (
	"encoding/json"
	"fmt"
	"github.com/creack/pty"
	"strconv"
	"strings"
)

const AValidTokenForTests = "THEoneANDonlyTOKEN"

// ErrorArgs is a type that holds the args for an error message
type NAckArgs struct {
	// Desc hold the textual desciption of the error
	Desc string `json:"desc"`
	// Ref holds the message id the error refers to or 0 for system errors
	Ref int `json:"ref"`
}

// AuthArgs is a type that holds client's authentication arguments.
type AuthArgs struct {
	Token string `json:"token"`
}

// AckArgs is a type to hold the args for an Ack message
type AckArgs struct {
	// Ref holds the message id the error refers to or 0 for system errors
	Ref  int             `json:"ref"`
	Body json.RawMessage `json:"body"`
}

// SetPayloadArgs is a type to hold the args for a set_payload type of a message
type SetPayloadArgs struct {
	// Ref holds the message id the error refers to or 0 for system errors
	Payload json.RawMessage `json:"payload"`
}

// ResizeArgs is a type that holds the argumnet to the resize pty command
type ResizeArgs struct {
	PaneID int    `json:"pane_id"`
	Sx     uint16 `json:"sx"`
	Sy     uint16 `json:"sy"`
}

// CTRLMessage type holds control messages passed over the control channel
type CTRLMessage struct {
	Time      int64       `json:"time"`
	MessageId int         `json:"message_id"`
	Type      string      `json:"type"`
	Args      interface{} `json:"args"`
}

// IsAuthorized checks whether a client token is authorized
func IsAuthorized(token string) bool {
	tokens, err := ReadAuthorizedTokens()
	if err != nil {
		Logger.Error(err)
	}
	for _, at := range tokens {
		if token == at {
			return true
		}
	}
	return false
}

// parseWinsize gets a string in the format of "24x80" and returns a Winsize
func ParseWinsize(s string) (*pty.Winsize, error) {
	var sy int64
	var sx int64
	var err error
	Logger.Infof("Parsing window size: %q", s)
	dim := strings.Split(s, "x")
	sx, err = strconv.ParseInt(dim[1], 10, 16)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse number of rows: %v", err)

	}
	sy, err = strconv.ParseInt(dim[0], 0, 16)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse number of cols: %v", err)
	}
	return &pty.Winsize{uint16(sy), uint16(sx), 0, 0}, nil
}
