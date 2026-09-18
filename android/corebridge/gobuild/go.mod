module hole/androidbind

go 1.26.0

tool (
	golang.org/x/mobile/cmd/gobind
	golang.org/x/mobile/cmd/gomobile
)

require hole v0.0.0

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/pion/dtls/v3 v3.1.8 // indirect
	github.com/pion/ice/v4 v4.4.2 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/mdns/v2 v2.2.0 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/stun/v4 v4.0.0 // indirect
	github.com/pion/transport/v4 v4.1.0 // indirect
	github.com/pion/turn/v5 v5.1.0 // indirect
	github.com/quic-go/quic-go v0.54.0 // indirect
	github.com/wlynxg/anet v0.0.5 // indirect
	go.uber.org/mock v0.5.0 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/mobile v0.0.0-20260908204917-8b95e45f8d3e // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/time v0.14.0 // indirect
	golang.org/x/tools v0.50.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace hole => ../../..

replace github.com/wlynxg/anet => ../../../core/compat/anet
