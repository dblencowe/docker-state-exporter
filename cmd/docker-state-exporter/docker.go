package main

import (
	"context"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// DockerClient is the small subset of the Docker SDK's *client.Client surface
// that the collector consumes. Defined here (consumer side) so tests can
// substitute a fake.
//
// Per AGENTS.md §3.4, two-method interfaces are ideal; four is a deliberate
// deviation (§5) — every method is genuinely used, and *client.Client
// satisfies this for free with no wrapping.
type DockerClient interface {
	ContainerList(ctx context.Context, opts container.ListOptions) ([]types.Container, error)
	ContainerInspect(ctx context.Context, id string) (types.ContainerJSON, error)
	Ping(ctx context.Context) (types.Ping, error)
	Close() error
}

// newDockerClient builds a Docker client that reads connection details from
// the standard DOCKER_* environment variables and negotiates the API version
// with the daemon. Negotiation is lazy on first request, so callers should
// Ping() at startup to surface incompatibility immediately.
func newDockerClient() (*client.Client, error) {
	return client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
}
