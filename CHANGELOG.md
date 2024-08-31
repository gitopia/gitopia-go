# Change Log

All notable changes will be documented here.

## [v0.6.1] - 2024-08-31

- remove websocket connection in rpc client
- set GAS_ADJUSTMENT before estimating gas

## [v0.6.0] - 2024-08-20

- upgrade cosmos-sdk to v0.47.13
- upgrade gitopia to v4.0.0
- upgrade go to 1.21
- set gas adjustment to 1.8

## [v0.5.2] - 2024-07-10

- upgrade gitopia version to v3.3.1 (offchain sign fix)
- upgrade ledger dependencies

## [v0.5.1] - 2023-09-16

- upgrade gitopia version to v3.0.2

## [v0.5.0] - 2023-07-07

- initialize cosmos sdk client context in the command pre run handler

### upgrade instructions:
Currently, client context was partially initialized in the pre run handler and the run handler. Now, context initialization is done in one place.
- If you're using client context with commands, initialize the context in the pre run handler. fetch the context from the command in the run handler. All configurations are read from command flagset
- Otherwise, Get the client context completely initialized. limited params can be customized with arguments

## [v0.4.0] - 2023-06-07

- support broadcasting multiple transactions
- accept configs dynamically set by the user
- calculate fees for tx

## [v0.3.1] - 2023-05-17

- add support for configuring fee granter for transactions

## [v0.3.0] - 2023-03-14

- Add utility to create query client
- event handling errors will terminate the server
- add prometheus metrics
- handle context cancellation

## [v0.2.0] - 2023-02-09

- NewWSEvents method to subscribe to events
- Re-subscription on websocket reconnect
- Websocket logging
