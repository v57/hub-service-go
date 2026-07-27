// Command basic demonstrates the hub-service-go SDK: load identity, register a
// post handler, declare a network share, connect to Hub, and publish a runtime
// share once the listener port is known.
package main

import (
	"context"
	"log"
	"time"

	"github.com/v57/channel-go/channelgo"
	hubservice "github.com/v57/hub-service-go"
	"github.com/v57/hub-service-go/network"
)

func main() {
	svc, err := hubservice.New(hubservice.Options{
		Name:         "example",
		IdentityPath: "auth",
		Address:      1997,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer svc.Stop()

	svc.Post("hello", func(body channelgo.Body) (any, error) {
		return "world", nil
	}, hubservice.ServiceSettings())

	// Declared before Start: published on connect, republished on reconnect.
	svc.Share("example-tcp", network.ProtocolTCP, "127.0.0.1", 8080,
		&network.Policy{Mode: network.PolicyHub})

	if err := svc.Start(); err != nil {
		log.Fatal(err)
	}

	// Runtime port publication once a listener is bound.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := svc.PublishShare(ctx, "example-runtime", network.ProtocolTCP, "127.0.0.1", 9090,
		&network.Policy{Mode: network.PolicyPrivate}); err != nil {
		log.Printf("PublishShare: %v", err)
	}

	select {}
}
