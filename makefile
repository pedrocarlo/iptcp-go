all:
	go build ./cmd/vhost
	go build ./cmd/vrouter
	go build ./cmd/example

clean:
	rm -fv vhost vrouter example