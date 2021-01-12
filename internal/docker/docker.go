package docker

import (
	"context"
	"errors"
	"sync"
)

var (
	GetInitializedDb = startDbInDockerUnsupported
	StartDbInDocker  = startDbInDockerUnsupported

	ErrDockerUnsupported = errors.New("docker is not currently supported on this platform")

	mx = sync.Mutex{}
)

func startDbInDockerUnsupported(context.Context, string) (cleanup func() error, retURL string, err error) {
	return nil, "", ErrDockerUnsupported
}
