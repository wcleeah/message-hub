package messagehub

import "errors"

var (
	ClientConnectedErr = errors.New("Client can only connect to hub once")
)
