// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Tetragon

package multiplexer

import (
	"context"
	"fmt"
	"time"

	"github.com/cilium/tetragon/api/v1/tetragon"
	"github.com/cilium/tetragon/pkg/logger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

const (
	defaultConnectRetries = 10
	defaultConnectBackoff = time.Second
)

// GetEventsResult encapsulates a GetEventsResponse and an error
type GetEventsResult struct {
	*tetragon.GetEventsResponse
	Error error
}

// ClientMultiplexer multiplexes one or more GetEvents clients into a single stream
type ClientMultiplexer struct {
	clients        []tetragon.FineGuidanceSensorsClient
	connectRetries int
	connectBackoff time.Duration
}

// NewClientMultiplexer constructs a new ClientMultiplexer.
func NewClientMultiplexer() *ClientMultiplexer {
	return &ClientMultiplexer{
		clients:        []tetragon.FineGuidanceSensorsClient{},
		connectRetries: defaultConnectRetries,
		connectBackoff: defaultConnectBackoff,
	}
}

// WithConnectRetries updates the number of attempts this multiplexer will make to connect
// to each gRPC server. The default is 10.
func (cm *ClientMultiplexer) WithConnectRetries(retries uint) *ClientMultiplexer {
	cm.connectRetries = int(retries)
	return cm
}

// WithConnectBackoff updates the backoff time between connection attempts to each gRPC
// server. The default is 1 second.
func (cm *ClientMultiplexer) WithConnectBackoff(backoff time.Duration) *ClientMultiplexer {
	cm.connectBackoff = backoff
	return cm
}

func ConnectAttempt(ctx context.Context, addr string) (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", addr, err)
	}

	// Deprecation of grpc.DialContext made us switch to grpc.NewClient
	// which does not perform any I/O, the following statement should
	// maintain the previous "connect" behavior by waiting for client
	// connection.
	for {
		switch state := conn.GetState(); state {
		case connectivity.Ready:
			return conn, nil
		case connectivity.Idle:
			conn.Connect()
			fallthrough
		case connectivity.Connecting:
			ok := conn.WaitForStateChange(ctx, state)
			if !ok {
				return nil, fmt.Errorf("conn for %s did not change from %s: %w", addr, state, ctx.Err())
			}
		case connectivity.TransientFailure, connectivity.Shutdown:
			return nil, fmt.Errorf("conn for %s in state %s bailing out", addr, state)
		default:
			return nil, fmt.Errorf("%s: unknown conn state: %s", addr, state)
		}

	}
}

func (cm *ClientMultiplexer) SetConns(conns []*grpc.ClientConn) {
	for _, conn := range conns {
		client := tetragon.NewFineGuidanceSensorsClient(conn)
		cm.clients = append(cm.clients, client)
	}
}

// GetEventsWithFilters calls GetEvents for each client in the multiplexer and returns a channel that
// multiplexes the GetEventsResponses. allowList and denyList can be used to filter what
// events we care about.
func (cm *ClientMultiplexer) GetEvents(ctx context.Context, allowList, denyList []*tetragon.Filter) (chan GetEventsResult, error) {
	c := make(chan GetEventsResult)

	for _, client := range cm.clients {
		var stream tetragon.FineGuidanceSensors_GetEventsClient
		var err error
		for range cm.connectRetries {
			stream, err = client.GetEvents(ctx, &tetragon.GetEventsRequest{
				AllowList: allowList,
				DenyList:  denyList,
			})
			if err == nil {
				break
			}
			time.Sleep(cm.connectBackoff)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to get events after %d tries", cm.connectRetries)
		}
		go func(stream tetragon.FineGuidanceSensors_GetEventsClient) {
			for {
				select {
				case <-ctx.Done():
					logger.GetLogger().Debug("ClientMultiplexer: Context cancelled, stopping GetEvents goroutine")
					return
				default:
				}
				res, err := stream.Recv()
				// We've occasionally been running into errors in e2e tests about invalid
				// wire format messages and message size mismatches in protobuf. According
				// to https://github.com/golang/protobuf/issues/1609, this is generally
				// related to concurrency issues such as modifying a message during
				// marshalling. Add a Clone() call before sending the event over the
				// channel to mitigate this issue.
				//
				// Although this isn't great for performance, this is fine to do
				// here as a quick workaround since we're only using this code
				// for testing purposes anyway. In other words, this will never
				// make it to a production environment.
				if err != nil {
					res = proto.Clone(res).(*tetragon.GetEventsResponse)
				}
				c <- GetEventsResult{res, err}
			}
		}(stream)
	}

	return c, nil
}
