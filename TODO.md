## Websocket processing
- [x] response
- [x] fragmentation handling
- [x] python script to interactively tests websocket

## Websocket
- [x] refactor / decouple web socket logic from the message hub
- [x] register conn, keep track of liveliness, and the type of connection
- [x] context cancelling (graceful shutdown for a connection)
- [ ] integrate with message hub

## The event handling part
- [ ] construct the websocket protocol, with type and action
- [ ] separate client and producer connection
- [ ] allow subscription on events

## Interactive testing
- [ ] a better way to handle server shutdown, so all things can quit gracefully
- [x] add an option to force quit
